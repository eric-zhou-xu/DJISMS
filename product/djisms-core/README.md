# djisms-core

独立原生核心边界，按 ../../docs/djisms/DISTRIBUTABLE_APP_CONTRACT.md 实施。
现有 internal/sms* 与 tools/djisms 是迁移/测试输入，尚非可分发核心。
禁止引入开发机路径、固定UID/USB location/en6/SIM/operator。
核心独占USB/AT/档案/SQLite/生命周期/purge/reconciliation；UI只消费类型化服务。
