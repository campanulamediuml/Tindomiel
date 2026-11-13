# 接口设计

约定：

- 服务仅监听 `127.0.0.1`，所有 REST/WS 请求都需要鉴权并校验来源。
- REST 使用 `Authorization: Bearer <token>`。
- 前端先通过 REST 换取短期、一次性 WebSocket ticket，再建立 WS；不在 URL 中传递长期 token。
- 错误统一格式：`{ "error": { "code": "string", "message": "string", "details": {} } }`，其中 `details` 可选。

## REST

### 健康检查

```
GET /api/health
→ { "ok": true, "version": "0.1.0" }
```

### 连接配置（profiles）

```
GET /api/profiles
→ [{
    "id": "openssh:socat-248",
    "name": "socat-248",
    "source": "openssh" | "custom",
    "options": { "host": "192.168.31.248", "port": 22, "user": "socat",
                 "auth": "publicKey", "privateKeys": ["~/.ssh/id_ed25519"],
                 "shortcuts": [{ "from": "Ctrl+Shift+P", "to": "Ctrl+A" }] }
  }]

POST /api/profiles        # 新建 custom profile（body 同上单条结构）
PUT  /api/profiles/:id    # 编辑 custom；password/privateKeyPassphrase 留空保留原值
DELETE /api/profiles/:id  # custom 删除；OpenSSH 仅从应用隐藏，不改原配置
```

自定义 profile 包含 `name`、`remark`、`folder`，列表和标签始终使用用户自定义名称。`options.auth` 为 `publicKey` 或 `password`；公钥连接可包含多个 `privateKeys` 和 `privateKeyPassphrase`，密码连接包含 `password`。敏感字段只用于后端认证，GET 和创建响应都会清除，不会在接口中回传。配置文件以 0600 权限保存。

删除语义按 profile 来源区分：

- `custom:*`：从自定义配置中永久删除。
- `openssh:*`：把 ID 写入 `preferences.json` 的 `hiddenProfiles`，仅从应用列表隐藏；不修改 `~/.ssh/config` 或其他 OpenSSH 文件，暂不提供从界面恢复隐藏的入口。

删除前由前端显示应用内确认弹窗，不使用桌面 WebView 中不可靠的 `window.confirm()`。

```
GET  /api/folders
POST /api/folders          body: { "name": "家里的电脑" }
GET  /api/keys/defaults    # 自动载入默认私钥的名称、类型、长度和路径
POST /api/keys/info        body: { "path": "~/.ssh/id_ed25519" }
POST /api/local/choose-file
→ { "path": "/Users/me/.ssh/id_ed25519" }
GET  /api/local/download-path
→ { "path": "/Users/me/Downloads" }
POST /api/local/open-downloads  # 在 Finder 中打开同一下载目录
→ { "ok": true }
POST /api/local/choose-download-directory  # macOS 原生目录选择器
→ { "path": "/Users/me/chosen-directory" }
```

下载目录保存在 `preferences.json` 的 `downloadDirectory`，重启服务后仍有效。未选择时默认为 `~/Downloads`；`TINDOMIEL_DOWNLOADS` 可强制覆盖偏好，主要用于开发测试。取消选择返回 `DIRECTORY_SELECTION_CANCELLED`，不改变原目录。

### 应用设置

```
GET /api/settings
→ { "theme": "homebrew", "fontSize": 13, "cursorBlink": true, "scrollback": 10000,
    "downloadDirectory": "/Users/me/Downloads",
    "shortcuts": [{ "from": "Ctrl+Shift+P", "to": "Ctrl+A" }] }

PUT /api/settings
  body: 同上完整结构
→ 保存后的完整设置
```

`theme` 可取 `homebrew`、`clearDark` 或 `boring`，默认为 Homebrew；旧客户端保存时省略该字段也会使用默认值。字号范围为 10–24，回滚行数范围为 1000–50000，下载目录必须是绝对路径。默认值为 13px、光标闪烁、10000 行和空快捷键数组；旧版偏好缺少字段时自动使用默认值。最多保存 128 组映射，`from` 和 `to` 均不能为空。设置与下载目录、默认打开应用和隐藏连接共用 `preferences.json`，更新时保留其他字段。

全局 `shortcuts` 只作为新 Session 的创建模板。Session 内的映射由前端独立保存，不通过此接口回写；已有 Session 不会因 PUT 全局设置而自动改变。旧 profile 的 `options.shortcuts` 字段仍为配置兼容保留，但不再作为新 Session 的快捷键来源。

接口只返回私钥路径和元信息，不返回私钥内容。OpenSSH profile 的生效配置由后端通过 `ssh -G <别名>` 解析，前端不自行展开 OpenSSH 指令。

### 日志

```
GET /api/logs/recent?lines=300
→ { "log": "{\"time\":\"...\",\"level\":\"INFO\",\"msg\":\"ssh_dial_success\",...}\n...", "path": "/.../logs/tindomiel.log" }

GET /api/logs/frontend/recent?lines=300
→ { "log": "{\"time\":\"...\",\"level\":\"INFO\",\"msg\":\"frontend_event\",...}\n...", "path": "/.../logs/frontend.log" }

POST /api/logs/frontend
{ "message": "界面事件内容", "level": "info" }
→ { "ok": true }
```

`lines` 默认 200，可设置为 1–1000。接口返回当前日志文件的末尾内容，不合并历史轮转文件。前后端日志均为 JSON Lines，分别写入 `tindomiel.log` 与 `frontend.log`，单个文件达到 4 MiB 后轮转，超过 3 天的备份自动清理；日志不包含 token、密码、私钥内容或私钥短语。前端日志级别支持 `debug|info|warn|error`，其他值按 `info` 记录。

### 会话（sessions）

```
POST /api/sessions
  body: { "profileId": "openssh:socat-248", "cols": 120, "rows": 32 }
→ { "sessionId": "s_xxx", "status": "connecting" }

GET /api/sessions
→ [{ "sessionId": "s_xxx", "profileId": "...", "status": "ready", "openedAt": 1731200000 }]

POST /api/sessions/:id/reconnect
→ { "sessionId": "s_xxx", "status": "reconnecting" }

GET /api/sessions/:id/cwd
→ { "path": "/home/ubuntu" }

DELETE /api/sessions/:id
→ { "ok": true }
```

内置 `profileId` 为 `local:shell` 时，后端使用当前 macOS 用户、`127.0.0.1:22`、默认私钥和 SSH Agent 创建本机 SSH 会话，不保存额外的连接配置；主机指纹确认、SFTP、资源监控和重连均复用普通 SSH 流程。macOS 需要在“系统设置 → 通用 → 共享 → 远程登录”中启用 SSH 服务。

状态取值：`connecting | ready | disconnected | reconnecting | closed | error`。

每个标签页创建独立会话。远程断线后的重连沿用逻辑 `sessionId`，但会建立新的 SSH 连接和远程 shell；接口不承诺恢复原 shell 或工作现场。显式 `DELETE` 才销毁逻辑会话，本地 WS 异常关闭不自动销毁 SSH 会话。

### 主机密钥确认

首次连接未知主机时，`POST /api/sessions` 返回 `409 Conflict`：

```json
{
  "error": {
    "code": "HOST_KEY_UNKNOWN",
    "message": "Unknown host key",
    "details": {
      "verificationId": "hk_xxx",
      "host": "192.168.31.248",
      "port": 22,
      "keyType": "ssh-ed25519",
      "fingerprint": "SHA256:..."
    }
  }
}
```

用户核对并明确接受指纹后：

```
POST /api/host-keys/accept
  body: { "verificationId": "hk_xxx", "fingerprint": "SHA256:..." }
→ { "ok": true }
```

`verificationId` 短期有效且只能使用一次。后端验证两者匹配后保存主机密钥，前端随后重新创建会话。

主机密钥与 `known_hosts` 记录不一致时返回 `HOST_KEY_CHANGED` 并拒绝连接。该错误不能通过上述确认接口绕过。

### SFTP

远端拒绝 SFTP subsystem 时，相关接口返回 `502` 和 `SFTP_UNAVAILABLE`，消息为“远端未启用或无法提供 SFTP”；前端文件区域直接展示该状态，不再显示为普通目录读取失败。原始服务端错误保留在后端日志中。

```
GET  /api/sftp/:sessionId/list?path=/home/ubuntu
→ [{ "name": "config.json", "type": "file", "size": 4221, "mtime": 1731200000, "created": 1731100000 }]

POST /api/sftp/:sessionId/download-ticket
→ { "ticket": "short-lived-one-time-value", "expiresAt": 1731200060 }

GET  /api/sftp/:sessionId/download?path=/etc/hostname&ticket=short-lived-one-time-value
→ 文件内容（application/octet-stream，也可使用 Bearer token 鉴权）

POST /api/sftp/:sessionId/upload?path=/home/ubuntu/
→ multipart/form-data，单文件，请求体流式写入远端
→ { "ok": true, "written": 5330944 }

POST /api/sftp/:sessionId/download-file
  body: { "path": "/home/ubuntu/config.json" }
→ { "path": "/Users/me/Downloads/config.json", "bytes": 4221 }

POST /api/sftp/:sessionId/download-directory
  body: { "path": "/home/ubuntu/project" }
→ { "path": "/Users/me/Downloads/project", "files": 42, "bytes": 5330944 }
```

`created` 为 Unix 秒时间戳，表示远程文件出生时间。Linux 使用 GNU `stat --printf='%W\0'` 每 50 个路径批量查询；macOS 使用 BSD `stat -f '%B'` 并通过 `tr` 转为 NUL 分隔；Windows 使用 PowerShell `CreationTimeUtc` 逐项容错查询，并将 SFTP 的 `/C:/...` 路径规范化为 `C:/...` 后执行。无法获取时为 0，前端显示 `—`，不会用 `mtime` 代替。非 Windows 命令失败会立即返回，不进入交互 Shell 回退；目录树展开节点并行加载。

上传下载不得先将整个文件读入内存。界面中的普通文件和文件夹下载均通过 POST 接口直接写入用户选择的下载目录，完成后返回实际本地路径；同名文件在扩展名前加 `-1`、`-2`，同名目录在名称后加序号，不覆盖已有内容。每个文件先写临时文件再原子改名。GET 下载 ticket 接口保留用于直接流式下载。

文件夹下载复用同一会话的 SFTP channel，递归保留目录结构和普通文件权限，空目录也会创建；符号链接及非常规文件跳过，不跟随链接目录。文件夹任务显示递归文件总字节数和总进度，WS `transfer` 事件的 `currentPath` 表示当前正在传输的远程文件，`path` 始终保持文件夹根路径。路径切换事件立即发送，字节进度按 100ms 节流。

SSH 断线会使进行中的传输失败；重连后由用户重新发起，首版不自动续传。

### 绝对路径与缓存编辑

```
GET  /api/sftp/:sessionId/realpath?path=.
→ { "path": "/home/ubuntu" }

POST /api/sftp/:sessionId/open
  body: { "path": "/home/ubuntu/config.json", "application": "/Applications/Visual Studio Code.app" }
→ { "cachePath": "/Users/me/Library/Caches/Tindomiel/s_xxx/.../config.json", "application": "/Applications/Visual Studio Code.app" }

POST /api/local/choose-application
→ { "path": "/Applications/Visual Studio Code.app" }
```

显式传入 `application` 时会将其持久化为后续默认打开方式；之后省略该字段即沿用保存的应用，直到再次更改。未设置过偏好时才使用 macOS 系统默认应用。偏好保存在配置目录的 `preferences.json`（0600），重启服务后仍有效。

`open` 首次调用时下载到会话私有缓存并启动目录监听，再次打开同一文件不会覆盖尚未回传的本地修改。本地保存事件去抖后自动上传原远程路径，WS 发送 `{ "type": "sftp", "action": "saved", "path": "...", "message": "..." }`；失败使用 `action: "save-error"`。

### 资源监控

```
GET /api/metrics/:sessionId
→ {
    "timestamp": 1794468642,
    "system": { "hostname": "socat", "osName": "Ubuntu 22.04.4 LTS", "kernelVersion": "5.15.133.1", "architecture": "x86_64", "cpuModel": "Intel Core i7", "cpuCores": 16 },
    "uptimeSeconds": 259200,
    "loadAverage": [0.14, 0.34, 0.75],
    "cpuPercent": 1.2,
    "memory": { "total": 33554432000, "used": 3221225472, "available": 30333186000, "usedPercent": 9.6 },
    "swap": { "total": 8589934592, "used": 22020096, "usedPercent": 0.25 },
    "processes": [{ "pid": 123, "cpuPercent": 1.7, "memoryBytes": 486000000, "command": "kimi-code" }],
    "interfaces": [{ "name": "eth4", "rxBytesPerSecond": 9000, "txBytesPerSecond": 1000 }],
    "pingMs": 11.2,
    "disks": [{ "path": "/", "total": 1000000000000, "used": 899000000000, "available": 101000000000, "usedPercent": 89.9 }]
  }
```

每个会话由后端后台 goroutine 持续采集并维护最近快照，首次采样按 Session ID 在 1 秒窗口内错峰。普通资源快照的 `timestamp` 是本机 Go sidecar 在动态采集完成时生成的 Unix 秒级时间戳，不读取或代表远端服务器时钟。静态系统信息按 SSH 连接代次缓存，重连后重新采集。GET 优先返回缓存，初次缓存尚未建立时才同步采集；前端不通过定时 REST 请求维持监控，而是接收 WS 推送。进程默认按资源占用降序，点击 CPU 或内存表头可切换升序/降序。

网卡使用独立采样链路，不等待 CPU、磁盘和进程等整组资源采集。Linux/macOS 每秒读取累计字节数，以相邻两次成功采样的差值除以跨过的整数秒数；失败或 900ms 超时的秒槽推送 0，不推进累计值基线，之后成功时按实际跨过的秒数求平均，且不回填之前的失败秒。Windows 通过持续运行的 `typeperf` 通道直接读取系统每秒速率，连续样本按序号分配逐秒时间槽，避免进程启动耗时和网络到达抖动破坏周期；启动、中断和恢复期间缺秒补 0。网络样本通过独立 WS `network` 帧携带本机 Unix 秒级时间戳，普通 `metrics` 帧和 REST 快照只附带最近一次网络结果，不重复驱动图表。

后端按名称排序网卡；前端自动模式保持当前活动网卡，也可按接口名手动切换并立即刷新。网络图以中间横线为零轴，红色上行柱向上、绿色下行柱向下，图表高度、柱宽和间距固定。每根柱严格按时间戳表示一秒，同一秒覆盖、缺秒补 0、窗口内迟到样本按原时间回填，不做插值。每个 Session 的每张网卡分别根据最近 40 秒的上下行样本计算共用滚动刻度，峰值进入或离开窗口时，上传和下载柱统一重绘；图下显示起止时刻，悬停柱子显示该秒时间和速率。

后端通过 `uname -s` 区分 Linux、macOS（Darwin）和 Windows。macOS 监控使用系统自带 `top`、`vm_stat`、`vm.swapusage`、`netstat`、`df` 和 `ps`，无需安装远端依赖。

Windows 同时兼容 `cmd.exe` 和 PowerShell 默认 Shell。PowerShell 采集使用 UTF-16LE Base64 的 `EncodedCommand`；远端拒绝 exec 时改用独立交互 Shell（`TERM=dumb`），结果按带序号的 Base64 短行返回并校验完整性，解码为 `SYS|...`、`PROC|...`、`NET|...`、`DISK|...` 键值文本，避免 ConPTY 长行控制序列污染。若 `cmd.exe` 禁止启动 PowerShell，监控自动切换并缓存到 `systeminfo` + `typeperf` 原生采集路径，不再依赖嵌套 PowerShell。Windows 创建时间查询中的 `/C:/...` 会转换为 `C:/...`；PowerShell 采集的交换空间来自 `Win32_PageFileUsage`，`cmd` 采集结合虚拟内存总量和分页文件使用率。

## WebSocket：终端

前端先获取一次性 ticket，再建立连接：

```
POST /api/sessions/:id/ws-ticket
→ { "ticket": "short-lived-one-time-value", "expiresAt": 1731200060 }

WS /ws/terminal?sessionId=s_xxx&ticket=short-lived-one-time-value
```

### 数据帧（二进制，双向）

- 前端 → 后端：键盘输入的原始字节，直接写入 SSH channel stdin
- 后端 → 前端：SSH channel stdout/stderr 原始字节，前端 `xterm.write()`
- `disconnected` 和 `reconnecting` 状态下前端停止发送输入，不在本地排队

### 控制帧（JSON 文本）

前端 → 后端：

```json
{ "type": "resize", "cols": 120, "rows": 32 }
```

后端 → 前端：

```json
{ "type": "status", "status": "ready" }
{ "type": "status", "status": "disconnected", "message": "SSH connection lost" }
{ "type": "status", "status": "reconnecting" }
{ "type": "exit", "code": 0 }
{ "type": "error", "message": "Connection closed by remote host" }
{ "type": "network", "network": { "timestamp": 1794468642, "interfaces": [{ "name": "eth4", "rxBytesPerSecond": 9000, "txBytesPerSecond": 1000 }] } }
{ "type": "metrics", "metrics": { "system": {}, "cpuPercent": 1.2, "memory": {}, "interfaces": [], "disks": [] } }
```

`disconnected` 仅表示远程 SSH 连接失效，不代表本地 WebSocket 断开。`exit` 表示远程 shell 正常退出或逻辑会话结束，不用于普通的网络断线。

## 时序：建立会话

1. 前端 `POST /api/sessions` → 拿到 `sessionId`
2. 前端获取一次性 ticket，打开 `WS /ws/terminal?...`
3. 后端完成主机密钥校验和 SSH 认证，发送 `status: ready` 后开始转发字节流
4. 前端 resize → 后端 window-change → 远程 PTY 尺寸更新
5. 远程 shell 退出或用户显式关闭会话：后端发送 `exit`，会话进入 `closed`

## 时序：远程断线与重连

1. 后端检测到远程 SSH 失效，发送 `status: disconnected`
2. 前端保留当前 xterm 实例及已渲染内容，禁用输入并更新标签页状态灯
3. 后端自动重连，或前端调用 `POST /api/sessions/:id/reconnect`
4. 后端发送 `status: reconnecting`，成功后发送 `status: ready`
5. 新远程 shell 的输出追加到原 xterm 实例；旧远程进程和 shell 状态不恢复
