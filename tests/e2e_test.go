//go:build windows

package main

// 黑盒端到端测试：构建真实 exe，起独立进程验证 CLI / 服务器 / 单实例 / 日志等行为。
// 所有实例均以 -no-hooks -watchdog-off 启动——不安装全局钩子、不打扰真实输入。
// 进程树与临时目录在测试结束时清理。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var e2eExe string

// TestMain 构建一次 exe 供所有 e2e 场景使用。
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "kfm-e2e-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "MkdirTemp:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)
	e2eExe = filepath.Join(tmp, "KeyboardFrequencyMonitor.exe")

	out, err := exec.Command("go", "build", "-o", e2eExe, ".").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "构建 e2e exe 失败: %v\n%s", err, out)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// e2eInstance 是测试托管的一个程序实例。
type e2eInstance struct {
	cmd    *exec.Cmd
	dir    string // 实例工作目录
	exeDir string // exe 所在目录（db/日志/legacy 文件都跟随 exe 路径）
	port   int
	url    string
	exitCh chan struct{} // 进程退出时关闭
}

// instanceExe 在每个测试目录放一份 exe 副本并返回其路径。
// 实例的数据库、monitor.log、key_counts.json 都跟随 exe 所在目录（见 main.go 的
// dbPath 逻辑），所以每个实例必须有自己的 exe 副本，互不污染。
func instanceExe(t *testing.T, dir string) string {
	t.Helper()
	exeDir := filepath.Join(dir, "exe")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(exeDir, "KeyboardFrequencyMonitor.exe")
	if _, err := os.Stat(exe); err == nil {
		return exe
	}
	data, err := os.ReadFile(e2eExe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

// startInstance 启动一个实例并等待面板就绪（最多 8 秒）。
func startInstance(t *testing.T, dir string, port int, extraArgs ...string) *e2eInstance {
	t.Helper()
	exe := instanceExe(t, dir)
	args := append([]string{
		"-no-tray", "-no-hooks", "-watchdog-off", "-open-panel=false",
		"-port=" + strconv.Itoa(port),
	}, extraArgs...)
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动实例失败: %v", err)
	}
	inst := &e2eInstance{
		cmd: cmd, dir: dir, exeDir: filepath.Dir(exe), port: port,
		url:    fmt.Sprintf("http://127.0.0.1:%d", port),
		exitCh: make(chan struct{}),
	}
	go func() { _ = cmd.Wait(); close(inst.exitCh) }()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-inst.exitCh:
			t.Fatalf("实例启动后立即退出（args=%v）", args)
		default:
		}
		resp, err := http.Get(inst.url + "/api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return inst
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("实例未在 8s 内就绪（port=%d）", port)
	return nil
}

// stopInstance 结束实例进程并等待其退出。
func stopInstance(t *testing.T, inst *e2eInstance) {
	t.Helper()
	if inst.cmd.Process != nil {
		_ = inst.cmd.Process.Kill()
	}
	select {
	case <-inst.exitCh:
	case <-time.After(5 * time.Second):
		t.Log("实例未及时退出")
	}
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s → %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

// 1. 启动冒烟：面板、标记头、JSON 契约、db 与日志文件。
func TestE2E_StartupAndPanel(t *testing.T) {
	dir := t.TempDir()
	inst := startInstance(t, dir, getFreePort(t))
	defer stopInstance(t, inst)

	resp, err := http.Get(inst.url + "/api/stats?range=all")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-KFM") != "1" {
		t.Fatal("缺 X-KFM 标记头")
	}
	var payload struct {
		Range string  `json:"range"`
		Total int64   `json:"total"`
		Keys  [][]any `json:"keys"`
		Days  [][]any `json:"days"`
		Hours []int64 `json:"hours"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Hours) != 24 {
		t.Fatalf("hours 长度 = %d, want 24", len(payload.Hours))
	}
	var health struct {
		Status string `json:"status"`
	}
	getJSON(t, inst.url+"/api/health", &health)
	if health.Status != "off" { // 本套件全部 -watchdog-off
		t.Fatalf("health.status = %q, want off", health.Status)
	}
	for _, f := range []string{"keyboard_stats.db", "monitor.log"} {
		if !fileExists(filepath.Join(inst.exeDir, f)) {
			t.Fatalf("启动后缺少 %s", f)
		}
	}
}

// 2. 单实例：第二个实例应识别已有实例并退出，而不是双钩子重复计数。
func TestE2E_SingleInstance(t *testing.T) {
	dir := t.TempDir()
	port := getFreePort(t)
	a := startInstance(t, dir, port)
	defer stopInstance(t, a)

	b := exec.Command(instanceExe(t, dir), "-no-tray", "-no-hooks", "-watchdog-off",
		"-open-panel=false", "-port="+strconv.Itoa(port))
	b.Dir = dir
	b.Stdout = io.Discard
	b.Stderr = io.Discard
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- b.Wait() }()
	select {
	case <-done: // 退出即符合预期
	case <-time.After(10 * time.Second):
		_ = b.Process.Kill()
		t.Fatal("第二实例未退出——单实例互斥失效")
	}
}

// 3. 崩溃接管：强杀实例后（互斥体 abandoned），下一实例应正常接管。
func TestE2E_CrashTakeover(t *testing.T) {
	dir := t.TempDir()
	port := getFreePort(t)
	a := startInstance(t, dir, port)
	_ = a.cmd.Process.Kill()
	select {
	case <-a.exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("实例未退出")
	}

	b := startInstance(t, dir, port)
	defer stopInstance(t, b)
}

// 4. -reset：实例运行时执行必须报错退出；实例退出后执行应清空数据。
func TestE2E_Reset(t *testing.T) {
	dir := t.TempDir()
	port := getFreePort(t)

	// 已有实例 → -reset 必须失败（退出码非 0）
	a := startInstance(t, dir, port)
	cmd := exec.Command(instanceExe(t, dir), "-reset", "-no-tray", "-no-hooks", "-watchdog-off",
		"-open-panel=false", "-port="+strconv.Itoa(port))
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("-reset 在实例运行时应失败退出: %s", out)
	}
	stopInstance(t, a)

	// 实例退出后 -reset 应成功（清空后照常运行），数据归零
	b := startInstance(t, dir, port, "-reset")
	defer stopInstance(t, b)
	var payload struct {
		Total int64 `json:"total"`
	}
	getJSON(t, b.url+"/api/stats?range=all", &payload)
	if payload.Total != 0 {
		t.Fatalf("-reset 后 total = %d, want 0", payload.Total)
	}
}

// 5. -export：生成可解析的 CSV（空库时含表头）。
func TestE2E_Export(t *testing.T) {
	dir := t.TempDir()
	port := getFreePort(t)
	a := startInstance(t, dir, port)
	stopInstance(t, a)

	out := filepath.Join(dir, "out.csv")
	cmd := exec.Command(instanceExe(t, dir), "-export="+out, "-no-tray", "-no-hooks", "-watchdog-off",
		"-open-panel=false", "-port="+strconv.Itoa(port))
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("-export 失败: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimPrefix(string(data), "\ufeff") // 导出带 UTF-8 BOM，断言前剥掉
	if !strings.HasPrefix(text, "键名") || !strings.Contains(text, "占比%") {
		t.Fatalf("CSV 表头异常:\n%s", text)
	}
}

// 6. legacy 导入：放置 key_counts.json 启动 → 数据进入"全部"且文件改名归档。
func TestE2E_LegacyImport(t *testing.T) {
	dir := t.TempDir()
	exeDir := filepath.Join(dir, "exe")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exeDir, "key_counts.json"),
		[]byte(`{"counts":{"a":3,"space":2}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := startInstance(t, dir, getFreePort(t))
	defer stopInstance(t, inst)

	var payload struct {
		Total int64 `json:"total"`
	}
	getJSON(t, inst.url+"/api/stats?range=all", &payload)
	if payload.Total != 5 {
		t.Fatalf("legacy 导入 total = %d, want 5", payload.Total)
	}
	if fileExists(filepath.Join(exeDir, "key_counts.json")) {
		t.Fatal("导入成功后应改名原文件")
	}
	if !fileExists(filepath.Join(exeDir, "key_counts.json.imported.bak")) {
		t.Fatal("缺少 .imported.bak")
	}
}

// 7. 日志轮转：超过 5MB 的旧日志启动后被轮转为 .1。
func TestE2E_LogRotation(t *testing.T) {
	dir := t.TempDir()
	exeDir := filepath.Join(dir, "exe")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exeDir, "monitor.log"),
		bytes.Repeat([]byte("x"), 5<<20+1), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := startInstance(t, dir, getFreePort(t))
	defer stopInstance(t, inst)

	if !fileExists(filepath.Join(exeDir, "monitor.log.1")) {
		t.Fatal("旧日志应轮转为 .1")
	}
	if st, err := os.Stat(filepath.Join(exeDir, "monitor.log")); err != nil || st.Size() > 5<<20 {
		t.Fatalf("新日志应小于 5MB, size=%v err=%v", st, err)
	}
}

// 8. 健康 off：-watchdog-off 时 /api/health 发布 status=off。
func TestE2E_HealthOff(t *testing.T) {
	dir := t.TempDir()
	inst := startInstance(t, dir, getFreePort(t), "-watchdog-off")
	defer stopInstance(t, inst)

	var health struct {
		Status string `json:"status"`
		Msg    string `json:"msg"`
	}
	getJSON(t, inst.url+"/api/health", &health)
	if health.Status != "off" {
		t.Fatalf("status = %q, want off", health.Status)
	}
	if health.Msg == "" {
		t.Fatal("off 状态应带说明消息")
	}
}

// 9. restart 接管：旧实例退出后，-restart 模式新实例接管原端口。
func TestE2E_RestartTakeover(t *testing.T) {
	dir := t.TempDir()
	port := getFreePort(t)
	a := startInstance(t, dir, port)
	stopInstance(t, a)

	b := exec.Command(instanceExe(t, dir), "-restart", "-no-tray", "-no-hooks", "-watchdog-off",
		"-open-panel=false", "-port="+strconv.Itoa(port))
	b.Dir = dir
	b.Stdout = io.Discard
	b.Stderr = io.Discard
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = b.Process.Kill()
		_, _ = b.Process.Wait()
	}()

	deadline := time.Now().Add(15 * time.Second) // -restart 先 waitPortFree（最多 10s）再绑定
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/health", port))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return // 接管成功
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("restart 模式未接管原端口")
}
