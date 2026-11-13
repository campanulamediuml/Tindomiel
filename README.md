# Tindomiel

前后端分离的图形化 SSH 客户端（个人项目，macOS 优先）。Tauri/WebView 提供桌面窗口，独立 Go 进程负责 SSH 与 SFTP。

## 目标

- 终端体验优先：TUI 程序（vim / htop / top）完整兼容
- 四区工作台：资源监控、Shell、远程目录树、当前目录文件常驻显示
- SFTP 文件管理：绝对路径、文件上传下载、文件夹递归下载、从 Finder 拖入上传（不做拖出）
- 缓存编辑：本地选择应用打开，VSCode 等保存后自动回传原路径
- 连接管理：文件夹分组、自定义名称、密码和多私钥认证
- 低资源占用：复用系统 WebView，不额外打包 Chromium
- 配置零迁移：直接读取 `~/.ssh/config`、`~/.ssh/known_hosts` 与现有密钥
- 多标签独立会话：每个标签页对应一条独立的 SSH 会话
- 断线保留画面：远程 SSH 断开或重连时，不清空当前标签页已经渲染的终端内容

## 当前交互与修复行为

- 自定义连接删除前使用应用内确认弹窗，确认后永久移除；OpenSSH 导入连接只会从应用隐藏，不修改 `~/.ssh/config`。
- 关闭标签立即释放前端终端，再异步清理后端会话；每个标签使用独立 SSH TCP 长连接和 shell，后端每 30 秒发送 SSH keepalive。
- 未知主机指纹使用应用内弹窗确认；指纹变化仍拒绝连接。下拉框统一使用应用内暗色组件。
- Cmd+C 复制终端选区，Cmd+V 直接使用原生 `paste` 事件中的剪贴板数据并通过 bracketed paste 粘贴，阻止 WebView 弹出系统 Paste 菜单，不请求 `navigator.clipboard.readText` 权限；普通输入框保持默认粘贴行为。
- 资源信息每 1 秒刷新；前后端日志可从右上角“日志”查看，保留策略见下文。

## 非目标

- 团队协作、权限体系
- 移动端
- 对标商业软件的全功能
- tmux / screen 集成
- 恢复远程 shell、进程或工作现场
- 应用刷新或重启后的终端画面恢复

## 使用手册

### 1. 启动应用

开发或构建完成后，打开 `Tindomiel.app`。首次连接局域网主机时，如果 macOS 请求“本地网络”权限，请点击允许。

主窗口左侧为贯穿整个高度的目标设备资源栏；右侧上方是 Shell，下方是常驻文件区，文件区内部再分为远程目录树和当前目录文件两栏。顶部是连接入口、下载目录、日志、传输中心和会话状态。

### 2. 创建和管理连接

点击左上角文件夹图标，可以新建 SSH 连接、新建文件夹或打开连接管理器。

新建连接时填写：

- **名称**：显示在连接列表和标签页上的自定义名称。
- **主机 / 端口**：远程 SSH 地址和端口，默认端口为 22。
- **用户名**：远程登录用户。
- **认证方法**：公钥或密码。公钥模式会自动载入默认私钥，可添加多个私钥并按顺序尝试，也可使用 SSH Agent；私钥短语按实际情况填写。
- **文件夹 / 备注**：用于分组和补充说明。

连接管理器固定显示在滚动列表中，支持按名称、IP 或用户名搜索。新建和编辑统一使用“编辑连接”页面，新连接的主机、用户名等信息留空。自定义连接可右键编辑或删除；文件夹右键可在该分组下添加连接。删除自定义连接会永久移除；删除 OpenSSH 导入连接只会从本应用隐藏，不会修改 `~/.ssh/config`。

通过 macOS 的 **Tindomiel → 设置…**、`Cmd+,` 或左侧资源栏底部的小齿轮打开应用设置，可调整终端配色、字号、光标闪烁、回滚行数、全局快捷键映射和下载目录。齿轮右侧每秒显示 Tindomiel 全应用的 CPU 和内存合计，包含主进程、Go 后端及 WebKit 辅助进程。终端内建 Homebrew（黑绿，默认）、macOS ClearDark 和原自定义配色 Boring 三套主题。设置保存在本机 `preferences.json`，保存后立即应用终端显示设置；减少回滚行数可降低内存占用，关闭光标闪烁可减少空闲绘制。

全局快捷键在**新 Session 创建时复制**，不会实时继承到已有 Session。标签右键“会话设置”可查看、添加或删除当前 Session 的映射，不影响其他 Session 或全局配置；点击“同步全局配置”并保存，才会用当前全局映射覆盖该 Session。组合 A 在终端中捕获，组合 B 转换为实际终端字节发送给 Shell。重连沿用同一 Session 的独立映射，复制标签页作为新 Session 复制当前全局映射。

首次连接未知主机时会显示密钥类型和 SHA256 指纹，请通过可靠渠道核对后再信任。指纹与已有记录不一致时会拒绝连接，不能一键绕过。

> 密码和私钥短语目前保存在本机配置文件中，权限为 0600，尚未接入 macOS 钥匙串。不要把 `profiles.json`、私钥或测试配置提交到公共仓库。

### 3. 使用终端

- 点击连接后立即显示连接中标签，连接信息和失败原因直接显示在窗口内；失败标签保留，可右键“重连”。已断开的标签显示红点，也可通过右键菜单重连，恢复后变为绿点。
- 每个标签页都有独立的 SSH TCP 长连接和远程 shell，同一主机开多个标签也不会共享 shell。右键“复制标签页”会使用相同连接信息创建新 Session，并插入原标签右侧。隐藏标签的 SSH/WS 流不会暂停，输出继续写入 xterm 缓冲区，切回时自动滚动到底部；scrollback 默认上限为 10000 行，可在设置中调整。
- 左上角菜单的“本机 Shell”通过 `127.0.0.1:22` 创建本机 SSH 标签，自动使用当前 macOS 用户、默认私钥和 SSH Agent，复用 SFTP、资源监控及重连。需要在“系统设置 → 通用 → 共享 → 远程登录”中开启 SSH 服务，首次连接仍需确认主机指纹。
- WebView 原生右键 Reload 已禁用，避免误刷新丢失当前终端画面；应用自己的文件、连接和标签右键菜单仍可使用。
- `Cmd+C` 复制选区，`Cmd+V` 在捕获阶段阻止原生 Paste 菜单并使用 bracketed paste 粘贴；终端中的 `Ctrl+C` 仍发送给远程程序。
- 后端每 30 秒发送 SSH keepalive。远程断线时保留当前终端已经渲染的内容，重连后新 shell 的输出继续追加。
- 关闭标签会关闭对应远程会话。应用不提供 tmux/screen 集成，不恢复远程进程、vim 状态或其他工作现场；刷新页面或重启应用也不恢复终端画面。

### 4. 浏览、上传和下载文件

下方 SFTP 区域始终打开。左侧目录树从 `/` 按需展开，右侧显示当前目录。

- 在路径栏输入绝对路径并按回车跳转；“复制”复制当前远程路径。
- 每个会话分别缓存目录树、当前路径和文件列表，切换标签直接恢复；只有手动刷新、进入目录、路径联动或上传完成后才重新读取。
- 点击“文件名”“最近修改”或“创建时间”可切换升降序，初始为目录优先、名称升序；表头依次为文件名、大小、最近修改、创建时间、权限，浅灰分隔线可拖动调整列宽，悬停时提亮。创建时间由远程系统批量查询，不支持的文件系统或链接显示 `—`。Windows SFTP 路径使用 `/C:/...` 协议格式，PowerShell 查询时自动转换为 `C:/...`。
- 双击目录进入；双击文件进入本地缓存编辑。
- 点击“上传”选择文件，或把文件从 Finder 拖入当前文件列表。暂不支持把远程文件拖出到 Finder。
- 选中一个或多个文件/文件夹后点击“下载”。普通文件和递归文件夹都保存到所选本机目录。
- 文件夹下载保留目录结构、空目录和普通文件权限；符号链接和非常规文件跳过，不跟随链接目录。
- 同名文件在扩展名前添加 `-1`、`-2`，同名文件夹在名称后添加序号，不覆盖已有内容。
- 传输中心显示总进度和当前正在传输的远程文件路径，可清除已完成记录。

点击顶部“更改”可选择新的下载目录，选择结果会持久化，重启应用后仍有效。点击下载地址本身会在 Finder 中打开该目录。

### 5. 在本地编辑远程文件

双击远程普通文件后，文件会下载到会话私有缓存，并由默认应用打开。右键文件选择其他应用后，该应用会成为后续默认打开方式。

使用 VSCode 等编辑器修改并保存时，应用会自动把文件回传到原远程路径，支持普通保存和原子改名保存。成功或失败都会显示通知。请在会话仍连接时保存；关闭会话会停止缓存监听，远程断线期间的保存可能失败。

缓存编辑与普通下载不同：缓存文件用于自动回传，普通下载文件用于本地留存，不会自动回传。

### 6. 资源监控和路径联动

- 每个会话在后台持续刷新 CPU、内存、交换、进程、网络、延迟和磁盘，前端保存最近快照；切换标签直接展示对应机器的数据，不触发新的远程采集。
- Linux、macOS 和 Windows 均支持资源监控；macOS 使用系统自带命令采集，无需安装远端依赖。
- Windows 同时兼容 `cmd.exe` 和 PowerShell 默认 Shell；PowerShell 不可用时自动使用 `systeminfo` + `typeperf` 原生采集，远端禁止 exec 时也能通过独立 Shell 获取数据。交换空间按分页文件统计。
- 点击进程表的 CPU 或内存标题可切换升序/降序。
- 网络默认选择并保持活动网卡，也可手动切换；中间横线以上红色表示上传，以下绿色表示下载。每根柱按时间戳表示一秒，缺失秒补 0，上下行共用最近 40 秒的滚动刻度；图下显示起止时刻，悬停可查看单柱时间和速率。
- 磁盘显示挂载路径、已用/总大小和比例条。
- 点击“系统信息”查看主机名、操作系统、内核、架构、CPU 型号和逻辑核数。
- Bash/Zsh 中执行 `cd` 后，SFTP 自动跳转到对应目录；Windows `cmd.exe` 暂不支持该联动。

### 7. 下载目录、配置和缓存位置

桌面版默认使用：

| 内容 | 位置 |
|---|---|
| 自定义连接 | `~/Library/Application Support/Tindomiel/profiles.json` |
| 应用设置、下载目录、默认打开应用、隐藏的 OpenSSH 连接 | `~/Library/Application Support/Tindomiel/preferences.json` |
| 缓存编辑文件 | `~/Library/Caches/Tindomiel/` |
| 普通下载 | 初始为 `~/Downloads`，可在右上角更改 |
| 后端日志 | `~/Library/Application Support/Tindomiel/logs/tindomiel.log` |
| 界面日志 | `~/Library/Application Support/Tindomiel/logs/frontend.log` |

### 8. 日志与故障排查

点击右上角“日志”可分别查看后端日志和界面日志。两者分别写入独立文件，单个日志文件达到 4 MiB 后轮转，备份均保留 3 天；查看界面展示当前文件末尾，历史备份可在日志目录中查看。日志不记录 token、密码、私钥内容或私钥短语。

常见问题：

- **`connect: no route to host`**：浏览器开发模式能连接而桌面版不能时，优先检查 macOS“系统设置 → 隐私与安全性 → 本地网络”中的 Tindomiel 权限，以及发布包的嵌套 sidecar 签名。
- **主机指纹变化**：确认远程主机是否重装或更换密钥，不要直接删除旧记录绕过警告。
- **远端未启用或无法提供 SFTP**：远端拒绝了 SFTP subsystem，请检查 sshd 的 `Subsystem sftp ...` 配置；应用会显示明确状态，原始错误保留在后端日志中。
- **SFTP 没有跟随 `cd`**：确认使用 Bash/Zsh；Windows `cmd.exe` 暂不支持。
- **编辑器保存没有回传**：确认原标签仍连接，并查看传输通知和日志中的 `save-error`。

## 技术栈

| 层 | 选型 |
|---|---|
| 前端 | TypeScript + Vite + xterm.js |
| 桌面壳 | Tauri / 系统 WebView |
| 后端 | Go + `golang.org/x/crypto/ssh` + `github.com/pkg/sftp` |
| 通信 | WebSocket（终端 I/O）+ REST（连接管理 / SFTP） |
| 部署形态 | Tauri 管理本地 Go sidecar，仅监听 `127.0.0.1` |

## 文档

- [架构设计](docs/architecture.md)
- [接口设计](docs/api.md)
- [UI 设计规范](docs/ui.md)
- [路线图](docs/roadmap.md)
- [测试与评审用例](docs/test-cases.md)

## 目录结构

```
Tindomiel/
├── docs/           # 设计文档
├── shell/          # Tauri 壳与 Go sidecar 生命周期管理
├── server/         # Go 后端：SSH 会话管理 + WS/REST 服务
├── web/            # 前端：xterm.js 终端 + 连接管理 + SFTP 面板
└── README.md
```

## 快速开始

环境要求：macOS、Go 1.26+、Node.js/npm、Rust 1.85+（rustup）和 Xcode 命令行工具。Rust 可通过 <https://rustup.rs> 安装；如果 Go、Node 或 Rust 不在默认 PATH，请先配置对应路径。

克隆后安装依赖：

```bash
npm ci
npm --prefix web ci
```

浏览器开发模式（两个终端分别执行）：

```bash
TINDOMIEL_ADDR=127.0.0.1:18080 TINDOMIEL_TOKEN=dev-token npm run dev:server
```

```bash
VITE_BACKEND_URL=http://127.0.0.1:18080 VITE_BACKEND_TOKEN=dev-token npm run dev:web
```

打开 `http://127.0.0.1:1420`。

Tauri 桌面开发模式需要 Rust。当前项目的本地 Rust 工具链可按下面方式使用：

```bash
export CARGO_HOME="$PWD/.tools/cargo"
export RUSTUP_HOME="$PWD/.tools/rustup"
export PATH="$CARGO_HOME/bin:$HOME/go/bin:/opt/homebrew/bin:$PATH"
npm run tauri:dev
```

生产构建：

```bash
npm run tauri:build
```

构建产物：`shell/target/release/bundle/macos/Tindomiel.app`。

## GitHub Actions 构建产物

`.github/workflows/build.yml` 会在推送到 `main`、创建 `v*` 标签、Pull Request 或手动触发时，使用 GitHub 的 `macos-15` ARM64 runner 构建应用。工作流依次安装 Go 1.26、Node.js 26 和 Rust，执行 `npm ci`、前端依赖安装及 `npm run tauri:build`，最后生成：

- `Tindomiel-macos-arm64.zip`：包含完整 `Tindomiel.app`，使用 `ditto` 保留 macOS 资源元数据。
- `Tindomiel-macos-arm64.zip.sha256`：ZIP 的 SHA256 校验值。

构建完成后，在仓库 **Actions → 对应运行记录 → Artifacts → Tindomiel-macos-arm64** 下载，Artifact 保留 14 天。推送 `v*` 标签时还会自动创建同名 **GitHub Release**，将 ZIP 和 SHA256 作为正式版本附件上传；手动构建可在 Actions 的 `build` 工作流中点击 **Run workflow**。

当前流水线沿用项目的 `codesign --force --deep --sign -` ad-hoc 签名，不需要配置证书 secrets，可用于本机及内部测试；它不是 Developer ID 签名和 Apple 公证，其他 Mac 首次打开时可能出现 Gatekeeper 提示。正式公开分发还需配置 Developer ID 证书、专用 App Store Connect API Key、公证流程及相应仓库 secrets。

## 日志

前后端使用 JSON Lines 结构化日志。后端日志同时写入标准错误和文件，默认路径：

- 浏览器开发环境：`.tools/logs/tindomiel.log` 与 `.tools/logs/frontend.log`
- 桌面应用：`~/Library/Application Support/Tindomiel/logs/tindomiel.log` 与 `~/Library/Application Support/Tindomiel/logs/frontend.log`

可通过 `TINDOMIEL_LOG`、`TINDOMIEL_FRONTEND_LOG` 分别指定路径，通过 `TINDOMIEL_LOG_LEVEL=debug|info|warn|error` 调整级别。两个日志均按单文件 4 MiB 轮转，超过 3 天的备份自动清理，可在右上角“日志”中分别查看当前文件末尾。

日志记录连接地址、认证方式、连接成功/失败、会话状态及重连过程，不记录 token、密码、私钥内容或私钥短语。典型事件包括 `ssh_dial_start`、`ssh_dial_failed`、`ssh_dial_success`、`session_ready`、`session_disconnected` 和 `session_reconnect_failed`。

## macOS 本地网络权限

打包应用的 `Info.plist` 包含 `NSLocalNetworkUsageDescription`。首次连接局域网 SSH 主机时，如果 macOS 弹出本地网络权限请求，需要允许。若此前已经拒绝，请在“系统设置 → 隐私与安全性 → 本地网络”中启用 Tindomiel。

`npm run tauri:build` 还会对整个 `.app` 及嵌套 Go sidecar 执行 `codesign --force --deep --sign -`，确保 macOS 能正确归属网络权限。不要只给主可执行文件签名而漏掉 sidecar。

当浏览器开发模式可以连接而桌面 App 报 `connect: no route to host` 时，优先检查此权限、嵌套二进制签名及桌面日志中的 `ssh_dial_failed`。
