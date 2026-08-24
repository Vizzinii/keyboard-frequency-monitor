package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// getFreePort 找一个空闲端口（探测后释放，供实例/服务器绑定）。
func getFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestProbeExistingIdentity 回归：probeExisting 只认带 X-KFM 标记的本程序响应，
// 不能被同端口上任意 HTTP 200 服务骗到（否则会把别人的服务误认成已有实例）。
func TestProbeExistingIdentity(t *testing.T) {
	marker := atomic.Bool{}
	marker.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if marker.Load() {
			w.Header().Set("X-KFM", "1")
		}
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	defer srv.Close()

	port, err := strconv.Atoi(strings.TrimPrefix(srv.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}

	if got := probeExisting(port); got != port {
		t.Fatalf("带标记的本程序实例应命中, got %d, want %d", got, port)
	}

	marker.Store(false) // 换成无标记的第三方服务
	if got := probeExisting(port); got != 0 {
		t.Fatalf("无标记的第三方服务不应命中, got %d", got)
	}
}

// TestStartServerOccupiedPort 非 restart：want 被无关服务占用且无本程序实例 →
// 自动顺延绑定 want+1。
func TestStartServerOccupiedPort(t *testing.T) {
	s := newTestStore(t)
	h := NewHealth()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	want := ln.Addr().(*net.TCPAddr).Port

	srv, port, existing := startServer(s, h, want, false)
	if srv == nil {
		t.Fatalf("应顺延绑定成功, existing=%d", existing)
	}
	defer srv.Close()
	if port != want+1 {
		t.Fatalf("应绑定 want+1=%d, got %d", want+1, port)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/stats?range=all", port))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-KFM") != "1" {
		t.Fatal("缺少 X-KFM 标记")
	}
}

// TestStartServerExistingInstance 已有本程序实例（带 X-KFM 标记）占用端口时，
// 非 restart 模式应识别其端口并让位（不启动新服务器）。
func TestStartServerExistingInstance(t *testing.T) {
	s := newTestStore(t)
	h := NewHealth()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-KFM", "1")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer fake.Close()
	want, err := strconv.Atoi(strings.TrimPrefix(fake.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}

	srv, _, existing := startServer(s, h, want, false)
	if srv != nil {
		srv.Close()
		t.Fatal("已有实例时不应启动新服务器")
	}
	if existing != want {
		t.Fatalf("应识别已有实例端口 %d, got %d", want, existing)
	}
}

// TestStartServerRestartTakeover restart 模式：旧实例退出后重试接管其端口。
func TestStartServerRestartTakeover(t *testing.T) {
	s := newTestStore(t)
	h := NewHealth()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	want := ln.Addr().(*net.TCPAddr).Port
	go func() {
		time.Sleep(700 * time.Millisecond) // 模拟旧实例稍后退出
		ln.Close()
	}()

	srv, port, existing := startServer(s, h, want, true)
	if srv == nil {
		t.Fatalf("restart 模式应重试接管, existing=%d", existing)
	}
	defer srv.Close()
	if port != want {
		t.Fatalf("应接管原端口 %d, got %d", want, port)
	}
}

// TestHandlersMarkersAndHeaders 三个路由的标记头、缓存策略与 404 行为。
func TestHandlersMarkersAndHeaders(t *testing.T) {
	s := newTestStore(t)
	h := NewHealth()
	port := getFreePort(t)
	srv, gotPort, _ := startServer(s, h, port, false)
	if srv == nil {
		t.Fatal("启动失败")
	}
	defer srv.Close()

	check := func(path string, wantStatus int) {
		t.Helper()
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", gotPort, path))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s → %d, want %d", path, resp.StatusCode, wantStatus)
		}
		if resp.Header.Get("X-KFM") != "1" {
			t.Fatalf("%s 缺少 X-KFM", path)
		}
	}
	check("/api/stats?range=all", 200)
	check("/api/health", 200)
	check("/", 200)
	check("/nope", 404) // 404 也带 X-KFM 标记（withMarker 在最外层），但不保证缓存头

	// 正常路由的缓存策略
	for _, p := range []string{"/api/stats?range=all", "/api/health", "/"} {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", gotPort, p))
		if err != nil {
			t.Fatal(err)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s Cache-Control = %q, want no-store", p, got)
		}
		resp.Body.Close()
	}

	// /api/stats 的 JSON 契约
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/stats?range=all", gotPort))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var payload struct {
		Range string    `json:"range"`
		Hours []int64   `json:"hours"`
		Keys  [][]any   `json:"keys"`
		Days  [][]any   `json:"days"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Hours) != 24 {
		t.Fatalf("hours 长度 = %d, want 24", len(payload.Hours))
	}
}
