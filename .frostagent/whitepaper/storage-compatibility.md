# 数据库版本与兼容性

SQLite 和 PostgreSQL 共用 SQL 迁移序列。`schema_meta` 中维护两个独立字段：

- `version`：实际已执行到的迁移版本，不能为了运行旧程序而调低。
- `min_compatible_version`：允许使用该数据库的最低程序 schema 版本，表示读写兼容，而不只是能够读取。

例如，`version=15, min_compatible_version=14` 表示采用本兼容协议的 v14 程序可以使用该库的已知功能，新模块数据留在原处；不会降级到 v14，也不会反向执行迁移。`version=16, min_compatible_version=15` 则拒绝 v14 程序打开。

## 打开与迁移规则

1. 新库写入当前迁移版本及兼容下限。当前程序的 `SchemaVersion` 仍为 14，不为兼容性元数据单独增加业务 schema 版本。
2. 旧库 v7–v14 没有兼容字段时，在事务内补齐，以旧版本作为初始下限，再按原顺序向上迁移。每步迁移的 DDL、数据变换、版本和兼容下限一起提交或回滚；整个多步升级不宣称是单一事务。
3. 每个新增迁移默认提高下限到自身版本。只有审核证明旧程序的读写均安全，才能显式标记 `preservesCompatibility: true`，保留先前下限；迁移不得降低已存在的下限。
4. 高版本库的声明合法且下限不超过当前程序版本时，先验证当前写入契约，再原地打开。不修改 `version`，不执行已完成的迁移，不解析或重写不认识的独立模块数据。
5. 缺失、为空、低于 v7 或高于实际版本的声明不能用于猜测未来兼容性。不满足兼容条件的库在写入兼容字段前拒绝打开。v7 之前的数据库仍不在 SQL 迁移范围内。

一个有界的历史例外是尚未有兼容字段的 Computer Use v15：仅当已知 v14 核心布局、三张 Computer Use 表的列/主键/非空/外键及已知可选表均匹配时，才回填下限 14。未知的无声明未来版本或额外表不自动放行。该回填只增加兼容元数据，不变换业务字段或 JSON。

## “非破坏性”的约定

下限是迁移作者需要维护并通过测试证明的契约，不是数据库能自动推导的结论。新增表、列或 JSON 键，不天然意味着兼容：

- 当前模型目录、MCP、表情包及部分记忆存储仍有删除再插入的写入路径。因此兼容模式保守地拒绝已知核心表的额外列、列类型/主键/非空/唯一约束变化、外键变化、未知 `CHECK` 约束，以及未知触发器。必须先让旧写入器保留新增数据，才能放宽这类变化。v14 核心表没有 `CHECK`；即使只是增加 `CHECK (enabled = 1)`，也会阻止旧程序正常创建或停用实例。PostgreSQL 从约束目录检查，SQLite 从建表语句识别约束关键字并跳过注释和引用内容；不会将字符串里的 `CHECK` 当成约束。数据库选择表 `storage_selection` 的已有检查不属于核心表检查范围，不受影响。
- PostgreSQL 核心主键和唯一约束还必须保持 `NOT DEFERRABLE`。即使键列没有变化，`DEFERRABLE` 也不能作为 `ON CONFLICT` 的冲突判定约束，`INITIALLY IMMEDIATE` 并不能消除这个限制。例如把 `counters(key)` 的主键改为可延迟约束，会使现有实例创建事务中的计数器 UPSERT 失败。启动检查读取 `pg_constraint.condeferrable`，拒绝核心表的这类变更；不重建约束、不改数据，也不把此限制施加到不受旧写入器管理的独立扩展表。
- PostgreSQL 核心表也不能增加未知排斥约束（`EXCLUDE`）。其索引不一定标记为唯一，不能依赖普通唯一索引检查；启动读取 `pg_constraint.contype = 'x'` 拒绝这类变化，包括延迟到事务提交时才检查的约束。例如按 `enabled` 区间排斥重复值会阻止正常创建实例。独立扩展表的排斥约束不受此限制。
- PostgreSQL 的核心及缓存写入列还必须保持普通列属性。启动读取 `pg_attribute.attgenerated` 与 `attidentity`，拒绝 v14 契约中不存在的生成列和 identity 列（包括 `BY DEFAULT`）。相同类型、非空和主键并不足以保证可写：`GENERATED ALWAYS` 会拒绝旧写入器提供的显式值。检查只读取属性，不转换列、不修改生成表达式或序列；独立扩展表的生成列/identity 不受此限制。
- PostgreSQL 触发器检查除保留当前 schema 的检查外，也按关系 OID 覆盖 `search_path` 实际解析到的核心表、兼容元数据表和两张 Embedding 缓存表。位于后续 schema 的写入目标也不能带未知触发器，否则普通记忆导入的缓存清理可能被阻断或改写新版模块数据。其他 schema 中被前置同名表遮蔽、或根本不在写入路径上的独立表不会仅因同名而被拒绝。检查不执行、禁用或删除触发器；未标记历史 v15 必须先通过检查才能回填。
- PostgreSQL rewrite rule 同样属于写入契约，但存储在 `pg_rewrite`，不能由触发器检查代替。对当前 schema 及上述实际写入目标，启动拒绝非 `_RETURN` 规则：`DO ALSO` 可以附加删除或改写新版模块数据，`DO INSTEAD` 可以静默替代原操作。检查与触发器共用关系 OID 范围，保留普通视图的内建 `_RETURN` 规则；不执行、禁用或重建规则，历史 v15 也须先通过检查再回填元数据。
- PostgreSQL 对当前 schema 表及上述实际写入目标启用 Row-Level Security（`pg_class.relrowsecurity`）时，也拒绝兼容启动。RLS 不仅会拒绝写入，还可能隐藏已有实例或静默过滤 scope 删除；不能因为检查账号是 superuser、拥有 `BYPASSRLS` 或暂时绕过策略就认定安全。检查只读取目录，不禁用 RLS 或重写 policy。仅保留但未启用的 policy、单独设置而未启用 RLS 的 `FORCE` 标志，以及后续 schema 中被实际写入目标遮蔽的同名表，不会被此检查误拒。
- 旧写入器省略的列必须保留兼容的默认值。当前正常写入路径中，`WriteExamplesTx` 省略已退役的 `dialogues.scene` 和 `dialogues.relation`，依赖它们的空字符串默认值。启动通过 PostgreSQL `pg_attrdef` / `pg_get_expr` 或 SQLite `pragma_table_info.dflt_value` 读取默认值；拒绝缺失、`NULL`、非空字面量及无法识别的表达式。保守识别原生空字符串字面量（允许外围括号和空白；PostgreSQL 为 `''::text`），不执行未知默认表达式以免产生副作用。其他由正常写入器显式提供的列和独立扩展表不受此默认值限制。检查不修改默认值、业务字段或版本号，未标记 v15 也须先通过再回填。
- 独立扩展表不能通过外键依赖会被旧代码重建的核心行；否则一次普通保存就可能级联删除新版数据。仅引用不可变的 `instances(id)` 的扩展表可保留，但完整实例生命周期受下节保护。`instances(name)` 虽然唯一，却会被正常实例改名更新；无论更新动作是级联、置空还是限制，都可能改变模块数据或阻断改名，因此必须拒绝，不能按整张实例表豁免。
- PostgreSQL 的外键检查包含其他 schema 对当前写入器可变表的入站依赖，不限于当前 schema 的表清单。被引用表按写入器实际解析到的关系 OID 匹配，其他 schema 中的同名独立表不会被误拒；外部同名 `memory_embeddings` 也不能冒用原生缓存关联豁免。检查包含当前 schema 引用 `search_path` 后续 schema 中可变缓存的反向情形，不能假设写入目标总在第一个 schema。无论外键使用级联、置空还是限制删除，均可能改变未知数据或阻断旧写入器，必须拒绝。该检查不枚举或重写外部业务数据，不扩大历史 v15 的表白名单；未标记 v15 也必须先通过检查才能回填兼容字段。
- 已有列的含义、合法枚举和 JSON 结构也属于契约。例如新增全局设置键或模型路由 workload 可能被旧校验拒绝；给记忆目录或合并归档的固定结构 JSON 增加字段，可能在旧程序反序列化后重写时丢失。此类变更必须提高下限，或先发布能保留未知字段的旧写入器。
- 可重建的 Embedding 派生缓存仍遵循其原有失效与重建规则，不能将新的不可丢失数据放进旧程序会清理的缓存或写入域。`memory_embedding_scopes` 与 `memory_embeddings` 同样属于可变外键目标，兼容检查拒绝扩展表对它们的依赖；仅保留向量表到 scope 表原有的五列 `ON DELETE CASCADE`、`ON UPDATE NO ACTION` 关联，避免记忆导入或保存时连带删除新版模块数据。
- PostgreSQL 已有的两张 Embedding 缓存表也要满足完整的写入布局：列集合/顺序、类型、非空、主键、唯一/外键及 CHECK/EXCLUDE/可延迟约束与生成属性。`embedding` 必须是 pgvector 扩展拥有的原生 vector 类型，按类型 OID 和扩展依赖识别，不能是同名类型或带固定维度的 `vector(n)`。两表不存在时不要求安装 pgvector，也不在启动时创建缓存；只有合法 scope 表时可留待启用 Embedding 后创建向量表。对于声明兼容的未来库，缺列、额外必填列或其他未知缓存布局在启动时拒绝，并原样保留缓存、源记忆、模块数据和元数据；不自动重建未知布局。原生版本中 `EnsureEmbeddingStorage` 对已知损坏缓存的恢复路径保持原行为。
- 数据库布局检查用于拦截可识别的矛盾，不是对任意新语义的自动证明。每个保持下限的迁移都必须测试旧程序的正常读取、保存、重启，以及新数据保持不变。

已发布且不认识兼容字段的旧二进制不会自动获得本能力，必须更新代码并重新构建。旧开发分支的迁移也不会自动维护声明；除了上述已核对的 v15 例外，不对未采用本协议的未知迁移作兼容承诺。该约定支持停机后切换版本，不表示允许多个不同版本进程同时写库。

## 兼容模式的完整实例操作

当数据库比程序更新时，普通已知功能可以继续使用，但旧程序不了解新模块的导出、脱敏、恢复和删除语义：

- 完整实例 ZIP 导出、完整实例恢复、准备删除与确认删除在修改运行状态、数据库或文件前拒绝，提示使用支持实际数据库版本的程序。
- 启动时若发现有效的未完成实例删除暂存目录，保留文件和记录并拒绝自动恢复/清理；用匹配版本完成该操作。没有暂存目录时不因这一保护阻止启动。
- 取消待删除状态、明确标注范围的单项配置导出等不宣称覆盖未知模块的操作保持可用。

实例 ZIP 是逻辑导出，本来就有敏感信息剔除等语义，不等同于整个 SQLite/PostgreSQL 的数据库备份。不能把不认识的新版 JSON 无检查地塞入旧 ZIP 来伪装成完整备份。

## 回归验证

`internal/storage/compatibility_test.go` 使用一次性 SQLite 和隔离 PostgreSQL schema，覆盖历史升级、兼容/不兼容未来版本、重复打开、原始 JSON 保留、迁移下限推进/保持/回滚，以及结构拒绝后数据不变。PostgreSQL 测试通过现有 `FROSTAGENT_TEST_POSTGRES_DSN` 开关运行；未配置时是跳过，不算通过。

`internal/storage/compatibility_embedding_test.go` 独立构造缓存表的真实复合主键和外键；PostgreSQL 使用预先启用的真实 pgvector 类型，SQLite 仅使用文本载荷测试扩展关系。它先在回滚事务内证明缓存删除会级联删除扩展数据，再验证拒绝启动且全库元数据和行值不变；正常 v15/v16 缓存布局仍可打开，大小写外键名称也不能绕过检查。PostgreSQL CI 在启用 pgvector 前先验证原生和声明兼容的未来库均能在无缓存时启动。

`internal/storage/compatibility_cross_schema_test.go` 使用独立 PostgreSQL schema 覆盖跨 schema 的核心表与缓存外键、外部同名缓存表、`search_path` 后续 schema 中的缓存目标、原始 v15 回填保护，以及无关同名表和仅引用实例 ID 的合法布局；快照同时检查当前 schema 与外部 schema 的行值和约束，确认拒绝打开不修改任何一侧。

`internal/storage/compatibility_instance_key_test.go` 通过真实 `LoadInstances` / `UpdateInstance` 证明名称外键的 CASCADE、SET NULL、NO ACTION 和 RESTRICT 会改写模块引用或阻断改名；随后验证 SQLite/PostgreSQL 在兼容启动时拒绝这些布局，且数据、约束和版本元数据不变。覆盖外部 schema 子表、历史 v15 不回填、不可变 ID 的合法引用及无关同名实例表；PostgreSQL 按实际目标 OID 与被引用的 ID 属性编号豁免，而非表名或子表列名。

`internal/storage/compatibility_deferrable_test.go` 覆盖主键、非主键唯一约束的 `INITIALLY DEFERRED` / `INITIALLY IMMEDIATE` 变化，以及原始 v15 拒绝后不回填。主键场景通过真实 `CreateInstance` 复现失败并验证整个事务回滚；非主键场景通过直接 UPSERT 探针验证约束限制。拒绝启动前后比较行、列类型和约束定义；普通不可延迟约束与独立扩展表保持可用。

`internal/storage/compatibility_exclusion_test.go` 通过真实 `CreateInstance` 复现立即及提交时的排斥冲突（SQLSTATE `23P01`），确认索引不属于普通唯一索引、失败写入及启动拒绝均不改数据。覆盖历史 v15 不回填和独立扩展约束不误拒。

`internal/storage/compatibility_trigger_test.go` 使用带特殊字符的独立 schema，覆盖后续 schema 的缓存、核心和元数据写入目标，以及历史 v15、同名遮蔽、无关 schema、无触发器的安全回退布局。回滚事务中的 SQL 写入探针分别证明行级触发器阻断操作、语句级触发器改写扩展数据（scope 删除探针对应记忆导入的 SQL，不宣称执行完整导入 API）。拒绝启动前后比较两个 schema 的数据、列、约束、触发器启用状态和函数定义。

`internal/storage/compatibility_rule_test.go` 先在回滚事务内证明 rewrite rule 附加删除、附加更新或静默跳过删除，确认这些规则没有对应的用户触发器，再验证启动拒绝。覆盖当前 schema、`search_path` 后续 schema 的缓存/元数据目标、核心表更新、历史 v15 不回填，以及普通视图、同名遮蔽和无关外部表不误拒。前后快照包含两个 schema 的数据、列、约束、触发器以及 rewrite rule 的启用状态和定义。scope SQL 探针对应记忆导入的删除操作，不宣称执行完整导入 API。

`internal/storage/compatibility_rls_test.go` 使用独立 `NOSUPERUSER NOBYPASSRLS` 登录角色，覆盖非 owner 与受 `FORCE ROW LEVEL SECURITY` 约束的 owner。真实 `LoadInstances` / `CreateInstance` 先证明实例被隐藏、创建被拒，scope SQL 探针证明删除静默影响零行；再验证当前 schema 和后续 `search_path` 写入目标被拒、历史 v15 不回填，并覆盖同名遮蔽、已禁用 policy 和未启用 RLS 的 `FORCE` 标志。管理员快照比较两个 schema 的数据、默认值、RLS 标志和 policy 定义，避免因测试账号绕过或看不到数据而误判。

`internal/storage/compatibility_defaults_test.go` 同时覆盖 SQLite 与 PostgreSQL 中两个省略列的 `NULL` / 缺失默认值、非空默认值和历史 v15 不回填；原生/带括号的空字符串、显式写入列的默认值变化与独立扩展表保持兼容。快照包含全部行和默认值定义，确认拒绝启动无副作用。`internal/service/dialogue/compatibility_postgres_test.go` 另通过真实 `SaveExamplesSQL` 复现两个列各自引发的 SQLSTATE `23502`，确认失败保存回滚并保留原有示例，再检查启动拒绝。PostgreSQL CI 显式执行该真实写入路径测试。

`internal/storage/compatibility_generated_test.go` 通过真实 `CreateInstance` 复现生成列/ALWAYS identity 引发的 SQLSTATE `428C9`，覆盖 BY DEFAULT 属性拒绝、历史 v15 不回填、普通列与独立扩展表的合法对照；对话测试另通过真实 `SaveExamplesSQL` 复现 `position` 的 ALWAYS identity 写入失败。快照比较行、默认值、生成/identity 属性、类型和兼容元数据。

`internal/storage/compatibility_cache_layout_test.go` 覆盖两张缓存表的额外必填列、缺列、错误类型、固定向量维度、约束和生成属性变化，以及历史 v15、实际 search_path 目标、同名遮蔽与合法可选布局。`internal/memory/embedding_compatibility_test.go` 通过真实 `Embeddings.publish` 分别复现两张表额外必填列导致的 SQLSTATE `23502`，验证事务回滚及启动拒绝不改源记忆、模块数据、缓存和兼容元数据；合法未来布局还能重启、发布并执行向量查询。

PostgreSQL 迁移 CI 为真实数据库测试生成并上传独立覆盖率报告，与普通 Go 测试报告合并；否则只在 PostgreSQL 执行的兼容检查会被错误计为未覆盖。该报告不替代普通测试，也不降低覆盖率门槛。

`internal/backup/schema_compatibility_test.go` 与 `internal/instance/schema_compatibility_test.go` 覆盖完整操作拒绝时无副作用、删除暂存文件保留，以及仍可用的部分导出。所有用例使用临时数据库，不访问部署中的真实数据。
