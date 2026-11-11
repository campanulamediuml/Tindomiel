# 测试与评审用例

本文用于功能回归和独立 agent 评审。测试只读取目录、执行无破坏性 Shell 命令，不修改远端 sshd、SSH 密钥或用户连接配置。

## 自动检查

在仓库根目录执行：

```bash
cd server && go test ./... && go vet ./... && go build ./...
cd .. && npm run build:web
npm run tauri:build
git diff --check
```

`server/internal/session/monitoring_test.go` 当前覆盖：

| 测试 | 验证点 |
|---|---|
| `TestSplitSectionsAcceptsCmdMarkerWhitespace` | `cmd.exe` 的分段标记允许尾随空格 |
| `TestNumericValuesReadsNullSeparatedBirthTimes` | macOS/Linux 创建时间的 NUL 分隔协议 |
| `TestParseDarwinUptimeAndLoad` | macOS 启动时间与三项负载 |
| `TestParseDarwinMemoryAndSwap` | macOS 页大小、可用内存和交换空间单位 |
| `TestParseDarwinNetworkAndDisks` | macOS 网卡字节数与 `df` 容量换算 |
| `TestParseWindowsCounters` | Windows 无实例计数器、进程和带括号的网卡名称 |
| `TestParseCmdSystem` | `systeminfo` CSV、CPU、内存和虚拟内存 |
| `TestParseCmdDisksFiltersSystemVolumes` | `cmd.exe` 只显示盘符并正确计算已用空间 |
| `TestLocalProfileUsesLoopbackAndCurrentUser` | 本机 Shell 使用当前用户和 `127.0.0.1:22` |

## 实测环境

- **本机 Shell**：macOS 内置菜单，使用当前用户和 `127.0.0.1:22`。需开启“系统设置 → 通用 → 共享 → 远程登录”。
- **远程进入wsl**：Linux/WSL 文件与终端测试。
- **vpn**：另一台 Linux 主机的文件与终端测试。
- **windows / socat-248**：Windows `cmd.exe` 监控和 SFTP；PowerShell 默认 Shell 按需另行切换验证。

## 功能验收

### LOCAL-01：本机 Shell 全链路

1. 从左上角菜单点击“本机 Shell”。
2. 若出现主机指纹弹窗，核对后接受。
3. 执行 `whoami`、`pwd`、`uname -s`。
4. 查看资源面板和文件面板。

预期：

- 标签名称为“本机 Shell”，命令在本地 macOS 执行。
- CPU、内存、交换、进程、网卡和磁盘均有数据。
- SFTP 与资源监控复用普通 SSH 会话，不启动额外本地 PTY。
- 若系统未开启“远程登录”，标签内显示连接失败，不卡死、不创建本机 PTY 回退。

### FILE-MAC-01：macOS 文件列表与创建时间

1. 在本机 Shell 或远程 macOS 会话中进入用户主目录。
2. 同时观察左侧目录树和右侧文件列表。
3. 刷新根目录 `/` 和主目录。

预期：

- 100 项以内的目录在本地网络下应低于 2 秒，不出现固定约 15 秒等待。
- 左右两侧最终都离开“读取中”，不存在右侧完成而左侧长期等待。
- 支持出生时间的 APFS 文件显示创建时间；不支持时显示 `—`，不影响列表。

本次基线：本机主目录 41 项约 0.05 秒，41 项均有创建时间；根目录 22 项约 0.04 秒。

### FILE-WSL-01：WSL 文件列表与创建时间

1. 打开“远程进入wsl”。
2. 进入 `/home/campanula` 并展开左侧目录树。
3. 刷新文件列表并检查创建时间列。

预期：目录在公网/内网正常条件下低于 3 秒，左右同步完成，创建时间按文件系统能力显示。

本次基线：42 项约 1.48 秒，42 项均有创建时间。

### FILE-LINUX-01：VPN Linux 文件列表

1. 打开 `vpn`，进入 `/home/ubuntu` 和 `/`。
2. 刷新并检查列表、修改时间和创建时间。

预期：目录低于 3 秒，左右同步完成。若文件系统不支持出生时间，创建时间允许显示 `—`，不能因此失败或等待 15 秒。

本次基线：主目录 21 项约 0.79 秒，出生时间返回 0 并显示 `—`。

### WIN-CMD-01：Windows 默认 `cmd.exe`

1. 确认远端默认 Shell 为 `cmd.exe`。
2. 新建 Windows 会话并查看资源面板。
3. 连续观察至少三轮刷新。
4. 若远端启用 SFTP，打开 `/C:/Users/<用户>`。

预期：

- 即使远端拒绝启动 PowerShell，也能通过 `systeminfo` + `typeperf` 返回资源数据。
- CPU、内存、交换、进程和网卡非空；磁盘只显示 `C:`、`D:` 等盘符。
- 进程名不出现 `PS>` 或连续乱码。
- SFTP 可用时正常显示文件列表。

### WIN-PS-01：Windows 默认 PowerShell

1. 将远端默认 Shell 设为 PowerShell 后重新连接。
2. 查看至少三轮资源快照。

预期：PowerShell/CIM 采集正常；进程、网卡、磁盘完整，不出现 ConPTY 长行乱码。若远端拒绝 SFTP subsystem，文件区域显示“远端未启用或无法提供 SFTP”，而不是普通目录读取失败。

### PASTE-01：终端 Cmd+V

1. 复制文本 `echo TINDOMIEL_PASTE_OK`。
2. 在终端中按 `Cmd+V`，再按 Enter。
3. 在普通路径输入框中再执行一次粘贴。

预期：

- 终端直接粘贴命令，执行后输出一次 `TINDOMIEL_PASTE_OK`。
- 不出现 WebView 原生 Paste 菜单，不请求 `navigator.clipboard.readText` 权限。
- 普通输入框保持系统默认粘贴行为。

### TAB-01：多标签后台状态

1. 同时打开本机 Shell、WSL 和 VPN。
2. 在 A 标签持续输出，在 B 标签停留后切回 A。
3. 多次切换资源面板和文件面板。

预期：终端输出不丢失，切回后位于底部；资源使用对应会话的缓存，文件列表不因切换自动重新加载，目录树与文件列表状态一致。

## 评审注意事项

- 不要为了测试去启用、关闭或改写远端 SFTP subsystem；已有故障只验证应用的提示行为。
- 不要读取、复制或提交私钥、密码、`profiles.json` 或 token。
- 创建时间返回 0 是文件系统能力差异，应与加载失败区分。
- 若网络本身不可用，应先记录连接错误，不要把网络超时误判为 SFTP 解析或渲染问题。
