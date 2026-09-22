# 2026-09-22 睡眠软件恢复增量

按最新用户授权加入一次有边界的主机侧 USBDeviceReEnumerate(0)，限定在已关闭接收会话的睡眠恢复检查点。详见 SLEEP_SOFTWARE_RECOVERY.md；无 root/SIP/模块配置改变，成功必须经网络和库存重新核验。

# 2026-09-21 Development RC 实施收敛（最新）

依据用户在接受 `DJISMS_V1_CURRENT_STATUS.md` 后的连续开发授权，V1 当前实施契约以 [已实现协议](../../product/djisms-core/contracts/README.md) 为准。以下 2026-09-18 设计稿作为历史保留；其中“仅 ME1”“尚未实现协议”“后续申请每条确认”等阶段限制已被连续工作包及后续用户授权覆盖。

当前 V1 明确采用：App 私有父子管道、有界 JSON schema、单设备拒绝歧义策略、Core 串行接收/逐条自动清理、持久通知 outbox、按需查询历史/状态。未实现也不声称提供：XPC、设备选择器、外部 purge preview/token API、cursor 事件重放。等效安全边界是 Core 独占连接、内部新鲜 receipt/index/PDU 核对和一次性 durable delete intent；UI 无删除索引或 AT 输入能力。

新增连接恢复仅针对有持久证据的完整空闲接收边界；未知删除、归档/库存不一致、partial frame 与未完成命令仍停止。睡眠/唤醒仅改变生命周期，不放宽删除核验。详见上述协议与测试。Developer ID、公证、其他干净机器和最低系统验收仍是正式发行条件；缺少这些条件不冒充已验证，但不阻塞 Development RC 软件开发。

用户已取消新的长短信实机测试；已有分段证据保留，不把取消项标作当前版本实时 PASS。最终物理拔插/睡眠/iPhone 回归仍按最终同一版本记录真实结果。

---

# DJISMS V1 可分发产品契约 — 2026-09-18 冻结

这是产品需求与后续实施边界，不声明当前工程 Gate 已成为可发行 App。本修订优先于此前“无复杂 GUI”的概括：V1 包含菜单栏、原生通知、历史查看/搜索、设备状态和设置。现有 raw/journal/SQLite 永久保存与逐条删除契约继续适用。

## 交付与职责

目标是其他 Apple Silicon Mac 用户可独立安装使用的 `DJISMS.app`。普通用户不运行 Terminal、Codex、Homebrew、Go/Python，不需要 root/sudo、降低 SIP、安装自定义 modem driver 或修改 DJI 固件。开发工具只在构建机使用；发布包包含全部必要运行组件。最低 macOS 版本须由真实兼容性验证决定，不能假定当前开发机代表所有机器。

- `product/djisms-core/`：独立编译的原生用户态核心；负责硬件审核匹配、独占 IF2、USB/AT白名单状态机、PDU事实/解码/拼接、永久存储、SQLite索引、通知outbox、逐条purge授权与reconciliation。计划复用已审核Go/C部分，将Python归档参考实现迁移为自包含原生实现；迁移必须用相同故障注入与档案兼容性测试证明，不能依赖用户Python。
- `product/DJISMS.app/`：Swift/AppKit或SwiftUI原生菜单栏App；显示状态、查看搜索历史、接收核心事件、提交用户意图并显示原生Notification Center。App不得链接原始USB写入API、构造AT文本、直接修改archive/journal/SQLite生命周期。关闭通知不影响档案。
- `product/packaging/`：应用包组装、签名、公证、staple及干净机器验收。目录中的README不是已构建应用。

建议核心作为App内用户态辅助进程，由App启动/停止，使用仅本App可连接的私有IPC。IPC的具体XPC桥接实现在后续平台原型验证；不得因此默认安装LaunchAgent、daemon、登录项或特权helper。核心缺失或协议版本不兼容时App仅显示故障，不回退到直接AT。

## API边界

API协议版本1，强类型请求/响应、有界输入、request ID、核心生成的device session ID；UI不能自选路径、索引、原始AT或USB endpoint。

| API意图 | 输入边界 | 核心责任 |
|---|---|---|
| ListDevices / GetStatus | 无原始硬件坐标 | 返回可支持/不支持/歧义/连接状态，隐藏底层操作能力 |
| SelectDevice | 核心枚举出的device ID | 明确选择一台，取得新session；歧义禁止自动purge |
| StartReceive / StopReceive | 当前session token | 独占、原始事实先持久化、可停止、安全关闭 |
| ListMessages / SearchMessages / GetMessage | 分页、有限长度查询、message ID | 只读索引，不执行AT、不暴露永久原始文件写入 |
| GetPurgePreview | receipt ID集合 | 显示可归档资格和hold原因，返回过期/会话绑定preview ID |
| ConfirmIndividualPurge | 核心preview ID和显式用户确认 | 每条重新读取storage/index/PDU并校验，任何歧义停止，不允许bulk/raw AT |
| UpdatePreferences | 经schema审核的有限设置键 | 通知预览等产品设置；不能放宽原始保存/删除安全契约 |
| SubscribeEvents | 有界cursor | 状态、archive完成、消息可读、通知请求、purge结果、故障；可重复消费，不靠通知保存数据 |

此API是后续产品契约，不给现有Gate新增执行入口。此次ME1授权不可转换为持久的App purge授权。核心自己验证所有前提；UI显示“可删除”不能成为授权凭证。断连、重启、session变化使preview及确认token失效。

## 状态机与拔插

设备：Absent → Enumerating → Unsupported / Ambiguous / Ready → Receiving；异常进入StoppedWithError。拔出立即取消session、停止OUT并关闭句柄。重新插入重新审核identity/完整descriptor、重新发现网络关联、重建session，不延用旧index/删除许可。不自动重发未确认的删除。

单条事实：Acquired → RawDurable → JournalDurable → Indexed → Verified → Reconciled → Eligible；随后独立的用户确认与新读比较 → DeleteIntentDurable → DeleteAttempt → DeletedConfirmed / Unknown / Held。任何归档失败禁止删除；timeout/索引复用/其他短信变化/reconciliation异常立即停止。同一设备每次只执行一条删除，禁止delflag/bulk。Unknown需后续独立只读调查，不自动重发。

多设备：枚举全部候选，按审核的VID/PID/bcdDevice、接口/端点及完整descriptor contract筛选；VID/PID相同不足以放行。序列号只有经可信性/唯一性验证后才用于长期标识；没有稳定唯一标识时采用会话标识并要求明确选择，不能用当前USB location当永久身份。每设备独立session和inventory；全局归档写入串行化。若无法区分同型设备或证明网络接口归属，停止涉及该设备的purge，UI显示可操作的状态。

## 可移植性与存储

运行数据全部基于当前用户的Application Support API解析到 `~/Library/Application Support/DJISMS/`。不拼接用户名、不固定UID，不使用Desktop工程目录作为运行依赖。raw/journal/SQLite格式继续可验证、可恢复；迁移保留原始文件并经用户数据兼容性测试。

产品硬件profile可以包含审核后的硬件常量，但不得包含Eric的USB location、registry ID、en6名称、SIM或运营商。USB location仅为当次枚举的关联线索；network interface由设备注册树与系统网络信息动态关联。不能猜测第一个en接口，不改变系统默认路由。SIP必须保持enabled，权限使用当前实际用户验证而非UID501。

当前单条Gate为一次获准的开发机审计，固定location/en6/路径仅隔离在审计工具中。它不属于产品运行组件，不进入App资源或可执行包。

## 发行契约与验收

正式分发使用Apple Developer ID签名，包含嵌套组件的正确签名顺序、适当hardened runtime配置、notarization与staple。ad-hoc只用于开发，不能称为正式发行或Gatekeeper通过。证书/团队及开发者账户由后续发布流程提供；当前不申请证书、不上传公证、不发布。

发布前必须在干净Apple Silicon Mac验收：无开发工具安装、正常SIP、无自定义driver、签名与公证验证、首次启动权限/通知、设备插拔/睡眠恢复/多设备歧义、动态USB/网络接口映射、其他用户名、历史搜索、通知拒绝与消失、永久档案恢复、磁盘满/崩溃/index reuse/模糊删除结果等。运行时不得要求Terminal完成常规操作。归档格式和协议版本有迁移/兼容检查；二进制验收需检查个人路径/UID/location/en6/运营商硬编码泄露。

后续按核心原生化与兼容测试 → 类型化IPC与App界面 → 热插拔/多设备 → 干净机器验证 → Developer ID与公证准备实施。任何阶段不能扩大当前仅ME1一次的删除授权。
