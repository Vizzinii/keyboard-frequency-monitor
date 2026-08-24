package main

// 前端契约：dashboard.html 内嵌 JS 语法校验。键名可达性见 keymap_test.go 的
// TestLayoutNamesReachable（HTML 静态检查）。环境缺 node 时自动跳过。

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// TestDashboardJavaScriptSyntax 抽取 <script> 内容用 node --check 校验语法。
func TestDashboardJavaScriptSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("环境无 node，跳过 JS 语法校验")
	}
	html, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?s)<script>(.*?)</script>`)
	m := re.FindStringSubmatch(string(html))
	if m == nil {
		t.Fatal("未找到 <script> 块")
	}
	tmp := filepath.Join(t.TempDir(), "check.js")
	if err := os.WriteFile(tmp, []byte(m[1]), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "--check", tmp).CombinedOutput()
	if err != nil {
		t.Fatalf("JS 语法错误: %v\n%s", err, out)
	}
}
