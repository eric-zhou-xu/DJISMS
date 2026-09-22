## DJISMS V1 产品冻结（2026-09-18，最新用户范围）

按 `docs/djisms/V1_REQUIREMENTS.md` 冻结：模块是临时收件箱，Mac 的
`~/Library/Application Support/DJISMS/` 是永久事实库；所有短信统一永久本地保留，
无云/NAS/AI。允许实现并测试 raw archive、SQLite、journal、恢复、index reuse 防护、
原生通知设计和 archive-and-purge 离线模拟，并采集当前23/23真实快照生成 Dry Run。
每片 Preserve→Verify→Reconcile 才有归档删除资格；实际删除前必须新读 storage/index/PDU
hash；永久禁止 bulk delete。**本次不实际删除；第一次真实 purge 仍需用户单独明确批准。**
未来自动接入/LaunchAgent仅设计与审计，不安装任何后台常驻。既有冻结接收证据保持不变。

