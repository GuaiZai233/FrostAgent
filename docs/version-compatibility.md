# v15 / v16 无损切换

## 当前保证范围

本页说明 [PR #320](https://github.com/GuaiZai233/FrostAgent/pull/320) 实现的配对版本兼容范围。[ADR-0001](adr/0001-lossless-database-forward-compatibility.md) 保留通用无损前向兼容的设计目标，当前仅部分实现；本页的配对保证不等于该 ADR 已全部兑现，也不替代该 ADR。

配对的 v15 兼容修补版核心迁移游标为 15，支持读取和保存 schema 16；本分支的核心迁移游标仍为 16。数据库 16 会原位使用，不会改写为 15。原始、未经修补的 v15 二进制不在兼容范围内。

- 端点格式会从数据库载入；旧 UI 编辑保存时保留它不认识的格式字段。
- 旧版无法调用的新协议会在发送请求前明确报错，其他旧功能继续使用。
- 补充迁移使用稳定的 module ID，并验证结构、事务回滚及重复执行。已由 v15 安装的附加列不会导致 v16 再升级失败。
- v15 和 v16 都保留 Computer Use 元数据与 token usage；ZIP 可以互相恢复。桥凭据和独立 API Key 仍不导出，恢复任务继续要求重新绑定，不能自动重放。
- 按 Maintainer 的决定，备份原样保留完整端点 URL，包括查询参数及内嵌凭据；备份提示会说明风险，请妥善保管。
- 完整性保证适用于经配对测试的 v15 修补版 / v16，不承诺任意未来版本或多进程同时写库。

## 验证依据

[跨版本 CI](../.github/workflows/version-roundtrip.yml) 将配对 v15 修补版固定为提交 [`409e50b5262ea3324f092560ee0cdcf206f93a1f`](https://github.com/GuaiZai233/FrostAgent/commit/409e50b5262ea3324f092560ee0cdcf206f93a1f)，并与被测 v16 分别编译测试二进制。[往返脚本](../scripts/test_schema_roundtrip.py)覆盖新版写入、旧版编辑保存、新版重读、两端 ZIP 导出及完整恢复。所有数据均在临时测试目录中创建。该测试范围不包含任意未知模块的载荷往返。

## 当前限制与保护

- [RequireNativeSchema](../internal/storage/compatibility.go)仍会在数据库版本高于程序支持范围时拒绝完整操作，也会在发现有数据的未知模块表时拒绝操作。[完整实例备份](../internal/backup/zip.go)和[实例删除](../internal/storage/deletion.go)仍依赖该保护。
- [ZIP 解码](../internal/backup/decode.go)只接受已知成员及合法的表情文件；未知模块成员会被拒绝，不会作为不透明载荷保存、恢复和再次导出。已知 Computer Use 元数据的保留不代表通用未知模块支持。
- [未来 schema 备份回归](../internal/backup/schema_compatibility_test.go)验证完整备份被拒绝、数据库和未知载荷未被改写；它没有证明未知模块能够无损往返。明确标示范围的局部导出仍可使用，不能当作包含未知模块的完整备份。
- 通用 opaque payload 机制、任意未知字段与 JSON 键的安全重写，以及按实际能力替代统一版本门禁，仍是 ADR 的未完成目标。在实现并验证这些能力前，保留拒绝检查以保护未知数据。

这些限制说明当前能力边界；保护性拒绝本身不是数据丢失的证据。测试通过只能证明其覆盖的范围，不能据此宣称 ADR-0001 已全部实现。
