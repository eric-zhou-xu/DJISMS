# 首次真实 ME1 Archive-and-Purge Gate

用户于2026-09-18明确批准仅 ME index 1 一次真实删除。该批准优先于 AGENTS.md 历史禁止删除条款，其余接口、系统、网络限制不变。

批准的 PDU 字节 SHA-256：`6d8a3fa3c24279c25e19d689d77709e31988cf291183900356539b0f146854a4`。先校验永久 raw/journal/SQLite，再采集完整23条库存、新读 CMGR=1、确认 CPMS ME23，保存新事实，再验证主机/归档，才准单次 `AT+CMGD=1`。

固定序列：CMGF?、CPMS?、CNMI?、CSMS?、CMGL=4、CPMS?、CMGR=1、CPMS?、CMGD=1、CPMS?、CMGL=4、CPMS?、CMGF?、CNMI?、CSMS?。只有 CMGD=1 删除一条；不存在 delflag 参数或任意 AT API。ETSI TS127005 §3.5.4规定索引删除，非零delflag可能扩大删除范围，本实现不提供delflag。CMGL/CMGR读取可能将未读改为已读；原始状态和PDU先保存，库存身份比较使用index+PDU hash。

Native C严格15步，每步先消耗许可再写入；Go逐项解释响应并在任何错误或250ms读写超时后停止；不重发、不换接口、不reset、不drain。删除仅在新读目标30秒内，父进程持有独占归档锁、归档及主机检查均通过且提交匹配nonce/hash/receipt的一次性许可后进行。永久approval-consumed.json禁止重复启动；执行完撤销二进制执行位。无后台服务安装。

删除后须ME22且index1消失、其他22条index/PDU不变、设置不变、IF2关闭、USB/ECM/en6/HTTPS/SIP一致；原始target档案hash不变、journal intent/attempt/result/confirmation齐备。未知结果只记录unknown并停止。历史forensic attribution INCONCLUSIVE保留，不升级为新的证明。

官方语义来源：https://www.etsi.org/deliver/etsi_ts/127000_127099/127005/17.00.00_60/ts_127005v170000p.pdf
