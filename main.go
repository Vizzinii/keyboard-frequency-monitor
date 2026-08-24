package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	port := flag.Int("port", 8321, "面板端口，被占用时自动向后尝试")
	reset := flag.Bool("reset", false, "启动前清空所有统计数据")
	export := flag.String("export", "", "导出全部统计为 CSV 到指定文件后退出")
	openPanel := flag.Bool("open-panel", true, "启动时自动在浏览器打开面板")
	noTray := flag.Bool("no-tray", false, "不显示托盘图标（调试用，Ctrl+C 退出）")
	restart := flag.Bool("restart", false, "内部：自动重启时由旧实例拉起，跳过已有实例检测")
	watchdogOff := flag.Bool("watchdog-off", false, "关闭钩子看门狗（调试用）")
	watchdogWin := flag.Int("watchdog-win", 60, "看门狗判定窗口（秒）：该时长内无钩子事件且系统有输入则重装钩子")
	flag.Parse()

	exePath, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	exeDir := filepath.Dir(exePath)
	dbPath := filepath.Join(exeDir, "keyboard_stats.db")

	// 日志同时输出到控制台和可执行文件目录的 monitor.log，自愈/重启事件有据可查
	if lf, err := os.OpenFile(filepath.Join(exeDir, "monitor.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		defer lf.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, lf))
	}

	if *reset {
		for _, suf := range []string{"", "-wal", "-shm"} {
			if p := dbPath + suf; fileExists(p) {
				os.Remove(p)
			}
		}
		log.Println("[已清空所有统计数据]")
	}

	store, err := OpenStore(dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer store.Close()

	importLegacyJSON(store, filepath.Join(exeDir, "key_counts.json"))

	if *export != "" {
		if err := exportCSV(store, *export); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *restart {
		// 旧实例退出前仍占着端口和数据库，先等它释放再接管
		if !waitPortFree(*port, 10*time.Second) {
			log.Fatalf("自动重启：等待端口 %d 释放超时，请手动启动", *port)
		}
	}

	health := NewHealth()
	listenPort, existing := startServer(store, health, *port, *restart)
	if listenPort == 0 {
		if existing != 0 {
			fmt.Println("已有实例正在运行，直接打开它的面板。")
			openInBrowser(fmt.Sprintf("http://127.0.0.1:%d/", existing))
			return
		}
		log.Fatalf("端口 %d~%d 都被占用，请用 -port 换一个", *port, *port+9)
	}
	panelURL := fmt.Sprintf("http://127.0.0.1:%d/", listenPort)

	rec := NewRecorder()
	hooks, err := rec.StartHooks()
	if err != nil {
		// macOS 未授权“输入监控”时是一段给用户看的指引，不该带 log 前缀
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer hooks.Stop()

	go flushLoop(store, rec)

	if *watchdogOff {
		log.Println("[看门狗] 已关闭（-watchdog-off）")
		health.setOff() // 否则状态停在初始的 "ok"，托盘会谎报"记录正常"
	} else {
		wd := NewWatchdog(rec, health, time.Duration(*watchdogWin)*time.Second, restartAtFromEnv())
		go wd.Run(func() { selfRestart(store, rec, listenPort) })
	}

	fmt.Println("记录已启动（全局生效），面板地址:", panelURL)
	fmt.Println("数据每秒落盘，进程被强杀最多丢最后一秒。")

	if *openPanel {
		openInBrowser(panelURL)
	}

	if *noTray {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
	} else {
		runTray(panelURL, rec, health)
	}

	flushOnce(store, rec) // 收尾：把最后不足一秒的也写进去
	fmt.Println("已退出，数据保存在", dbPath)
}

func flushLoop(store *Store, rec *Recorder) {
	ticker := time.NewTicker(time.Second)
	for range ticker.C { // 进程退出时随主线程一起结束
		flushOnce(store, rec)
	}
}

func flushOnce(store *Store, rec *Recorder) {
	labels := rec.Drain()
	if len(labels) == 0 {
		return
	}
	now := time.Now()
	if err := store.Add(now.Format(dateLayout), now.Hour(), labels); err != nil {
		log.Println("[警告] 写入失败:", err)
	}
}

func importLegacyJSON(store *Store, path string) {
	if !fileExists(path) {
		return
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var old struct {
			Counts map[string]int64 `json:"counts"`
		}
		if json.Unmarshal(data, &old) == nil && len(old.Counts) > 0 {
			total := int64(0)
			for _, c := range old.Counts {
				total += c
			}
			if err := store.Add("legacy", 0, toIntMap(old.Counts)); err == nil {
				fmt.Printf("已导入旧版统计 %d 次（保留在“全部”里）\n", total)
			}
		}
	}
	os.Rename(path, path+".imported.bak")
}

func toIntMap(m map[string]int64) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = int(v)
	}
	return out
}

func exportCSV(store *Store, path string) error {
	keys, err := store.Keys("")
	if err != nil {
		return err
	}
	var total int64
	for _, kc := range keys {
		total += kc.Count
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"键名", "总次数", "占比%"})
	for _, kc := range keys {
		pct := "0"
		if total > 0 {
			pct = fmt.Sprintf("%.2f", float64(kc.Count)/float64(total)*100)
		}
		_ = w.Write([]string{kc.Name, fmt.Sprintf("%d", kc.Count), pct})
	}
	w.Flush()
	fmt.Printf("已导出 %d 个键位的数据到 %s\n", len(keys), path)
	return w.Error()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// waitPortFree 等待指定端口能被本机监听（旧实例退出即释放）；超时返回 false。
func waitPortFree(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for time.Now().Before(deadline) {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			ln.Close()
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// selfRestart 以 -restart 模式拉起新实例（跳过实例探测、接管旧端口），
// 写掉最后一秒数据后退出本进程。cmd.Start 失败时返回，由看门狗继续降级运行。
func selfRestart(store *Store, rec *Recorder, port int) {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("[自愈] 无法获取自身可执行文件路径: %v", err)
		return
	}
	cmd := exec.Command(exe, restartArgs(port)...)
	cmd.Env = append(os.Environ(), "KFM_RESTART_AT="+strconv.FormatInt(time.Now().Unix(), 10))
	detach(cmd) // 平台相关：见 restart_windows.go / restart_darwin.go
	if err := cmd.Start(); err != nil {
		log.Printf("[自愈] 拉起新实例失败: %v", err)
		return
	}
	flushOnce(store, rec)
	log.Printf("[自愈] 已拉起新实例（pid=%d），本进程退出", cmd.Process.Pid)
	os.Exit(0)
}

// restartArgs 构造自动重启时的命令行：保留用户原参数，但绝不携带 -reset/-export
// （否则重启会清库或直接导出退出），强制 -restart 与 -open-panel=false
// （避免再弹一个浏览器标签页），并把实际端口传下去。
func restartArgs(port int) []string {
	var out []string
	skipNext := false
	for _, a := range os.Args[1:] {
		if skipNext {
			skipNext = false
			continue
		}
		switch {
		case a == "-reset" || a == "-restart" || a == "-open-panel":
			continue
		case a == "-export":
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, "-export=") || strings.HasPrefix(a, "-open-panel=") {
			continue
		}
		out = append(out, a)
	}
	return append(out, "-restart", "-open-panel=false", "-port="+strconv.Itoa(port))
}

// restartAtFromEnv 读取 KFM_RESTART_AT（自动重启时旧实例注入的环境变量），无则返回零值。
func restartAtFromEnv() time.Time {
	s := os.Getenv("KFM_RESTART_AT")
	if s == "" {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
