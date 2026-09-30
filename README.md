# SliverReshine

一个自包含的 C2 控制台：单个可执行文件内嵌 Sliver 服务端、Web 前端与凭据提取载荷，解压即用，无需额外依赖。

> **免责声明**：本项目仅供**授权**的安全测试、红队演练与安全研究使用。使用者必须确保对目标系统拥有明确的书面授权。未经授权访问他人系统在大多数司法管辖区属于违法行为。作者不对任何滥用行为负责。

---

## 它是什么

SliverReshine 不是新写的 C2，而是给 [Sliver](https://github.com/BishopFox/sliver) v1.7.3 加了一层**操作台**，并补了几个上游没有的能力：

| 能力 | 说明 |
|---|---|
| **单文件部署** | 控制台内嵌服务端，`./c2tool` 起来就是完整 C2 |
| **Web 控制台** | React 前端，中英双语，浏览器操作，不用记命令行 |
| **正向连接（Bind）** | 上游只有反向连接；本项目加了 implant 监听、C2 拨入的完整链路 |
| **多级代理生成** | 生成页可直接产出 `tcp-pivot` 中转载荷，链路成树 |
| **内嵌 mimikatz** | 一键凭据提取，自动上传到目标临时目录，权限够时自动提权 |
| **权限维持清单** | 18 个模块的安装/检查/移除，**手动触发**探测 |
| **原始 RPC 控制台** | 前端直接调 SliverRPC，上游能力没有遗漏 |

---

## 快速开始

### 从 release 下载

```bash
# Windows
unzip c2tool-windows-amd64-plain.zip
./c2tool.exe

# Linux
unzip c2tool-linux-amd64-plain.zip
./c2tool
```

首次启动会在控制台打印访问地址与随机密码，浏览器打开即可。

**首次启动会解压内嵌的服务端**（约 300 MB），需要几秒到几十秒，取决于磁盘。

### 从源码构建

```powershell
# 1) 构建服务端（需要 Go 1.25+）
build\build-server.ps1 -SliverDir .\sliver -Targets windows/amd64 -Out .\build\out

# 2) 打包（内嵌服务端 + 前端 + mimikatz）
build\build-release.ps1 -ServerDir .\build\out -GOOS windows -GOARCH amd64
```

产物在 `dist/`。

---

## 两个版本的区别

Release 里每个平台提供两个包：

| 包 | 说明 |
|---|---|
| `*-plain.zip` | 未加壳。**推荐日常使用。** |
| `*-upx.zip` | 用 [UPX](https://upx.github.io/) 加壳。 |

**关于 UPX 版本的实测数据**（Windows/amd64，实测非估算）：

```
原始服务端      336.3 MB
gzip 后         216.9 MB   (64.5%)
UPX --best 后   199.6 MB   (59.4%)
```

**UPX 只比 gzip 好约 8%，最终包体积减少约 15–20 MB。** 因为服务端里大部分是已压缩的工具链归档（`go.zip` / `zig.zip` / `src.zip`），gzip 已经接近极限。

**加壳的代价**：

- UPX 是 AV/EDR 的高频特征，加壳的控制台**更容易被本地杀软告警**
- 首次运行需要自解压，启动稍慢

**选择建议**：除非环境明确需要规避基于文件特征的检测，否则用 `plain`。

---

## 仓库结构

```
.
├── cmd/c2tool/              控制台入口
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
- 自动写入目标的 `%TEMP%`（回退 `%TMP%`、`C:\Windows\Temp`）
- 落盘名为 `mimi.x64.exe`，不用工具本名
- **权限不足时自动提权到 SYSTEM** 再运行
- 输出自动解析并存入凭据库

### 权限维持

**会话详情 → 权限维持** 标签页：

> ⚠️ **清单是手动触发的。** 打开页面**不会**在目标上执行任何命令。

原因：清单是探测式的，一次完整扫描会在目标上串行启动约 18 个进程（`reg` / `dir` / `schtasks` / `sc`），全部会进入进程创建审计与 EDR 遥测。所以扫描由点 **「重新扫描」** 触发，而不是打开页面就自动跑。

模块目录是静态的，打开就能看到，可以直接安装；只有「是否已安装」这一列需要扫描。

需要管理员的模块（HKLM、服务、计划任务、本地账户）在未提权会话上会失败，控制台的模块目录里标注了 `requiresAdmin`。

---

## 已知限制

| 限制 | 说明 |
|---|---|
| 包体积大 | 服务端内嵌了 Go/Zig 工具链，压缩后仍有约 200 MB |
| bind 端口运行期不可改 | 换端口需要重新生成载荷 |
| bind 不支持并发会话 | 会话期间监听端口关闭 |
| 权限维持探测留痕 | 扫描会在目标上产生约 18 个进程创建事件 |
| 服务端资产需下载 | 见下 |

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

内嵌第三方二进制是为了部署便利；分发时请遵守各自的许可条款。
