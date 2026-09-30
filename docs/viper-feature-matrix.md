# Viper C2 功能全解 vs c2tool — 差集与可实现性评估

分析对象：`FunnyWolf/Viper`（5320★，2214 条目）当前主线，含 `JohnHubcr/viperpython` /
`vipermsf` / `viperjs` 三个早期拆分仓库的源码。
对照对象：c2tool（Sliver 1.7.3 + Web 控制台，177 路由 / 171 个客户端方法 / 16 会话 tab）。

证据来源：`docs/en/guide/*.md`（60 篇功能文档）、`docs/en/module/*.md`（129 个模块）、
`MODULES/` 源码、`PostModule/` 框架源码、`Msgrpc/msgrpc.py`（136 KB）、
`Viper/urls.py`（完整 API 表）。

---

## 0. 先修正一个数字

上一版分析说 Viper "55 个模块"，那是**早期拆分仓库** `JohnHubcr/viperpython/MODULES/` 的数量。
当前主线的实际规模是：

| 维度 | 数量 |
|---|---|
| 模块 | **129**（另有 1 个 index 页） |
| 功能文档页 | 60 |
| API ViewSet | 24 |
| 后端语言 | Python / Django + Channels + Redis + APScheduler |
| 引擎 | 魔改 Metasploit（fork，非包装） |
| 前端 | React + umi + Ant Design Pro |

模块分布（按 ATT&CK 战术）：

```
Discovery            28
Execution            22   ← 加载器/回调执行/免杀，全部在这一块
DefenseEvasion       15
Web                  11   ← Nuclei/Wafw00f/nmap/CDNCheck/Quake/爱企查
Persistence          10
LateralMovement       8
PrivilegeEscalation   8
CredentialAccess      7
AI                    6   ← LangGraph 智能体
ResourceDevelopment   5
Bot                   4   ← 全网扫描/蠕虫
InitialAccess         2
Collection            2
HttpProxyScan         1
────────────────────────
合计                129
```

---

## 1. 你截图里那排 tab，逐个对照

截图是 vshell-verge 的界面，那排 tab 基本就是 Viper 的功能地图。逐个核对：

| 截图 tab | Viper 对应功能 | 我方现状 | 判定 |
|---|---|---|---|
| 实时输出 | `realtime` 模块结果流 + 平台日志 + **多用户聊天** | `/api/events` + `EventsPage` + `TerminalPage` | ⚠️ 有事件流，无模块结果面板、无多用户聊天 |
| 任务列表 | `runningjob` | `/api/jobs` + `/api/beacons/{id}/tasks` + `JobsPage` | ✅ 已有 |
| 监听载荷 | `handler_and_payload` | `/api/listeners` + `/api/implant-profiles` + `ListenersPage` | ✅ 已有 |
| **回连过滤** | `handler_firewall` | **无** | ❌ 缺 |
| **WebDelivery** | `web_delivery` | **无**（原语已有，见 §3.2） | ❌ 缺 |
| 文件列表 | `file_explorer` | `/api/sessions/{id}/fs` + `FilesPage`/`FilesTab` | ✅ 已有 |
| **网络拓扑** | `pivotgraph` | `/api/pivots/graph` 有数据，但前端只渲染成**树形列表** | ⚠️ 有数据、缺可视化 |
| 内网代理 | `routeproxy` / `route` | `/api/socks` + `/api/portfwd` + `/api/tunnels` + WG | ✅ 已有（模型不同，见 §3.4） |
| 凭证管理 | `credential` | `/api/creds` + `CredentialsPage` | ⚠️ 只存不用（见 W3） |
| **自动编排** | `automation` | **无** | ❌ 缺 |
| **智能助手** | `ai_agent` | **无** | ❌ 缺 |
| Msfconsole | `msfconsole` | `/api/msf/*` 只有一次性调用 | ⚠️ 部分 |
| 平台设置 | `common_config` | `/api/settings/auth` + `SettingsPage` + Basic Auth | ✅ 已有 |

另外截图里能看到但不在 tab 上的：会话行的 **GeoIP/ASN 标注**（`tunnel_peer_locate` /
`tunnel_peer_asn`，GeoLite2-City/ASN.mmdb）、**主机标签**（截图里的"保留"）、
**权限数徽标**（`+15` / `+16`）。

---

## 2. Viper 有、我们没有的完整清单

### 2.1 数据实体层（我们完全没有）

| 实体 | Viper 证据 | 说明 |
|---|---|---|
| `HostModel` | `Core/models.py` | 主机表，带 **tag**（web_server/db_server/firewall/ad_server/pc/oa/cms/other）+ comment |
| `PortServiceModel` | `PostLateral/models.py` | 端口服务表，banner 解析出软件/版本/主机名/OS/设备类型/MAC，按 hid 归属 |
| `CredentialModel` | `PostLateral/models.py` | 凭证表，含 `password_type`（windows/userinput/browsers）、`tag`、`source_module`、`host_ipaddress` |
| `VulnerabilityModel` | `PostLateral/models.py` | 漏洞表，按 `source_module_loadpath` 归属，自动反查模块名 |
| `PostModuleResultHistory` | `PostModule/postmodule.py` | 模块历史结果，含**当时的参数快照** + 结果 + 时间 |

我们只有 Sliver 原生的 `Hosts`（含 IOC）和 `Creds`/`Loot`，**没有标签、没有端口服务、
没有漏洞、没有模块历史结果**。

### 2.2 框架层

| 能力 | Viper 证据 | 我方现状 |
|---|---|---|
| **声明式模块框架** | `PostModule/lib/OptionAndResult.py` + `module/__init__.py` | 无，每个功能 Go handler + TSX 手写 |
| **动态参数**（3 种） | `_msgrpc_handler` / `_postmodule_credential` / `_postmodule_file` 运行时渲染成下拉框 | 无 |
| **模块加载器** | `postmodule.py:_load_all_modules_config()` 用 `importlib` 扫 `MODULES/` 目录，读类属性，按战术排序，缓存 | 无 |
| **后台任务 broker** | `BROKER.post_python_job` / `post_msf_job` / `bot_msf_job` + APScheduler | 仅 `/api/jobs` 查询 |
| **结果类型规范** | `result_type_list = ['str','list','dict','table']`，前端按类型渲染 | 无 |
| **虚拟监听** | `Handler.create_virtual_handler()`，ID < 0，供持久化模块缓存回连配置 | 无 |

### 2.3 功能层

| # | 功能 | Viper 证据 | 缺口 |
|---|---|---|---|
| 1 | **回连过滤** | `guide/handler_firewall.md`：白名单→黑名单→云厂商→沙箱IP→地理位置 五级链式判定 | 大 |
| 2 | **WebDelivery** | `guide/web_delivery.md`：建服务 + 选监听 + 生成一行命令 | 小～中 |
| 3 | **自动编排** | `guide/automation.md`：新会话触发模块链 + 定时间隔 + **单主机权限数上限防死循环** | 中 |
| 4 | **AI Agent** | `guide/ai_agent.md` + 6 个 `AI_Agent_*_LangGraph_*` 模块 | 中 |
| 5 | **MCP Server** | `guide/mcpserver.md`：12 个工具暴露给 Cursor | 小 |
| 6 | **Bot 通知** | Telegram / DingDing / Bark / SMTP | 小 |
| 7 | **会话监控** | `guide/session_monitor.md`：新会话上线通知 | 小 |
| 8 | **反溯源** | `guide/avoid_tracing.md`：`nobody.sh` 改端口 + nginx 二次认证 | 小（我们已有 Basic Auth） |
| 9 | **全网扫描** | `guide/internet_scan.md`：FOFA/Quake/Hunter/爱企查 + 内置指纹规则 + 任务队列（最多 3 并发） | 中 |
| 10 | **Web 工具模块** | Nuclei / Wafw00f / nmap / CDNCheck / whois / subfinder | 中 |
| 11 | **Pivot Graph** | `guide/pivotgraph.md`：汇总**端口扫描 + 历史会话连接 + 当前会话 + 路由** | 中（前端为主） |
| 12 | **Transport 管理** | `guide/transport.md`：给会话增删多个传输协议、切换、sleep | 需改植入体 |
| 13 | **Route / autoroute** | `guide/route.md`：按网卡自动加路由 | 模型不同 |
| 14 | **Dashboard 运行信息** | `guide/dashboard.md`：主机信息缓存、外网连接、内网连接、ARP、重要进程 | 中 |
| 15 | **LazyLoader** | `Msgrpc/msgrpc.py:LazyLoader` + 无认证接口 `/api/v1/c` | 中 |
| 16 | **用户管理 / RBAC** | `Core/views.py` + `Authorized/` | 中 |
| 17 | **主机标签备注** | `guide/host_and_session_list.md` | 小 |
| 18 | **会话克隆 / 句柄窃取** | `SessionClone` / `ProcessHandle` 模块 | 中 |
| 19 | **UAC 绕过** | `BypassUserAccountControl_Windows` | 中 |
| 20 | **代码签名滥用** | 窃取微软签名 / PE 签名劫持 / 克隆 SSL PEM | 中 |
| 21 | **Potato 提权族** | GodPotato / SweetPotato / EfsPotato / CVE-2021-40449 | 中 |
| 22 | **加载器全家桶** | 22 个 Execution 模块：Syscall 注入、NtCreateSection、回调执行（ThreadpoolWait/TimerQueue/EnumWindows）、MSBuild、假 PPID、VSSyscall | 大 |
| 23 | **浏览器数据** | `BrowserData` / `BrowserDataCSharp` / `browser_history_api` | 小～中 |
| 24 | **WDigest 开启** | `WindowsWDigestEnable` | 极小 |
| 25 | **SunLogin/向日葵凭证** | `CredentialDumping_SunLogin` | 小 |
| 26 | **麦克风/摄像头** | `Microphone_Camera` / `Record_Mic` / `CallInfo` | 小 |
| 27 | **被动扫描** | `guide/passive_scan.md` | 中 |
| 28 | **Ladon / 内网工具集成** | `LateralMovement_Other_Ladon` | 中 |
| 29 | **HTTP 代理扫描** | `HttpProxyScan_Log4J2` | 小 |
| 30 | **钉钉/企业微信/爱企查 信息收集** | `Web_Company_AiqichaSearch*` | 小 |

### 2.4 我们反超或已覆盖

| 能力 | 说明 |
|---|---|
| 持久化广度 | 我们 15 个模块（含 win-watchdog / linux-watchdog / linux-systemd-user），Viper 约 9 个 |
| WASM 扩展运行时 | `RegisterWasmExtension` / `ExecWasmExtension`，Viper 无对应物 |
| WireGuard 传输 | WG socks / forwarders / listener，Viper 无 |
| **单包部署** | 单 zip 解压即跑；Viper 要 Docker Compose 多容器 |
| 原生载荷生成 | mTLS/WireGuard/HTTP/DNS + canary + per-implant profile + spoof metadata |
| **RPC 透传** | `/api/rpc/methods` + `/api/rpc/call` 覆盖全部 186 个 RPC 方法 |
| 流量/Shellcode 编码器 | TrafficEncoder + ShellcodeEncoder，Viper 无 |
| 进程转储 | `ProcessDump`（带 >1G 内存护栏），Viper 无 |
| Canaries | 蜜标/回调告警，Viper 无 |
| 证书管理 | CA + 证书列表，Viper 无 |
| 多平台植入体 | Windows/Linux/macOS + ARM，Viper 有 |

---

## 3. 哪些能实现 —— 逐项可行性评估

评估依据：**能否复用 Sliver 现有 RPC（不改植入体）× 成本 × 收益**。

### 3.1 能直接做，且成本低

#### ✅ W1. WebDelivery（回连一行命令）
**原语已全部具备**：`StartTCPStagerListener`（未封装）、`GenerateStage`（未封装）、
`Websites`/`WebsiteAddContent`（已封装）、`ShellcodeRDI`（已封装）。
做法：建 stager 监听 → 拿到 stager URL → 用 `Websites` 托管一行命令脚本
（PowerShell / python / regsvr32 / certutil / mshta）→ 界面显示命令。
成本：封装 2 个 RPC + 一个页面 + 命令模板。**收益高**（"能执行命令但不方便传文件"是高频场景）。

#### ✅ W2. MCP Server
我们已经有 `/api/rpc/methods` + `/api/rpc/call`，这本身就是现成的工具调度面。
挑一批安全的方法（列会话、列主机、跑命令、列监听、列凭证）映射成 MCP 工具，
走 stdio 或 SSE 暴露出去，就能在 Cursor/Claude 里直接驱动。
成本：低。**收益中高**（演示价值大）。

#### ✅ W3. 凭证库接入所有操作
`/api/creds` 已经存了 mimikatz 解析出来的账号密码，但**没有任何操作能选它**。
给 `make-token` / `runas` / `psexec` / `ssh` / `msf` / `execute-token` 加"从凭证库选一条"下拉。
成本：前端为主。**收益高** —— 凭证库从摆设变成枢纽。

#### ✅ W4. 会话通知（Bot / Webhook）
监听 `/api/events` 的会话上线事件 → 推 Telegram / 钉钉 / Bark / SMTP / 自定义 webhook。
成本：低。**收益中**（无人值守时非常有用，Viper 的 session_monitor 就是这个）。

#### ✅ W5. 主机标签与备注
Sliver 的 Host 没有 tag/comment。用一个按 `HostUUID` 索引的旁路元数据存储（JSON/SQLite）补上。
成本：低。**收益中**（资产梳理 + 喂给拓扑图和目标列表）。

#### ✅ W6. 开启 WDigest 明文缓存
用现成的 `POST /api/sessions/{id}/reg/write` 写
`HKLM\SYSTEM\CurrentControlSet\Control\SecurityProviders\WDigest\UseLogonCredential = 1`。
成本：一个操作定义。**收益**：直接提升 mimikatz 命中率。

### 3.2 能做，成本中低

#### ✅ W7. 网络拓扑图（截图里的核心）
**数据已经有**：`/api/pivots/graph` 返回 `Children[]`，每个节点带
`PeerID / Name / SessionID / Hostname / Username / OS / Transport / RemoteAddress`，
还有 `/api/sessions`、`/api/hosts`、`/api/socks`、`/api/portfwd`。
缺的是**渲染**：我们的 `PivotTab` 把它渲染成树形列表，不是图。
做法：加一个图库（react-flow / cytoscape / d3），后端加一个聚合端点把
会话 + 主机 + 监听 + 代理 + 路由合成 nodes/edges。
成本：中低（纯前端 + 一个聚合端点）。**收益高** —— 这是 Viper 最出彩的一屏。

#### ✅ W8. 回连过滤（Handler Firewall）
**这是 Viper 的招牌功能之一，但 Sliver 没有监听器 ACL**（已核对 `clientpb`，无
allowlist/blocklist 字段）。所以不能靠配置实现，要靠**代理层**：
c2tool 在前面绑公网端口，sliver 监听内部端口，中间过一条规则链
（白名单 → 黑名单 → 云厂商 ASN → 沙箱 IP → 地理位置）。
成本：中（Go TCP/HTTP 代理 + 规则引擎 + GeoLite2/ASN 库 + 界面）。
**收益高** —— 能挡住沙箱和蓝队直接拿 stager，是真实能力而不只是界面。

#### ✅ W9. 自动编排（Automation）
监听事件流 → 命中规则 → 按顺序执行模块链，带**执行间隔**和**单主机权限数上限**
（Viper 文档明确说了不加这个会因"会话克隆"类模块导致无限循环）。
成本：中。**收益高**，和 W10 组合是最大杠杆。

#### ✅ W10. 声明式模块框架 ★ 唯一架构级投资
不要照搬 Viper 的 129 个模块（它是靠 MSF 2000+ 模块撑起来的），照搬它的**框架**：
把"操作"抽象成数据（YAML/JSON），一个 Go 执行器跑我们的 171 个客户端方法，
一个通用 React 表单渲染器。
```yaml
id: wdigest-enable
name: 开启 WDigest 明文缓存
tactic: T1112
platforms: [windows]
requires: { admin: true }
options:
  - { name: enabled, type: bool, default: true, label: 启用 }
steps:
  - rpc: RegistryWrite
    args: { hive: HKLM, path: "SYSTEM\\...\\SecurityProviders",
            name: WDigest, value: "1" }
result: { kind: table }
```
成本：一个迭代。**收益：后面所有新功能的成本降一个数量级** ——
ATT&CK 标签、`check()` 预检、结构化结果、动态参数（监听器/凭证/文件下拉）全部免费获得。

#### ✅ W11. AI Agent（智能助手）
和 W2 同一套工具面（我们的 171 个客户端方法），加一个 LLM 工具调用循环 + 聊天界面 + API key 配置。
成本：中。**收益：可见度高**。

#### ✅ W12. 全网扫描 / Web 工具模块
FOFA / Quake / Hunter / ZoomEye / 爱企查 都是纯 HTTP API 封装，**不需要植入体**。
Nuclei / Wafw00f / nmap / whois / subfinder 在控制台主机上跑。
成本：中（每个是 API 封装 + 页面）。**收益中高** —— 这块我们目前零覆盖。

#### ✅ W13. Dashboard 运行信息
我们有 `/api/sessions/{id}/netstat`、`/ps`、`/ifconfig`。
加聚合 + 缓存 + 一个页面（外网连接 / 内网连接 / ARP / 重要进程）。
成本：中。**收益中**。

### 3.3 能做，但成本高或收益低

| 项 | 判断 |
|---|---|
| **端口服务实体 + 端口扫描** | Sliver **没有端口扫描 RPC**。要么走 SOCKS + 外部 nmap，要么写 WASM 扩展扫描器，要么 `ExecuteAssembly` 投 .NET 扫描器。成本中高。做完能喂给拓扑图和端口服务表。 |
| **漏洞实体** | 需要先有扫描器。成本低（有扫描器之后），单独做没意义。 |
| **会话克隆 / 句柄窃取** | 需要新原语，Sliver 只有 `Migrate`。成本中。 |
| **UAC 绕过 / Potato 族 / 代码签名滥用** | 需要技术库（多个 exploit 实现）。成本中，且需要明确需求。 |
| **22 个 Execution 加载器/免杀模块** | 需要 C/汇编/PE 开发能力。成本大。 |
| **用户管理 / RBAC** | 单操作员场景收益低。 |
| **LazyLoader** | 我们已有 WASM 扩展 + `Websites`，部分覆盖。 |

### 3.4 不建议做

| 项 | 理由 |
|---|---|
| **Bot / 蠕虫批量利用** | 需要完整 exploit 库 + 扫描编排；Sliver 没有 exploit 模块，等于重建 MSF |
| **多会话类型**（Meterpreter/Python/Webshell） | 等于新增一类植入体，架构级工程 |
| **Msfconsole** | 需要 zip 里塞一个 MSF RPC 服务，**破坏单包部署**这个核心优势 |
| **Transport 多协议增删** | Sliver 的 `Reconfigure` 只能改 beacon 间隔/抖动，**不支持给活着的会话加第二条 C2 通道**，要改植入体 |
| **Route / autoroute 全量克隆** | Sliver 用 pivot listener + WireGuard + SOCKS，是另一套模型，已覆盖同等能力 |
| **钓鱼 / 宏文档生成** | 独立产品（Viper 自己也是单独的 `PhishingInstall` 仓库） |

---

## 4. 优先级排序

```
第 1 梯队（低垂果实，几天量级）
  W3  凭证库接入操作          ← 接线，收益最高
  W7  网络拓扑图渲染          ← 数据已有，纯前端
  W6  开启 WDigest            ← 一个操作定义
  W5  主机标签备注
  W4  会话通知（Bot/Webhook）
  W1  WebDelivery

第 2 梯队（一个迭代）
  W10 声明式模块框架 ★        ← 做完之后 W6/W8/W12 全部退化成写 yaml
  W2  MCP Server

第 3 梯队（需要真正开发）
  W8  回连过滤（代理层）
  W9  自动编排
  W11 AI Agent
  W13 Dashboard 运行信息

第 4 梯队（按需）
  W12 全网扫描 / Web 工具
  端口扫描 + 端口服务实体 + 漏洞实体
```

**关键判断：W10 是唯一能改变后续所有工作成本结构的投资。**
做完它，第 1 梯队里 W5/W6 和后续大量小功能都退化成"写一个 yaml"，
不再需要改 Go、改 TSX、改两个 locale 文件。

---

## 5. 与当前待办的交叉

| 待办 | 归属 |
|---|---|
| mimikatz 运行前自动提权 | W10 的 `requires: { admin: true }` 预检 + getsystem 步骤 |
| mimikatz 两种运行方式（上传/内存） | 独立于本次分析；donut 路线（PE→shellcode）解锁内存模式 |
| 持久化模块弹窗 + 慢加载 | 已修复（`exec.go` 统一走 `ExecuteWindows` + `HideWindow`；inventory 并发化） |
| Basic Auth 开关 | 已完成，属于 W4/W8 的同类设置项 |

---

## 6. 数据来源

- `FunnyWolf/Viper` — `docs/en/guide/*.md`（60 篇）、`docs/en/module/*.md`（129 个模块）、
  README（产品对比表：Implants / Visual UI / Pivot Graph / Custom Plugin / Built-in Evasion /
  Automation / Team Collaboration / LLM Agent）
- `JohnHubcr/viperpython` — `MODULES/`、`PostModule/lib/OptionAndResult.py`、
  `PostModule/lib/ModuleTemplate.py`、`PostModule/postmodule.py`、`Msgrpc/msgrpc.py`（136 KB）、
  `Core/core.py`、`Core/models.py`、`Core/views.py`、`PostLateral/postlateral.py`、`Viper/urls.py`
- `JohnHubcr/vipermsf` — 13161 条目；41 个 `_api` 模块（19 post + 14 exploit + 5 auxiliary + 3 lib）
  + `reflective_pe_loader.rb`
- `JohnHubcr/viperjs` — 432 条目，9 个业务页面
- 我方基线 — `internal/ui/api/server.go`（177 路由）、`internal/ui/sliver/`（171 个客户端方法，封装 Sliver 的 186 个 RPC）、
  `frontend/src/pages/`（24 页面）、`SessionDetailPage.tsx`（16 tab）、`persistence.go`（15 模块）
