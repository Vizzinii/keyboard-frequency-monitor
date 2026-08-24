//go:build windows

package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRestartArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"剥离危险参数，保留运行参数",
			[]string{"-port=8321", "-reset", "-open-panel=false", "-watchdog-off"},
			[]string{"-port=8321", "-watchdog-off", "-restart", "-open-panel=false", "-port=8321"}},
		{"双段式 -export 连同参数值一起剥离",
			[]string{"-export", "out.csv", "-watchdog-win=30"},
			[]string{"-watchdog-win=30", "-restart", "-open-panel=false", "-port=8321"}},
		{"-export= 等号形式剥离",
			[]string{"-export=out.csv"},
			[]string{"-restart", "-open-panel=false", "-port=8321"}},
		{"裸 -open-panel 剥离",
			[]string{"-open-panel", "-no-tray"},
			[]string{"-no-tray", "-restart", "-open-panel=false", "-port=8321"}},
	}
	for _, c := range cases {
		if got := restartArgsFrom(c.args, 8321); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %v\nwant %v", c.name, got, c.want)
		}
	}
}

func TestImportLegacyKeepsFileOnBadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key_counts.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	importLegacyJSON(newTestStore(t), path)
	if !fileExists(path) {
		t.Fatal("解析失败时不应改名原文件")
	}
}

func TestImportLegacyRenamesOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key_counts.json")
	if err := os.WriteFile(path, []byte(`{"counts":{"a":5,"space":2}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestStore(t)
	importLegacyJSON(s, path)
	if fileExists(path) {
		t.Fatal("导入成功后应改名原文件")
	}
	if !fileExists(path + ".imported.bak") {
		t.Fatal("缺少 .imported.bak")
	}
	all, err := s.Keys("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Name != "a" || all[0].Count != 5 {
		t.Fatalf("导入数据不符: %+v", all)
	}
}

// TestOpenLogAppendRotation 超过 5MB 的旧日志应轮转为 .1，新日志重建为空。
func TestOpenLogAppendRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "monitor.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 5<<20+1), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openLogAppend(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if !fileExists(path + ".1") {
		t.Fatal("旧日志应轮转为 .1")
	}
	if st, err := os.Stat(path); err != nil || st.Size() != 0 {
		t.Fatalf("新日志应重建为空, size=%v err=%v", st, err)
	}
}

// TestOpenLogAppendSmall 小日志不轮转，内容保持原样。
func TestOpenLogAppendSmall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "monitor.log")
	if err := os.WriteFile(path, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openLogAppend(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if fileExists(path + ".1") {
		t.Fatal("小日志不应轮转")
	}
	if b, _ := os.ReadFile(path); string(b) != "small" {
		t.Fatalf("小日志内容被改动: %q", b)
	}
}

// TestExportCSV 导出表头/排行/占比格式正确。
func TestExportCSV(t *testing.T) {
	s := newTestStore(t)
	_ = s.Add("2026-01-01", 9, map[string]int{"e": 100, "space": 40})
	out := filepath.Join(t.TempDir(), "out.csv")
	if err := exportCSV(s, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimPrefix(string(b), "\ufeff") // 导出带 UTF-8 BOM，断言前剥掉
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 3 { // 表头 + 两行
		t.Fatalf("行数 = %d, want 3:\n%s", len(lines), text)
	}
	if !strings.HasPrefix(lines[0], "键名") || !strings.Contains(lines[0], "占比%") {
		t.Fatalf("表头异常: %s", lines[0])
	}
	if !strings.Contains(lines[1], "e") || !strings.Contains(lines[1], "100") {
		t.Fatalf("排行首行异常: %s", lines[1])
	}
	if !strings.Contains(lines[2], "space") || !strings.Contains(lines[2], "28.57") { // 40/140
		t.Fatalf("第二行占比异常: %s", lines[2])
	}
}

// TestWaitPortFree 被占用端口不可用，释放后可立即通过。
func TestWaitPortFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	if waitPortFree(port, 500*time.Millisecond) {
		ln.Close()
		t.Fatal("被占用的端口不应视为可用")
	}
	ln.Close()
	if !waitPortFree(port, 2*time.Second) {
		t.Fatal("释放后的端口应可用")
	}
}
