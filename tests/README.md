# 键盘频率监视器 · 自包含测试模块

本目录是一个**独立、自包含的测试模块**：内含一份与仓库根目录同步的源码副本 +
全部测试（白盒单元测试 + 黑盒端到端测试）。

## 运行

```bash
cd tests
./sync.sh          # 先把根目录源码同步进本目录（改了根目录代码后必须重跑）
go test ./...      # 全部测试
```

- 网络受限环境（本机 proxy.golang.org 不通）：
  `export GOPROXY=https://goproxy.cn,direct`
- 只跑黑盒端到端：`go test -run TestE2E ./... -v`
- 只跑单元：`go test -run '^(TestStore|TestVKLabel|...)' ./...`

## 组成

| 层 | 文件 | 说明 |
| --- | --- | --- |
| L1 纯逻辑单元 | keymap/store/stats/main/watchdog_logic 的 `_test.go` | 任意平台可跑 |
| L2 窗口单元/集成 | recorder/watchdog/singleinstance 的 `_test.go` | 需要 Windows |
| L3 服务器集成 | server_test.go | httptest + 真端口 |
| L4 黑盒 E2E | e2e_test.go | 构建真 exe，起独立进程验证行为 |
| L5 前端契约 | frontend_test.go | 校验 dashboard.html 内嵌 JS 语法与键名可达性 |

## 维护规则

1. **改根目录源码后**：先 `./sync.sh` 再测试，否则测的是旧副本。
2. **测试文件只存在于本目录**：根目录不放 `*_test.go`。
3. e2e 构建的 exe 由副本编译，与单测同一份代码。

## 已知注意点

- e2e 全部实例用 `-no-hooks -watchdog-off`，**不会安装全局键盘/鼠标钩子**。
  单测中 `TestReinstallAfterThreadDeath`、`TestConcurrentReinstall` 会短暂安装真实钩子。
- 单实例场景（TestE2E_SingleInstance）会让第二个实例尝试打开浏览器面板
  （`rundll32`），测试环境无头时该调用失败并被忽略；本机跑会闪一下浏览器。
- `-reset` 与已有实例同时存在时 e2e 会验证退出码非 0（这是预期行为）。

## 抽取 / 删除

- 抽取：整个 `tests/` 目录拷贝到任意有 Go 的机器，`./sync.sh`（可选，若带源码）后
  `go test ./...` 直接跑。
- 删除：`rm -rf tests/`，根目录代码不受影响；CI 中指向本目录的步骤一并移除即可。

## 手动验证项（不在本套件内）

- 托盘 GUI 菜单交互
- 真实按键注入的计数准确性（需要模拟输入设备）
- 看门狗 → 自愈 → 自动重启的完整真机链路
