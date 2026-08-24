# 键盘频率监视器 (KeyboardFrequencyMonitor)

一个轻量的 **Windows / macOS** 常驻小工具：后台统计**每个键盘按键和鼠标按键**的按下次数，
用浏览器面板查看**键位热力图、使用排行、每日趋势、时段分布**。

## 特性

- 📦 **单文件、免安装**：一个可执行文件，无运行时依赖，统计面板直接内嵌其中
- ⌨️🖱️ **全局生效**：切换到其它窗口照常统计，键盘与鼠标按键都算
- 💾 **崩溃安全**：数据每秒批量写入 SQLite，强杀进程 / 断电最多丢最后一秒，
  不依赖"正常退出"来保存
- 🌗 **双主题面板**：「月之暗面」青瓷暗色 / 「日之光面」琥珀亮色，选择自动记忆
- 🔒 **隐私优先**：只计数不记录内容；数据仅存本机，面板只监听 127.0.0.1
- 🪟 **托盘 / 菜单栏控制**：左键点图标开面板，右键可暂停记录、退出

## 下载使用

到 [Releases](../../releases) 下载对应平台的文件，放在任意你有写权限的目录：

| 平台 | 文件 |
| --- | --- |
| Windows 10/11 | `KeyboardFrequencyMonitor.exe` |
| macOS 10.15+ | `KeyboardFrequencyMonitor-macos` |

- 启动后自动在浏览器打开统计面板（`http://127.0.0.1:8321`）
- 托盘 / 菜单栏出现图标：**左键单击 = 打开面板**；右键菜单可暂停记录、退出
- 关闭浏览器标签页不影响记录；想停止记录请在菜单点"退出"

### Windows 首次运行

可能被 SmartScreen 或杀毒软件提示——因为程序使用了全局输入钩子（这是实现跨窗口
统计的必要手段），且未做代码签名。请选择"仍要运行"/添加信任。

### macOS 首次运行

macOS 要求全局键盘监听必须显式授权，程序会引导你完成两步：

1. **授予「输入监控」权限**：首次运行会弹出系统授权框；若已错过，
   到 **系统设置 → 隐私与安全性 → 输入监控** 手动勾选本程序，然后**重新启动它**。
   未授权时程序会打印指引并退出，不会静默地收不到数据。
2. **放行未签名程序**：首次可能提示"无法验证开发者"。
   到 **系统设置 → 隐私与安全性** 点"仍要打开"，或在终端执行：

   ```bash
   chmod +x KeyboardFrequencyMonitor-macos
   xattr -d com.apple.quarantine KeyboardFrequencyMonitor-macos
   ```

程序完全开源，可以自行审查或从源码构建。

## 面板功能

| 区块 | 内容 |
| --- | --- |
| 键位热力图 | 真实键盘布局，色块越亮表示该范围内按得越多（mac 上修饰键显示为 ⌘/⌥/⌃） |
| 使用排行 | Top 15 键位的次数与占比条形图 |
| 每日趋势 | 最近 14 天每天的输入总量（周末灰色显示） |
| 时段分布 | 一天中各小时的输入量（看看你几点打字最猛） |

顶部可切换 **今天 / 近 7 天 / 全部**，面板每 2 秒自动刷新。

## 数据存储在哪

统计保存在**可执行文件同目录**的 `keyboard_stats.db`（SQLite 单文件，按"键 × 日期 × 小时"聚合）。
这是刻意的绿色软件设计：整个文件夹拷到哪里，配置和数据就跟到哪里，备份 = 复制这一个文件。
程序对它只做追加统计，卸载时删掉可执行文件和这个 db 文件即可完全清除痕迹。

## 命令行参数

```
KeyboardFrequencyMonitor [-port 8321] [-reset] [-export f.csv] [-no-tray] [-open-panel=false]
```

| 参数 | 说明 |
| --- | --- |
| `-port N` | 面板端口（默认 8321，被占用自动向后尝试） |
| `-reset` | 启动前清空所有统计数据 |
| `-export f.csv` | 导出全部数据为 CSV 后退出 |
| `-open-panel=false` | 启动时不自动打开浏览器 |
| `-no-tray` | 不显示托盘图标（调试用） |

重复启动第二个实例不会重复计数，而是直接打开已有实例的面板。

## 已知限制

- **按物理键统计**：Shift+A 计入 `a`，`!` 计入 `1`——反映的是"哪个键敲得多"，
  无法还原你实际输入的字符；中文输入法期间记录的是拼音字母键
- **粒度为小时**：数据按"键 × 日期 × 小时"聚合，可以看趋势和分布，
  但不能回放某一次按键的精确时刻（这正是"只计数不记录内容"的代价）
- **未签名**：Windows 会触发 SmartScreen / 杀软提示，macOS 需手动放行（见上）；
  个别游戏的反作弊系统也可能拦截全局钩子
- **Windows**：以管理员身份运行的窗口里敲的键不会被统计到（系统 UIPI 安全机制）
- **macOS**：必须授予「输入监控」权限；权限是按可执行文件路径记住的，
  移动或替换文件后需要重新授权
- **Linux 暂未支持**：Wayland 下没有可靠的全局监听方案
- **主题选择存在浏览器里**：换浏览器或清理站点数据后，主题会回到默认的月之暗面

## 从源码构建

需要 Go 1.22+。macOS 还需要 Xcode Command Line Tools（`xcode-select --install`），
因为事件监听与菜单栏图标走 cgo。

```bash
# macOS（当前平台）
go build -trimpath -ldflags "-s -w" -o KeyboardFrequencyMonitor .

# Windows
go build -trimpath -ldflags "-s -w -H windowsgui" -o KeyboardFrequencyMonitor.exe .

go vet -unsafeptr=false ./...   # unsafeptr 对 Win32 钩子回调误报，故豁免
```

推送 `v*` 标签后，GitHub Actions 会同时构建 Windows 与 macOS 版本并发布（见 `.github/workflows/ci.yml`）。

## 项目结构

平台相关代码用 Go 的文件名后缀约定（`_windows.go` / `_darwin.go`）区分，无需手写构建标签。

```
├── main.go                 入口与命令行参数（跨平台）
├── recorder.go             事件缓冲与暂停开关（跨平台）
├── recorder_windows.go     Win32 低级键鼠钩子
├── recorder_darwin.go      CGEventTap + 输入监控权限检查
├── keymap_windows.go       Windows 虚拟键码 -> 键名
├── keymap_darwin.go        macOS 键码 -> 键名（+ 修饰键按下/松开去重）
├── store.go                SQLite 存储（每秒批量事务写入）
├── stats.go                统计聚合与 API 载荷
├── server.go               内嵌面板的本地 HTTP 服务
├── tray.go                 托盘/菜单栏菜单（跨平台）
├── icon_windows.go         内嵌 ICO 图标
├── icon_darwin.go          内嵌 PNG 图标（NSImage 不吃 ICO）
├── browser_windows.go      打开默认浏览器（rundll32）
├── browser_darwin.go       打开默认浏览器（open）
├── dashboard.html          网页面板（编译期内嵌）
└── assets/                 托盘图标（tools/genicon 可重新生成两种格式）
```

两个平台的 `vkLabel` **必须输出相同的键名词表**，否则面板热力图认不出键位。

## License

[MIT](LICENSE)
