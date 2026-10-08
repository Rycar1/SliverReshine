# CS 插件合集调研：能借鉴什么

对五个工具包的只读调研。**重点是「哪些设计值得抄」，不是「它们有哪些功能」** ——
功能清单没有价值，实现思路才有。

调研对象：

| 工具 | 类型 | 规模 |
|---|---|---|
| Ladon 9.1.7 | CS 插件 + .NET/PowerShell 扫描器 | 174 模块 |
| 梼杌 TaoWu | CS 插件 | 13 模块 + ~150 二进制 |
| LSTAR | CS 插件 | 11 模块，~370 KB .cna |
| OLa 欧拉 | CS 插件 | 10 .cna，104 MB |
| 谢公子 | CS 插件 | 16 .cna，11 MB |

**先说结论：这些插件普遍「功能很全但不完整」** —— 我逐个核对引用，找出 36 个
引用了但不存在的文件，其中一些是核心能力。这一条本身比任何功能都值得学。

---

## 一、最值得借鉴的 5 个设计

### 1. 反射式批量分发：一个对话框驱动 N 个功能

梼杌 `discovery.cna` 用 6 行代码让一个复选框对话框驱动约 20 个独立采集函数：

```cna
sub main{  foreach $func (keys($3)){ if ($3[$func] eq 'true'){ eval($func.'();'); } } }
```

没有 dispatch 表、没有每项一个 handler。新增功能只需加一个 `sub` 和一行复选框。

**为什么值得抄**：这是「几百个命令、接近零维护」的扩展方式。对照我们自己的
`sliverreshine` —— 每加一个能力就要写路由 + handler + 前端入口，成本线性增长。

**对应的做法**：把「能力」做成注册式而非枚举式，前端从后端拉取能力清单而不是硬编码。

### 2. 把 shell 输出解析回控制台对象

两个插件都用 `on beacon_output` 把裸文本变成结构化状态：

**梼杌的 SharpDPAPI 解析器**是一个显式的四状态机，直接写入 CS 凭据库：

```cna
$STATE_ScanningForHeader = 1; $STATE_ParsingHeader = 2;
$STATE_ScanningForKeyHeader = 3; $STATE_ParsingKeys = 4;
...
if($line ismatch '\t \* sha1\(key\) :\t(.*)') { $sha1 = matched()[0];
    if($sha1) { credential_add($UserName, $guid . ":" . $sha1, $Domain, "mimikatz: DPAPI Master Key SHA1", $hostname); } }
```

**LSTAR 的 AV 检测**用一个哨兵字符串标记输出结束，再查表：

```cna
bshell($1,"tasklist /SVC && echo AntiVirusCheckComplete");
...
if("AntiVirusCheckComplete" isin $string1){ ... }
```

**为什么值得抄**：我们已经有 `ParseMimikatz` 做类似的事，但没有「哨兵结束标记」
这个概念 —— 我们靠超时判断输出结束，而超时既慢又不可靠。

### 3. 状态栏作为常驻报告通道

LSTAR 把 AV 检测结果按**内网 IP 缓存**，然后渲染进每个 Beacon 的状态栏：

```cna
%beacon_llist_av[$ip] = $av;          # 缓存，按 IP 键
...
set BEACON_SBAR_LEFT {
  $av_list = %beacon_llist_av[$beacon_ip];
  if ($av_list eq "") { %beacon_llist_av[$beacon_ip] = "暂未发现杀软" }
```

**为什么值得抄**：把一次性枚举变成**常驻 UI 状态**，跨滚动、跨重连都存在。
一次检测，之后每次看 Beacon 都能看到。

**对应的做法**：我们的会话列表可以带上「上次探测到的 AV / 权限等级」列。

### 4. 上传即 timestomp：把安全习惯变成原语的一部分

LSTAR 的 `AuthMaintain.cna` 里 `bupload_raw` → `btimestomp` 出现 **6 次**，
这是模式而非偶然：

```cna
bupload_raw($bid,"C:\\windows\\system32\\oci.dll",$bdata);
btimestomp($bid, "C:\\windows\\system32\\oci.dll", "C:\\windows\\regedit.exe");
```

**为什么值得抄**：`wevtutil cl "Windows PowerShell"` 紧跟在每个
`powershell-import` 之后（`RDP相关.cna` 里出现 7 次）—— 这是**调用约定**，
不是习惯。操作员不可能忘记，因为它写在原语里。

**对应的做法**：我们的 `Execute` / `Upload` 之后可以自动 timestomp，而不是
让操作员记得。

### 5. 按目标实际状态自适应

LSTAR 的计划任务对话框**根据当前用户**决定任务名和可用参数：

```cna
if($user in @("Administrator","SYSTEM","LOCAL SERVICE")){
    $is_admin = 1;
    $taskname = "\\Microsoft\\Windows\\Wininet\\UserCache_".ticks();
    warn($user);
}else{
    $is_admin = 0;
    $taskname = "\\Explorer\\Public\\temp_".ticks();
    warn($user);
}
```

梼杌则在架构/版本不符时**明确拒绝**而不是失败：

```cna
if (!-is64 $bid) { berror($bid, "cve-2020-0796 exploit is x64 only"); return; }
$winbuild = binfo($bid, "build");
if ($winbuild != 18362 && $winbuild != 18363) { berror($bid, "This exploit only supports Windows 10 versions 1903 - 1909"); return; }
```

**这和我们 sliverreshine 里已经做的方向一致**（`validateBuildRequest`、
`shellPathFor`），但它们把它用在了**运行时**而不只是构建时。

---

## 二、Ladon 的独有思路：程序集分片

Ladon 的 CS 插件只有一个 337 KB 的 `Ladon.exe`，但按功能分片成 16 个 `.dat`，
每个都是**完整的 .NET PE**：

```
Ladon20.dat    382,464   ← RemoteExec / Elevate 类
LadonInfo.dat  152,576   ← GetInfo 类
LadonPoc.dat   248,320   ← 漏洞检测类
ChromePwd.dat  321,024   ← 浏览器凭据
```

调用时按命令名**路由到不同分片**：

```cna
if ($2 eq "GetIP" || $2 eq "Whoami" || $2 eq "GetInfo" || ...){
	bexecute_assembly!($1, script_resource("res/LadonInfo.dat"), ...);
}else if ($2 eq "ChromePwd" || ...){
	bexecute_assembly!($1, script_resource("res/ChromePwd.dat"),"Start pwd");
}else{
	bexecute_assembly!($1, script_resource("Ladon.exe"), ...);
}
```

**为什么这么做**：`execute-assembly` 需要把整个程序集送进内存。1.8 MB 的主程序
在内存紧张的目标上会失败；按需加载 150 KB 的分片则成功率高得多。

**值得学的点**：**按使用频率而不是按代码结构拆分**。常用功能留在主程序集，
低频大模块单独分片。

---

## 三、必须警惕的：这些插件普遍不完整

这是本次调研**最有价值的发现**，也是我唯一建议立刻用在自己项目上的东西。

### 我逐个核对了 `script_resource` 引用

| 插件 | 引用总数 | 缺失 | 后果 |
|---|---|---|---|
| 谢公子 | 68 | **22** | `dll/` 目录**完全空**，8 个 DLL 漏洞利用全废 |
| OLa | 104 | **14** | WMI 事件订阅持久化、RID 劫持、LsassDump 全部静默失效 |

### 谢公子的缺失清单（部分）

```
❌ dll/cve-2014-4113.x64.dll      ❌ exe/lazagne.exe
❌ dll/JuicyPotato.x64.dll        ❌ exe/SafetyKatz.exe
❌ script/RdpThief_x64.tmp        ❌ exe/Watson.exe
```

**讽刺的是**：`script/RdpThief_x64.tmp` 正是某个「最值得学的设计」所依赖的文件 ——
那个 `on heartbeat_5s` 自动注入 `mstsc.exe` 的机制，**因为文件不存在而完全不能用**。

### 这教给我们什么

**一个在 `script_load` 时校验所有 `script_resource` 存在性的检查，本可以同时
发现这两个插件的问题。**

我们 `sliverreshine` 已经吃过同类亏：

- `escalateForMimikatz` 静默返回 `""` → 操作员以为是权限问题
- `waitForPort` 看到**别人的**监听器就报「就绪」
- DNS 监听器丢弃端口，永远绑 53，而 API 返回 `{"success":true}`
- UI 起的 HTTP 监听器没有 `website`，stage 永远 404

**全是同一个模式：某个东西没生效，而所有环节都报告成功。**

这些插件把同一个模式犯在了资源加载上。

---

## 四、它们写得不好的地方（不要学）

| 问题 | 实例 |
|---|---|
| **硬编码环境** | LSTAR `rdpclear` 里写死操作员 IP `192.168.93.140` |
| **静默覆盖** | LSTAR 的 AV 表 569 条里有 **43 个重复键**（去重后 526），后写覆盖先写 |
| **语法错误未发现** | 同一张表 `"360safe.exe": =>` 多一个冒号 |
| **复制粘贴残留** | 梼杌「关闭 Fscan」实际执行 `taskkill /im modify.exe` |
| **函数重复定义** | 梼杌 `sub WMIHACKER_cmd` 定义了两次，`shell` 静默变成 `cmd` |
| **注册错名字** | 梼杌用 `beacon_command_register("lazagne")` 注册 InternalMonologue |
| **拼写错误** | LSTAR 调用 `dialog_descrption()`（正确是 `description`），描述从不显示 |
| **未定义变量** | LSTAR `sharpwmi_run` 用了未定义的 `$port`，拼出畸形命令 |

**共同点：没有测试，也没有 CI。** 这些错误任何一个单元测试都能抓到。

对照：`sliverreshine` 现在有 `go test -race`、vitest、183 路由扫描、载荷回连实测。
**这个差距比任何功能差异都重要。**

---

## 五、对我们 sliverreshine 的具体建议

按性价比排序：

| # | 建议 | 来源 | 成本 |
|---|---|---|---|
| 1 | **统一输出解析的「哨兵结束标记」** —— 替代现在的超时判定 | LSTAR | 小 |
| 2 | **会话列表增加 AV / 权限等级缓存列** | LSTAR 状态栏 | 中 |
| 3 | **`Upload` 后自动 timestomp**（做成原语的一部分） | LSTAR | 小 |
| 4 | **能力注册式而非路由枚举式** —— 前端从后端拉能力清单 | 梼杌反射分发 | 大 |
| 5 | **`/api/health/resources` 自检**：启动时校验所有内嵌资源存在且可用 | 两者缺失清单 | 小 |

第 5 条最应该先做 —— 它直接防住我们反复犯的那类问题。

---

## 六、我不建议借鉴的

| 项 | 原因 |
|---|---|
| 照搬 174 个扫描模块 | 我们的定位是「自包含控制台」，不是「扫描器合集」。Ladon 那种规模需要持续维护 |
| 反射式 DLL 注入的具体实现 | 与 Sliver 的 implant 架构不兼容，且需要重写 implant |
| CS 特有的 `beacon_output` / `BEACON_SBAR_LEFT` | 那是 CS 的 API，我们要在**控制台侧**实现等价物 |
| 分片程序集 | 我们的载荷是 Go 静态编译，没有 CLR 加载问题；而且载荷大小主要来自内嵌工具链 |

---

## 附：调研方法说明

- **只读**：没有任何文件被修改，没有任何二进制被执行
- **交叉验证**：子代理报告的关键结论我都自己复核过。两处与报告不符 ——
  AV 表实际 **569 条**（报告称约 1000），`main.cna` 的 16 个引用**全部存在**
  （缺失的 22 个在其它 15 个 `.cna` 里）
- **重复键与语法错误是我本轮新发现的**，不在子代理报告里
