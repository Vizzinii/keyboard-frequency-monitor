package main

import (
	_ "embed"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"time"
)

//go:embed dashboard.html
var dashboardHTML []byte

// startServer 只监听 127.0.0.1。
// 非重启模式：绑定前先探测是否已有本程序实例在跑——否则第二个实例会换个端口
// 继续跑并再装一个钩子，导致每个按键被计两次；从 want 端口开始找第一个可用的。
// 重启模式：只绑定 want 端口，短重试以等旧实例退出后接管（绝不跳到相邻端口，
// 否则新旧实例会短暂并存、重复计数）。
func startServer(s *Store, h *Health, want int, restart bool) (*http.Server, int, int) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, req *http.Request) {
		rng := req.URL.Query().Get("range")
		if rng != "today" && rng != "week" && rng != "all" {
			rng = "today"
		}
		payload, err := BuildStats(s, rng)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(payload)
	})

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(h.Snapshot())
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" && req.URL.Path != "/index.html" {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(dashboardHTML)
	})

	if !restart {
		if p := probeExisting(want); p != 0 {
			return nil, 0, p
		}
		for p := want; p < want+10; p++ {
			ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
			if err != nil {
				continue
			}
			return serve(mux, ln), p, 0
		}
		return nil, 0, probeExisting(want)
	}

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(want))
	for i := 0; i < 20; i++ {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return serve(mux, ln), want, 0
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil, 0, 0
}

func serve(mux *http.ServeMux, ln net.Listener) *http.Server {
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }() // 进程退出即结束，无需优雅关闭
	return srv
}

func probeExisting(want int) int {
	client := &http.Client{Timeout: time.Second}
	for p := want; p < want+10; p++ {
		resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(p) + "/api/stats?range=all")
		if err == nil {
			resp.Body.Close()
			return p
		}
	}
	return 0
}
