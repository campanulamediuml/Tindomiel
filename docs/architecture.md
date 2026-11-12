# 架构设计

## 总体架构

```
Tauri / 系统 WebView（TypeScript + xterm.js）
      │
      ├─ WebSocket  /ws/terminal    终端 I/O（二进制帧 + JSON 控制帧）
      └─ REST       /api/*          连接管理、SFTP、健康检查
      ▼
本地 Go sidecar（仅监听 127.0.0.1，随机端口 + token 鉴权）
      ├─ SessionManager   多标签会话生命周期、断线检测与重连
      ├─ SSHConnection    PTY、window-change、keepalive、主机密钥校验
      ├─ SftpService      目录列表、流式上传下载
      └─ ConfigStore      OpenSSH config、known_hosts、自定义 profiles
      ▼
远程 SSH 服务器
```

Tauri 负责启动、停止 Go sidecar，并通过安全的进程通道传递访问 token。macOS 应用菜单包含“设置…”和 `Cmd+,`，通过 Tauri 事件打开前端设置页。前端只加载本地静态资源，不直接持有私钥，也不直接发起 SSH 连接。

原生 `app_metrics` 模块通过 PID 责任归属汇总主进程、Go sidecar 和 WebKit 辅助进程；普通 PPID 无法关联 launchd 名下的 WebKit 进程，因此使用 macOS responsibility 接口确认归属，再读取 `proc_pid_rusage`。前端每秒调用一次 Tauri 命令，窗口隐藏时也持续采样，耗时较长时通过 pending 标记避免请求重叠。CPU 按相邻采样的累计 CPU 时间差计算，先通过 `mach_timebase_info` 将 `proc_pid_rusage` 返回的 Mach absolute time 换算为纳秒，避免在不同 CPU 架构上误用时间单位；内存汇总 physical footprint。

应用设置通过 `GET/PUT /api/settings` 持久化到 `preferences.json`；终端主题、字号、光标闪烁和回滚行数保存后立即更新所有 xterm 实例，下载目录沿用同一偏好存储。主题定义集中在前端 `terminal-themes.ts`，包含默认 Homebrew、macOS ClearDark 和原自定义 Boring；主题切换只更新渲染色板，不改变终端数据流。全局快捷键采用复制语义：每个 TerminalView 创建时从全局数组建立自己的 Map，后续全局保存不修改该 Map；Session 设置只更新当前 TerminalView，“同步全局配置”是显式覆盖当前 Session 编辑内容的操作。Session 快捷键只存于内存，重连保留，关闭标签释放。

## 模块职责

### 前端（web/）

- **TerminalView**：xterm.js 渲染、键盘输入、创建时复制且可独立修改的 Session 快捷键映射、选择复制、bracketed paste、resize 上报
- **TabManager**：每个标签页持有一个独立 `sessionId` 和 xterm 实例；切换标签只隐藏视图，不销毁终端
- **ProfileList**：连接列表展示（含 OpenSSH config 导入的分组）
- **SftpPanel**：远程目录树、拖拽上传、下载、进度条
- **HostKeyDialog**：展示未知主机指纹并要求用户明确确认；指纹变化只展示错误，不提供一键绕过

### 后端（server/）

- **ConfigStore**
  - 解析 `~/.ssh/config` 的 Host 列表；需要生效配置时调用本机 `ssh -G <别名>`，避免自行覆盖不全 `Include`、Host 模式等规则
  - 首版只消费明确支持的 HostName / User / Port / IdentityFile 等字段，不假装支持全部 OpenSSH 指令
  - 导入的 profile 不可编辑，但可从应用列表隐藏；隐藏记录存于 `preferences.json`，绝不修改用户的 OpenSSH 配置
  - 手动创建的 profile 存 `profiles.json`，编辑与永久删除均采用原子写入；默认路径为 `server/data/profiles.json`，可用 `TINDOMIEL_DATA` 覆盖
  - 优先使用 SSH Agent 和现有私钥，私钥不进入前端
- **SessionManager**
  - 一个后端进程托管多个相互独立的 SSH 会话；同一主机的多个标签页也不共享 shell
  - 内置 `local:shell` 使用当前 macOS 用户和默认密钥连接 `127.0.0.1:22`，不保存额外 profile，完整复用 SSH 终端、SFTP、资源监控和重连；macOS 未开启“远程登录”时在标签内显示连接错误
  - 每个标签页对应独立 SSH TCP 长连接、远程 shell、SFTP channel 和转发 goroutine
  - 逻辑会话状态：`connecting → ready → disconnected → reconnecting → ready`，显式关闭后进入 `closed`
  - 远程断线后的自动或手动重连沿用原 `sessionId`，但建立的是新的 SSH 连接和远程 shell
  - 每 30 秒发送一次 `keepalive@openssh.com` 检测远程连接失效；本地 WebSocket 不使用复杂心跳和重试机制
  - 点击连接时前端先创建本地 pending 标签并显示连接中状态，后端创建成功后改用真实 `sessionId`；pending 标签不请求监控或 SFTP，提前关闭会在连接完成后清理后端会话，首次失败可右键重新发起
  - 关闭标签时前端立即释放 UI 和 xterm，再异步调用后端删除；xterm 销毁异常被单独记录，DOM 在 `finally` 中强制移除，不阻断标签关闭和后端清理
- **SSHConnection**
  - 默认使用 `~/.ssh/known_hosts` 校验主机密钥
  - 未知主机必须经用户在应用内弹窗确认指纹后方可保存并连接；指纹变化必须拒绝连接
  - 公钥认证优先（SSH Agent / privateKeys），密码认证作为后续回退
  - PTY 尺寸同步（window-change）
  - Bash/Zsh 启动时注入 OSC 7 当前路径通知；Bash 的 `PROMPT_COMMAND` 使用换行拼接，兼容原值以分号或分号加空格结尾的配置
  - 注入期间暂时关闭 PTY ECHO，完成后用 `stty echo` 恢复，初始化脚本及续行提示不回显到终端
- **SftpService**
  - 同一标签页的 SFTP 与终端共用同一条 SSH 连接，使用独立 channel
  - 上传下载均采用流式 I/O 和背压处理，不将整个文件载入内存
  - 普通文件和文件夹统一直接写入用户选择的下载目录，偏好存于 `preferences.json`；同名文件在扩展名前加序号，不覆盖已有内容
  - 文件夹通过 SFTP 递归下载，不另起 `scp` 进程；保留目录结构和普通文件权限，跳过符号链接，同名本地目录自动加序号
  - 文件夹任务聚合总字节数和进度，通过 `currentPath` 实时报告当前远程文件
  - 通过 RealPath 将当前目录规范化为绝对路径；目录树从 `/` 按需读取
  - 前端以 `Map<sessionId, TreeSnapshot>` 隔离目录树节点、用户展开状态和当前路径，并按会话缓存右侧当前目录及加载状态；首次只读取根目录和右侧当前目录，点击展开才读取子目录，未加载节点灰显。路径联动只更新当前路径，不自动展开或读取祖先目录；目录树刷新仅重新读取根目录和用户已展开节点，右侧刷新仅读取当前目录
  - 目录树和右侧列表请求均按原 `sessionId` 写回缓存，切换到其他标签不取消、不丢弃结果，也不渲染到当前会话；切回时恢复已完成结果或“读取中”状态，同一会话仅接受最新目录请求，关闭会话后忽略迟到响应。手动刷新、进入目录、路径联动或上传完成后才重新读取右侧列表，普通标签切换不重新请求
  - 文件列表初始目录优先、名称升序，支持名称、mtime、创建时间点击排序及列宽拖拽；Linux 通过 GNU `stat %W` 批量获取出生时间，macOS 通过 BSD `stat -f %B` 和 NUL 分隔批量获取，Windows 通过 PowerShell `CreationTimeUtc` 逐项容错获取；SFTP 的 `/C:/...` 规范路径在查询前转换为 PowerShell 使用的 `C:/...`，未知值返回 0 并显示 `—`
  - 缓存编辑将文件放入用户缓存目录下的会话私有目录（0700 / 0600），调用 macOS `open` 打开
  - 监听缓存目录而非单个文件 inode，兼容 VSCode 原子保存；事件去抖后自动上传原远程路径
  - 会话关闭时停止文件监听；远程断线时保留监听，保存失败通过 WS 通知前端
  - SSH 重连后重建 SFTP channel；中断的传输不伪装成已恢复或已续传
- **ResourceMonitor**
  - 通过独立 SSH channel 采集 CPU、内存、交换、进程、网卡、磁盘、运行时间和负载
  - 通过 `uname -s` 区分 Linux、macOS（Darwin）和 Windows；Linux 读取 `/proc`、`df` 和 `ps`，macOS 使用 `top`、`vm_stat`、`vm.swapusage`、`netstat`、`df` 和 `ps`，Windows 使用 PowerShell/CIM，交换空间读取 `Win32_PageFileUsage` 的实际分页文件总量和已用量
  - Windows 优先通过 UTF-16LE Base64 的 `powershell -NoProfile -NonInteractive -EncodedCommand` 执行 CIM 采集；服务端拒绝 exec 时，使用独立的交互 Shell channel 回退，PTY 类型为 `dumb`
  - 默认 `cmd.exe` 禁止启动 PowerShell时，自动改用 Windows 自带的 `systeminfo` 和带引号的 `typeperf` 英文计数器采集，不再依赖嵌套 PowerShell；采集器选择按会话缓存，避免每次监控重复探测
  - Windows 回退结果使用 Base64 短行分块，每块带序号和总数，后端校验缺块、重块、顺序和 UTF-8 后解码，避免 ConPTY 改写长行；解码后的监控数据采用 `SYS|...`、`PROC|...`、`NET|...`、`DISK|...` 等键值文本协议，不依赖终端中的 JSON 或提示符位置
  - CPU 使用采样差值，本机 ping 目标 IP 获取延迟；网卡由独立采样链路按一秒时间槽推送。Linux/macOS 每秒读取累计字节数，900ms 超时或失败的槽为 0，相邻成功样本的差值除以跨过的整数秒数，失败不推进基线也不回填失败槽；Windows 通过持续运行的 `typeperf` 通道直接读取系统每秒速率，连续样本按序号分配逐秒时间槽，避免每次启动进程的开销和到达抖动，启动或断流期间缺秒补 0
  - 每个逻辑会话由 Manager 启动普通资源和网络两个后台监控 goroutine，持续采集并更新该 Session 的互斥快照缓存，关闭会话时停止并等待 goroutine 退出；首次采样按 Session ID 在 1 秒窗口内确定性错峰，关闭可立即打断等待
  - 主机名、系统版本、内核、架构、CPU 型号和核心数按 SSH generation 缓存，重连后重新采集；信息不完整或采集失败不写入缓存，Linux、macOS、PowerShell 和 CMD 均保留动态数据及原有回退路径
  - 普通资源在一轮完成后等待 1 秒，失败退避为 3 秒；Windows 实际间隔还包含 PowerShell/CIM 采集耗时，不影响独立网络循环
  - 普通快照通过 WS `metrics` 控制帧推送，网络样本通过独立 `network` 帧推送；REST 只读取最近缓存并附带最近网络结果，初次缓存尚未建立时才同步采集
  - 网络样本携带本机 Unix 秒级业务时间戳，仅用于图表对齐和排序，不依赖远端时钟；前端按 `sessionId` 保存最近快照、各网卡最近 40 秒连续时间轴、ping 历史和网卡选择，同一秒覆盖、缺秒补 0、窗口内迟到样本按原时间回填，后台标签也按时间推进；隐藏标签只更新内存、不渲染 DOM，切换标签直接展示已有曲线，不触发远端采集
- **Logging**
  - 后端输出 JSON Lines 到标准错误和日志文件，单个文件达到 4 MiB 后轮转，启动时及运行期间每小时清理超过 3 天的备份
  - 前端将界面事件写入 localStorage，最多保留 300 条且只保留最近 3 天
  - 日志包含连接地址、认证方式、会话状态、错误和重连过程，不记录 token、密码、私钥内容或私钥短语

## 断线与终端画面

本项目明确区分本地 WebSocket 和远程 SSH 连接。

### 本地 WebSocket

`127.0.0.1` 上的 WebSocket 不经过物理网络，远程断网、Wi-Fi 切换不会导致它断开，因此不围绕它设计断线重连能力。

WebSocket 异常关闭不等于 SSH 会话关闭。前端重新加载后可以重新附着到仍存活的后端会话，但不保证恢复刷新前的 xterm 画面；后端进程退出后，其内存中的会话随进程销毁。

### 远程 SSH 断线

1. 后端检测到 SSH 连接失效，通过仍存活的本地 WebSocket 发送 `disconnected` 状态。
2. 前端保留当前标签页的 xterm 实例和全部现有渲染内容，不调用 `clear()`、`reset()`，也不销毁实例。
3. 断开期间禁用终端输入，不缓存按键，避免恢复后把密码等输入意外发送到新 shell。
4. 自动重连或用户手动重连时，状态变为 `reconnecting`；连接成功后变为 `ready`。
5. 新远程 shell 的输出继续追加到原 xterm 实例，之前已经渲染的内容仍然可见。

这里只保证**标签页未刷新、应用未重启时，断线和重连过程中保留已有终端画面**。不恢复远程 shell、进程、当前目录、vim 编辑状态或其他工作现场，也不引入 tmux / screen。

## 关键设计决策

1. **终端数据是字节流，不是文本**
   - WS 数据帧一律二进制，前端 `xterm.write(Uint8Array)`，禁止按行或按 JSON 文本处理
   - 避免 ANSI 转义序列与 UTF-8 多字节边界被破坏
2. **数据帧与控制帧分离**
   - 二进制帧 = 终端原始字节（双向）
   - JSON 文本帧 = 控制消息（resize、exit、error、status）
3. **安全边界**
   - 后端只监听 `127.0.0.1`，使用随机端口和随机 token
   - REST 使用 Bearer token；WebSocket 使用短期、一次性 ticket，并校验 `Origin`
   - 密钥仅存在于后端进程，不下发前端
   - 主机密钥校验属于连接安全边界，从 M1 开始实现，不能默认信任任意主机密钥
4. **复制与粘贴**
   - 前端支持 bracketed paste，多行粘贴不被逐行执行
   - macOS 的 Cmd+C 复制选区；Cmd+V 仅拦截 xterm 的重复快捷键处理，由原生 `paste` 事件提供剪贴板文本，显式阻止默认行为和传播后走 bracketed paste，避免 WebView 弹出系统 Paste 菜单及 `navigator.clipboard.readText` 权限请求
5. **应用内交互组件**
   - 删除确认、未知主机确认和下拉选择均使用应用内组件，不依赖桌面 WebView 中行为不一致的 `window.confirm()` 或原生 `<select>` 弹层
   - 删除 custom profile 会永久移除；删除 OpenSSH profile 仅将其加入隐藏列表
6. **SFTP 与终端复用连接**
   - 仅复用同一标签页所属的 SSH 连接，不跨标签页共享 shell 或会话状态

## 日志与 macOS 桌面权限

后端默认日志路径为 `.tools/logs/tindomiel.log`（浏览器开发）或 `~/Library/Application Support/Tindomiel/logs/tindomiel.log`（桌面应用），可通过 `TINDOMIEL_LOG` 覆盖。`TINDOMIEL_LOG_LEVEL=debug|info|warn|error` 控制日志级别。右上角“日志”通过 REST 读取当前后端日志末尾，并展示前端事件。

桌面 App 的 `Info.plist` 必须包含 `NSLocalNetworkUsageDescription`。发布构建对整个 `.app` 和嵌套 Go sidecar 执行 `codesign --force --deep --sign -`，让 macOS 正确归属本地网络权限。若浏览器可连接而桌面 App 报 `connect: no route to host`，先检查本地网络权限、嵌套 sidecar 签名和桌面日志中的 `ssh_dial_failed`，不要先假定目标主机不可达。

## 目录结构

```
shell/                     # Tauri 配置、窗口与 sidecar 生命周期
server/
├── cmd/tindomiel-server/  # Go 服务入口
├── internal/
│   ├── configstore/       # OpenSSH config、profiles、文件夹和应用偏好
│   ├── session/           # 会话、重连、监控、缓存编辑与路径通知
│   ├── sshconn/           # SSH、认证、PTY、主机密钥校验
│   ├── logging/           # JSON Lines 日志、轮转和三天清理
│   └── httpapi/           # HTTP + WS、鉴权、SFTP 与日志接口
├── data/profiles.json
└── go.mod

web/
├── src/
│   ├── terminal.ts        # xterm/WebGL、WS 绑定、复制粘贴和断线画面保留
│   ├── connection.ts      # 连接管理、右键菜单和应用内删除确认
│   ├── custom-select.ts   # 统一暗色下拉组件
│   ├── metrics.ts         # 每秒监控、排序和资源图表
│   ├── logs.ts            # 前后端日志查看与前端日志保留
│   ├── sftp.ts
│   └── main.ts            # 多标签与独立会话生命周期
└── package.json
```
