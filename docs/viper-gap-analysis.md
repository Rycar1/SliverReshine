# Viper 三件套 vs sliverreshine — 功能差集与性价比分析

分析对象：`viperpython` / `vipermsf` / `viperjs`（Viper 平台的三个拆分组件）
对照对象：sliverreshine（Sliver 1.7.3 + Web 控制台）

---

## 0. 先说一个事实纠正

你给的三个 URL 是 `FunnyWolf/viperpython`、`FunnyWolf/vipermsf`、`FunnyWolf/viperjs`，
这三个地址在 GitHub 上**都是 404**。FunnyWolf 名下 88 个仓库里没有这三个名字。

它们是 Viper 早期拆分开发时的三个组件仓库，现在实际存活的位置是：

| 你给的地址 | 实际可用地址 | 说明 |
|---|---|---|
| `FunnyWolf/viperpython` | `JohnHubcr/viperpython` | "viper 后台代码" |
| `FunnyWolf/vipermsf` | `JohnHubcr/vipermsf` | "viper 自定义的msf" |
| `FunnyWolf/viperjs` | `JohnHubcr/viperjs` | "viper项目前端代码" |

FunnyWolf 后来把三者合并进了单仓库 `FunnyWolf/Viper`（5320★，2214 个文件，
描述 "Adversary simulation and Red teaming platform with AI"）。下面的分析基于
三个组件仓库的原始代码 + 合并仓库的模块文档。

---

## 1. 三个项目分别在做什么

### 1.1 viperpython —— 后端与模块框架（38.3 MB / 210 文件）

Django + Channels(WebSocket) + Redis + APScheduler。是整个 Viper 的大脑。

```
Core/          Django app：Host 模型、configs、lib
MODULES/       56 个模块（55 个有效 + __init__）
MODULES_DATA/  模块运行时需要的载荷资产（按模块名分目录）
PostModule/    模块框架本体 ← 最值钱的部分
PostLateral/   Host / Credential / Vulnerability 数据层
Msgrpc/        136 KB 单文件 MSF RPC 客户端
WebSocket/     Channels 消费者
```

**模块框架是 Viper 真正的核心设计**，不是那 55 个模块本身。每个模块就是一个
Python 类：

```python
class PostModule(PostMSFRawModule):
    NAME        = "获取Windows内存密码"
    DESC        = "Kiwi抓取内存中的windows用户明文密码,并保存到凭证列表."
    MODULETYPE  = TAG2CH.Credential_Access   # ATT&CK 战术分类
    PLATFORM    = ["Windows"]
    PERMISSIONS = ["Administrator", "SYSTEM"]
    ATTCK       = ["T1003"]
    REFERENCES  = ["https://attack.mitre.org/techniques/T1003/"]
    REQUIRE_SESSION = True
    OPTIONS = register_options([...])        # 声明式参数

    def check(self):                         # 执行前自检
        return True, None
    def callback(self, status, message, data):  # 结构化结果
        ...
```

配套的 `PostModule/lib/OptionAndResult.py` 提供类型化参数：
`OptionStr / OptionIntger / OptionBool / OptionEnum / OptionIPAddressRange /
OptionFileEnum / OptionCredentialEnum / OptionHander / OptionCacheHanderConfig`，
以及三种**动态参数**——运行时才解析的：

- `_msgrpc_handler` → 渲染成"选择监听器"下拉框
- `_postmodule_credential` → 渲染成"从凭证库选一条"下拉框
- `_postmodule_file` → 渲染成"从数据管理里选一个文件"下拉框

结果类型也有规范：`result_type_list = ['str', 'list', 'dict', 'table']`，
前端按类型渲染表格 / 列表，而不是丢一段纯文本。

**这个设计的效果：加一个功能 = 丢一个 .py 文件，前端零改动。**

### 1.2 vipermsf —— 魔改版 Metasploit（74.6 MB / 13161 文件）

不是包装器，是一整个 MSF fork。Viper 在里面加了自己的模块，全部带 `_api` 后缀
——含义是"给程序调用的非交互版本"，去掉菜单、去掉交互提示、参数全部走 RPC。

自研 post 模块 19 个：

```
windows/escalate/getsystem_api                提权（5 种技术）
windows/gather/credentials/kiwi_api           内存密码
windows/gather/credentials/browser_history    浏览器历史
windows/gather/hashdump_api                   SAM 哈希
windows/gather/arp_scanner_api                ARP 扫描
windows/manage/wdigest_caching_api            开启 WDigest 明文缓存
windows/manage/execute_pe_in_memory_api       内存加载 PE
windows/manage/execute_assembly_api           内存加载 .NET
windows/manage/shellcode_inject_api           Shellcode 注入
windows/manage/payload_inject_api             载荷注入
windows/manage/process_handle_api             进程句柄窃取
windows/manage/priv_migrate_api               提权式迁移
windows/manage/add_user_api                   加账号
windows/manage/registry_api                   注册表
multi/manage/portfwd_api                      端口转发
multi/manage/upload_and_exec_api              上传执行
multi/manage/exec_python_with_params_api      Python 执行
multi/manage/file_system_operation_api        文件操作
windows/manage/powershell/exec_powershell_function_mem_api  内存 PS
```

自研 exploit 模块 14 个：MS17-010、psexec、wmi、wmi_hash、ssh exec、
Office 宏、vbulletin RCE、5 个持久化、S4U、进程句柄。

外加一个自研载荷 `lib/msf/core/payload/windows/reflective_pe_loader.rb`
——反射式 PE 加载器。

### 1.3 viperjs —— 前端（432 文件）

React + umi + Ant Design Pro。9 个业务页面：

```
Core/HostAndSession      主机与会话
Core/Credential          凭证库
Core/PayloadAndHandler   载荷与监听器
Core/RunModule           模块运行（通用表单）
Core/FileMsf             数据管理
Core/MsfSocks            MSF SOCKS 代理
Core/MuitHosts           多主机面板
Core/SystemSetting       系统设置
User/Login               登录
```

值得注意的组件：`Xterm/`（xterm.js 终端）、`ActiveChart/`（主机世界地图）、
`Authorized/`（RBAC 权限）、`services/geographic.js`（地理位置）。

---

## 2. 架构对比：两条完全不同的路线

```
Viper:
  浏览器 (React/umi)
      │ REST + WebSocket
      ▼
  Django (viperpython)
      │ 模块框架 + Host/Credential/Vuln 数据层
      ▼
  MSF RPC (vipermsf)
      │
      ▼
  Meterpreter / Python / Webshell session

sliverreshine:
  浏览器 (React/Vite)
      │ REST + WebSocket (177 路由)
      ▼
  sliverreshine (Go)
      │ gRPC (178 个客户端方法)
      ▼
  sliver-server
      │
      ▼
  Sliver session / beacon
```

关键差异：

| 维度 | Viper | sliverreshine |
|---|---|---|
| 引擎 | Metasploit（有 2000+ 现成模块可包） | Sliver（183 个 RPC 方法，无 exploit 库） |
| 后端语言 | Python/Django | Go |
| 功能扩展方式 | **丢一个 .py 文件** | 改 Go + 改 TSX + 改 2 个 locale |
| 参数渲染 | 框架自动生成表单 | 每个 tab 手写 |
| 会话类型 | Meterpreter / Python / Webshell 三种 | 仅 Sliver session/beacon |
| 部署 | Docker Compose 多容器 | **单 zip 解压即跑**（我们的优势） |

**最本质的一条：Viper 的模块框架之所以成立，是因为 MSF 有 2000+ 个现成模块可以
包成 `_api`。我们没有 MSF 模块库，所以我们不能照抄它的模块清单——但可以照抄它的
框架思想，去包装我们已有的 178 个 RPC 方法。**

---

## 3. 功能差集：他有我们没有

我方基线：177 条 REST 路由、178 个客户端方法、16 个会话 tab
（`files / processes / network / env / exec / screenshot / portfwd / registry /
advanced / tokens / wg / pivot / services / postex / persistence / harvest`）、
15 个持久化模块、mimikatz 全流程、`/api/rpc/*` 透传。

### 3.1 架构级缺失

| # | Viper 能力 | 证据 | 我方现状 | 缺口 |
|---|---|---|---|---|
| 1 | **声明式模块框架**：类型化参数 + 自动表单 + `check()` 预检 + ATT&CK 标签 + 结构化结果 + 3 种动态参数 | `PostModule/lib/OptionAndResult.py`、`PostModule/module/__init__.py` | 无。每个功能都是 Go handler + TSX 手写 | **架构级** |

### 3.2 能力级缺失

| # | Viper 能力 | Viper 证据 | 我方现状 | 缺口 |
|---|---|---|---|---|
| 2 | **PE/EXE → Shellcode** | `GeneratesShellcodeFromPEorDll` | `/api/shellcode/rdi` 只做 DLL→shellcode（sRDI） | 中 |
| 3 | **任意 PE 内存加载** | `PeLoader`、`execute_pe_in_memory_api` | `Sideload`/`SpawnDll` 仅支持 DLL（需导出表） | 中 |
| 4 | **PowerView 域内枚举**（5 个模块） | `Discovery_*_PowerView` | 无 | 大 |
| 5 | **Pass-the-Hash / Pass-the-Ticket**（WMI 路径） | `LateralMovement_*` | 部分（`MakeToken`/`Impersonate`/`RunAs`/`psexec`） | 中 |
| 6 | **Bot/蠕虫式批量利用** | `Bot_MSF_Scan`/`Bot_MSF_Exp`、`bot_msf_job` | 无 | 极大 |
| 7 | **主机地理地图 + 多主机面板** | `Geoip`、`ActiveChart/`、`MuitHosts` | 仅 `/api/pivots/graph` 关系图 | 小（纯前端） |
| 8 | **漏洞实体与跟踪** | `PostModule/lib/Vulnerability.py` | 无 | 小～中 |
| 9 | **凭证库被操作直接消费** | `OptionCredentialEnum` 动态下拉 | `/api/creds` 有存储，但没接进任何操作 | **小（接线）** |
| 10 | **浏览器数据提取**（密码/Cookie/历史） | `CredentialInFiles_BrowserData`、`browser_history_api` | 无 | 小～中 |
| 11 | **开启 WDigest 明文缓存** | `wdigest_caching_api` | 只有 `sekurlsa::wdigest` 读取 | **极小** |
| 12 | **代码签名滥用**（窃取微软签名 / PE 签名劫持 / 克隆 SSL PEM） | 3 个模块 | 无 | 中 |
| 13 | **会话克隆 / 进程句柄窃取** | `SessionClone`、`ProcessHandle` | 无（只有 `Migrate`） | 中 |
| 14 | **UAC 绕过** | `BypassUserAccountControl_Windows` | 无 | 中 |
| 15 | **带 ATT&CK 映射的报告生成** | ATT&CK 标签 + `engagement-reporter` agent | 无 | **小** |
| 16 | **模块链式编排 / job broker** | `BROKER`、APScheduler | 仅 `/api/jobs` 查询 | 小～中 |
| 17 | **共享文件池作为模块输入** | `FileMsf` + `OptionFileEnum` | `/api/loot` 已有 | 小 |
| 18 | **监听器配置随持久化缓存**（掉线自动回连） | `cacheHandlerConfig` | 无 | 小 |
| 19 | **多会话类型统一**（Meterpreter/Python/Webshell 同一 UI） | `PostPythonModule`、pystinger | 仅 Sliver | 极大 |
| 20 | **钓鱼 / 宏文档生成** | `Spearphishing`、`office_word_macro_api` | 无 | 大，且偏离定位 |

### 3.3 我方反超或已覆盖

| 能力 | 说明 |
|---|---|
| **持久化广度** | 我们 15 个模块（含 win-watchdog、linux-watchdog、linux-systemd-user），Viper 约 9 个 |
| **WASM 扩展运行时** | 我们有 `RegisterWasmExtension`/`ExecWasmExtension`，Viper 无对应物 |
| **WireGuard 传输** | 我们有 WG socks/forwarders，Viper 无 |
| **单包部署** | 单 zip 解压即跑，Viper 要 Docker Compose 多容器 |
| **原生载荷生成** | mTLS/WireGuard/HTTP/DNS + canary + per-implant profile |
| **RPC 透传** | `/api/rpc/methods` + `/api/rpc/call` 覆盖全部 183 个方法 |
| **流量/Shellcode 编码器** | 有，Viper 无 |
| **SOCKS** | 我们有 Sliver socks + WG socks，Viper 只有 MSF socks |
| **进程转储** | 有（带 >1G 护栏），Viper 无 |

---

## 4. 性价比排序：该实现哪些

排序依据：**能否复用现有 RPC（不新增植入体能力）× 可见收益 × 是否解锁后续功能**。

### Tier S —— 最高性价比（前端/接线为主）

**S1. 通用模块框架（Playbook 引擎）** ★ 唯一架构级投资

不要照搬 Viper 的 55 个模块，照搬它的**框架**：把"操作"抽象成数据。

```yaml
# ops/wdigest-enable.yaml
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

一个 Go 执行器 + 一个通用 React 表单渲染器，之后**每个新功能都是一个 yaml 文件**，
不再需要改 Go、改 TSX、改两个 locale。ATT&CK 标签、`check()` 预检、结构化结果、
动态参数（监听器/凭证/文件下拉）全部免费获得。

- 成本：一个迭代
- 收益：下面所有 Tier A/B 项的成本降一个数量级
- 同时解决当前 3 个待办（mimikatz 弹窗、持久化弹窗、basic auth 开关）的**呈现层统一问题**

**S2. 凭证库接入所有操作** ★ 纯接线

`/api/creds` 已经存了 mimikatz 解析出来的账号密码，但没有任何操作能选它。
给 `make-token` / `runas` / `psexec` / `ssh` / `msf` / `execute-token` 加上
"从凭证库选一条"的下拉。成本：前端为主。收益：凭证库从摆设变成枢纽。

**S3. 开启 WDigest** ★ 三行注册表

用现成的 `POST /api/sessions/{id}/reg/write` 写 `UseLogonCredential=1`，
下次有人登录就能从内存里抓到明文。Viper 的 `wdigest_caching_api` 就是这个。
成本：一个模块定义。收益：直接提升 mimikatz 命中率。

**S4. 报告生成 + ATT&CK 映射**

我们已经有 `/api/hosts`、`/api/creds`、`/api/loot`、`/api/events`、`/api/sessions`，
加上 S1 的 tactic 标签，生成 Markdown/HTML 交互动报告 = 后端模板 + 一个页面。
成本：小。收益：交付物层面价值极高。

**S5. 主机地理地图 + 拓扑视图**

`/api/hosts` 已有主机信息，`/api/pivots/graph` 已有关系数据。
前端加 Leaflet 世界地图 + d3 力导向图即可。成本：纯前端。收益：演示与态势感知。

### Tier A —— 高性价比（小后端，复用现有原语）

**A1. PE → Shellcode（donut 路线）** ★ 顺带解决 mimikatz 内存加载

- `github.com/Binject/go-donut` 可拉取，Sliver 已 vendor 了 Binject 全家桶
  （`binjection` / `debug` / `universal` / `shellcode`）+ `sliverarmory/wasm-donut`
- 收益一：任意 EXE → shellcode → 走现成的 `POST /api/sessions/{id}/exec-shellcode`
  或 `Task` 注入，实现"任意 PE 内存执行"
- 收益二：**直接解决你之前提的 mimikatz 内存加载**——
  `mimikatz.x64.exe` 是纯 EXE、无导出表，所以 `SpawnDll`/`Sideload` 都用不了；
  但 donut 能把它转成 shellcode，就能走内存注入路线
- 成本：加依赖 + 一个 endpoint + 一个 UI 选项

**A2. 浏览器数据提取**

把 HackBrowserData 之类的 .NET 工具通过现成的 `ExecuteAssembly` 投进去，
输出解析后写入凭证库（复用 S2 的接线）。成本：打包二进制 + 解析器。

**A3. 域内信息收集（PowerView 等价物）**

不需要 PowerView 本身——用现成的 `ExecuteAssembly` 跑一个编译好的 .NET
侦察程序（Seatbelt/SharpHound 一类），输出结构化解析。成本：打包 + 表单。
收益：AD 环境里最高频的需求。

**A4. 会话克隆 / 进程句柄窃取**

复用现有 `Migrate` + token 原语组合。成本：中低。

**A5. 共享文件池接入模块输入**

`/api/loot` 已有存储，接一个"从文件池选"的动态参数即可（S1 框架的一部分）。

**A6. 监听器配置随持久化缓存**

持久化模块执行成功后把监听器配置存起来，供重新上线时用。成本：小。

### Tier B —— 中等性价比（需要真正开发）

| 项 | 说明 | 判断 |
|---|---|---|
| UAC 绕过 | 需要技术库（多种 UAC bypass 实现） | 有明确需求再做 |
| 代码签名滥用 | 需要签名流水线 + 证书提取 | 对抗强度高时再做 |
| Pass-the-Hash/Ticket 补全 | 我们有部分原语，缺 WMI 执行路径 | 有域环境时再做 |
| 漏洞实体跟踪 | 需要新的数据模型 | S1 完成后成本大幅下降 |

### Tier C —— 不建议做

| 项 | 理由 |
|---|---|
| Bot/蠕虫批量利用 | 需要完整 exploit 库 + 扫描编排，成本极高，且 Sliver 无 exploit 模块 |
| 多会话类型（Webshell/Python） | 等于新增一类植入体，架构级工程 |
| 钓鱼 / 宏文档生成 | 属于独立产品（Viper 自己也是单独仓库 `PhishingInstall`） |
| 全量集成 MSF | 我们已有 `/api/msf/*`；全量引入 MSF 会与 Sliver 重复且拖垮单包部署优势 |

---

## 5. 建议落地顺序

```
第 1 步  S1 通用模块框架（含 S3 WDigest 作为第一个验证模块）
         └─ 顺手统一 mimikatz / 持久化 的窗口隐藏与运行模式（当前待办）
第 2 步  S2 凭证库接线 + A5 文件池接入
第 3 步  A1 donut（PE→shellcode，含 mimikatz 内存加载模式）
第 4 步  S4 报告生成 + S5 地图拓扑
第 5 步  A2 浏览器数据 + A3 域内收集
第 6 步  A4 会话克隆 + A6 监听器缓存
```

第 1 步做完之后，第 2～6 步里大部分"新功能"会退化成写 yaml，
这是唯一一个能改变后续所有工作成本结构的投资。

---

## 6. 与当前待办的交叉

| 待办 | 归属 |
|---|---|
| mimikatz 弹窗修复 | 走 S1 的统一执行器（Windows 一律走 `ExecuteWindows` RPC + `HideWindow: true`） |
| mimikatz 运行前自动提权 | S1 的 `requires: { admin: true }` 预检 + `getsystem` 步骤 |
| mimikatz 两种运行方式 | A1 donut（内存加载）+ 现有上传执行 |
| 持久化模块弹窗 | 走 S1 的统一执行器（同一条修复路径） |
| basic auth 开关 | 独立小改动，与本次分析无关 |

---

## 附：数据来源

- `JohnHubcr/viperpython` — `MODULES/`（55 模块）、`PostModule/lib/OptionAndResult.py`、
  `PostModule/lib/Configs.py`、`PostModule/module/__init__.py`、`Msgrpc/msgrpc.py`（136 KB）
- `JohnHubcr/vipermsf` — 13161 条目，19 个自研 post `_api` 模块 + 14 个自研 exploit 模块
  + `reflective_pe_loader.rb`
- `JohnHubcr/viperjs` — 432 条目，9 个业务页面，`config/router.config.js`
- `FunnyWolf/Viper` — 合并仓库，5320★，2214 条目
- 我方基线 — `internal/ui/api/server.go` 177 路由、`internal/ui/sliver/` 178 方法、
  `frontend/src/pages/SessionDetailPage.tsx` 16 个 tab、`persistence.go` 15 个模块
