package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	port := flag.Int("port", 8321, "面板端口，被占用时自动向后尝试")
	reset := flag.Bool("reset", false, "启动前清空所有统计数据")
	export := flag.String("export", "", "导出全部统计为 CSV 到指定文件后退出")
	openPanel := flag.Bool("open-panel", true, "启动时自动在浏览器打开面板")
	noTray := flag.Bool("no-tray", false, "不显示托盘图标（调试用，Ctrl+C 退出）")
	flag.Parse()

	exePath, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	exeDir := filepath.Dir(exePath)
	dbPath := filepath.Join(exeDir, "keyboard_stats.db")

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

	listenPort, existing := startServer(store, *port)
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
		runTray(panelURL, rec)
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
