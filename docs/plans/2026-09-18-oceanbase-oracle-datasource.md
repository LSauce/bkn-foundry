# OceanBase Oracle 数据源接入设计

## 1. 背景

VEGA 当前具备 MySQL、MariaDB、PostgreSQL、OpenSearch 等已注册的本地连接器。仓库中还存在一套未注册的 Oracle 连接器实现，覆盖连接测试、Schema 校验、表/视图发现、字段/索引/外键元数据读取和 Raw Query，但它尚未进入本地连接器工厂和统一查询支持列表。

本需求新增 **OceanBase Oracle 兼容模式**数据源，使用户能够在 VEGA 中注册 OceanBase Oracle 租户，发现关系表资源，并通过现有数据访问链路读取数据。

OceanBase Oracle 模式与原生 Oracle 在协议、系统视图、数据类型和 SQL 方言上高度兼容，但不能仅凭兼容声明直接复用并上线。驱动握手、租户用户名格式、分页语法、系统视图字段和特殊类型必须在目标 OceanBase 版本上验证。

## 2. 目标与非目标

### 2.1 目标

- 新增独立连接器类型 `oceanbase_oracle`，在产品和接口中与原生 `oracle` 明确区分。
- 支持创建、更新、删除和连接测试 OceanBase Oracle Catalog。
- 支持按配置的 Schema 发现表、视图和物化视图。
- 支持读取表字段、注释、索引、主键/唯一键和外键等元数据。
- 支持资源预览、Raw Query 及 VEGA 已有的只读数据访问能力。
- 密码沿用现有敏感字段加密、脱敏和权限控制机制。
- 保持现有 Catalog、Resource 和 Connector Type API 结构兼容，不新增数据源专属 API。

### 2.2 非目标

- 不支持 OceanBase MySQL 模式；该模式应使用 MySQL/MariaDB 连接器或另立需求。
- 不提供 DDL、DML、存储过程、PL/SQL、事务写入和数据库管理能力。
- 不保证兼容所有 Oracle 专有对象，例如 DB Link、同义词、Package、Sequence、Object Type。
- 首期不处理 OceanBase 原生分区、租户、Zone、副本等运维元数据。
- 不在本次需求中统一重构所有关系型数据库连接器。

## 3. 类型与配置模型

### 3.1 连接器标识

新增常量：

```text
type: oceanbase_oracle
name: oceanbase_oracle
mode: local
category: table
```

不直接复用 `oracle` 类型，原因如下：

- 用户需要明确选择 OceanBase Oracle 租户，而不是原生 Oracle 实例。
- 两者后续可能采用不同驱动、连接参数、版本矩阵和兼容修正。
- 独立类型便于灰度启用、能力声明、验收测试和问题定位。
- 避免为了 OceanBase 兼容而改变原生 Oracle 连接器行为。

实现层可以抽取并复用 Oracle 的元数据查询和类型映射，但产品类型与运行时注册保持独立。

### 3.2 连接配置

建议首期配置字段：

| 字段 | 类型 | 必填 | 敏感 | 说明 |
| --- | --- | --- | --- | --- |
| `host` | string | 是 | 否 | OceanBase 服务地址或 OBProxy 地址 |
| `port` | integer | 是 | 否 | SQL 服务端口，范围 1–65535 |
| `service_name` | string | 是 | 否 | Oracle 兼容连接所需 Service Name；具体取值以部署配置为准 |
| `username` | string | 是 | 否 | 登录用户名；允许携带 OceanBase 租户/集群限定格式 |
| `password` | string | 是 | 是 | 登录密码，持久化时加密，响应中不回显明文 |
| `schemas` | array<string> | 否 | 否 | 限定允许发现的 Schema；为空时按账号实际可见范围发现 |
| `options` | object | 否 | 视子字段而定 | 驱动连接参数，例如超时、加密和会话参数 |

示例：

```json
{
  "host": "obproxy.example.internal",
  "port": 2883,
  "service_name": "ORACLE_TENANT",
  "username": "vega_reader@oracle_tenant#cluster_name",
  "password": "******",
  "schemas": ["APP"],
  "options": {
    "connect_timeout": "10s"
  }
}
```

示例中的端口、Service Name 和用户名格式仅用于说明字段结构，不能作为部署默认值。实际格式需要根据直连 OceanBase Server 或 OBProxy 的部署方式确认。

### 3.3 最小权限

连接账号应使用只读最小权限。至少需要：

- 建立会话；
- 查询目标 Schema 下的表和视图；
- 查询目标对象的数据；
- 读取发现和元数据解析所需的兼容系统视图。

实现前需在目标版本验证 `ALL_USERS`、`ALL_OBJECTS`、`ALL_TABLES`、`ALL_TAB_COMMENTS`、`ALL_TAB_COLUMNS`、`ALL_COL_COMMENTS`、`ALL_INDEXES`、`ALL_IND_COLUMNS` 及约束相关视图的可用性和字段兼容性。若普通只读账号无法访问某个 `ALL_*` 视图，应优先切换到权限更窄的 `USER_*` 视图或调整查询策略，而不是要求 DBA 级权限。

## 4. 总体设计

```text
Connector Type(oceanbase_oracle)
        │
        ▼
Catalog 配置与密文凭据
        │
        ▼
OceanBaseOracleConnector
   ├── 连接/健康检查
   ├── Schema 范围校验
   ├── 表与视图发现
   ├── 元数据读取
   └── 只读 SQL 执行
        │
        ▼
OceanBase Oracle 租户 / OBProxy
```

### 4.1 代码组织

建议新增：

```text
server/logics/connector/local/table/oceanbaseoracle/
├── oceanbase_oracle.go
├── oceanbase_oracle_test.go
└── type_mapping.go
```

同时将 Oracle 与 OceanBase Oracle 的公共逻辑收敛为内部复用组件，候选范围包括：

- Oracle 风格标识符和 Schema 大小写处理；
- `ALL_*` 元数据查询及结果转换；
- Oracle 类型到 VEGA 类型的基础映射；
- `OFFSET ... FETCH NEXT ...` 分页与 Count SQL 构造。

公共组件不得把驱动选择、连接串格式、连接器类型和兼容差异隐藏为条件分支。OceanBase 特有行为保留在 `oceanbaseoracle` 包中，以便独立测试和演进。

### 4.2 注册与启用

需要完成以下注册链路：

1. 在连接器类型常量中增加 `oceanbase_oracle`。
2. 在本地连接器工厂中注册 `OceanBaseOracleConnector`。
3. 通过现有 Connector Type 管理能力登记类型元数据并控制 `enabled`。
4. 连接器详情接口返回运行时 `field_config`。
5. 若统一查询链路通过完整验证，将其加入查询支持列表；未通过前只允许注册、连接测试和发现，不得宣称支持查询。

连接器二进制可用性与数据库登记状态继续分离：当前二进制缺少实现时，服务应报告 `available=false`，不能因历史登记记录导致启动失败。

## 5. 连接与安全

### 5.1 驱动验证门禁

现有 Oracle 代码使用 `go-ora/v2`。OceanBase Oracle 模式是否可直接使用该驱动必须先做最小兼容性验证，至少覆盖：

- 直连 OceanBase Server；
- 通过 OBProxy 连接；
- 含租户/集群限定的用户名；
- `PingContext`、参数绑定、查询取消和连接关闭；
- 密码或用户名含 URL 特殊字符；
- TLS/加密连接（若目标环境要求）。

若 `go-ora/v2` 无法稳定连接目标 OceanBase 版本，应更换为经 OceanBase 官方兼容矩阵确认的 Go 驱动。驱动选择结论必须由真实环境测试支撑，不能只依赖原生 Oracle 单元测试。

### 5.2 连接串构造

连接串必须通过驱动提供的构造器或 `net/url` 安全编码用户名、密码、主机和参数，禁止直接字符串拼接明文凭据。`options` 只允许白名单参数，未知参数返回配置错误，避免任意驱动参数改变安全语义。

日志和错误响应中不得输出完整连接串、密码或其他敏感参数。

### 5.3 连接池与超时

沿用 VEGA 的请求上下文取消机制，并为连接建立、连接测试和查询设置上限。连接池参数使用平台统一默认值；如需开放配置，应明确最小值、最大值和默认值，不直接透传任意数值。

## 6. 资源发现与元数据

### 6.1 Schema 范围

- 配置了 `schemas`：仅发现配置范围内且账号有权访问的对象；不存在或不可访问的 Schema 在连接测试阶段返回明确错误。
- 未配置 `schemas`：发现账号可见的业务 Schema，并过滤系统 Schema。
- Schema 比较兼容 Oracle 默认大写规则，同时保留系统视图返回的真实名称。
- 所有对象查询必须同时使用 `OWNER/SCHEMA + OBJECT_NAME` 定位，不能只按表名查询。

系统 Schema 列表不能直接照搬原生 Oracle 常量。应根据目标 OceanBase 版本建立最小过滤集合，并通过配置或版本化代码维护。

### 6.2 对象范围

首期发现：

- TABLE；
- VIEW；
- MATERIALIZED VIEW（仅在目标版本系统视图与查询行为验证通过后启用）。

同义词、临时表、外部表等对象首期不纳入。无法读取的对象应记录可定位的失败原因，不应导致整个 Catalog 的发现结果全部丢失。

### 6.3 元数据映射

每个资源至少返回：

- Schema、对象名、对象类型、注释；
- 字段名、原生类型、长度、精度、小数位、可空性、默认值、注释和顺序；
- 主键、唯一索引、普通索引及字段顺序；
- 外键及引用对象；
- 可获得时返回统计行数和最后分析时间，并明确它们是统计值而非实时值。

元数据查询必须适配 OceanBase Oracle 系统视图的真实字段。缺少非关键统计字段时应降级为空值，缺少对象名、字段名、原生类型等关键字段时才判定该对象发现失败。

## 7. 类型映射

首期以 OceanBase Oracle 模式实际支持的数据类型为准，基础映射建议如下：

| OceanBase Oracle 类型 | VEGA 类型 | 备注 |
| --- | --- | --- |
| `NUMBER(p,0)` | `integer` 或 `decimal` | 根据精度和小数位判定，不能一律映射为 decimal |
| `NUMBER(p,s)` | `decimal` | 保留精度与小数位元数据 |
| `BINARY_FLOAT`、`BINARY_DOUBLE`、`FLOAT` | `float` | 需验证驱动返回类型 |
| `CHAR`、`NCHAR`、`VARCHAR2`、`NVARCHAR2` | `string` | 保留字符长度与字符集信息 |
| `CLOB`、`NCLOB` | `text` | 预览时需限制单值大小 |
| `RAW`、`BLOB` | `binary` | API 输出编码策略沿用平台约定 |
| `DATE` | `datetime` | Oracle DATE 含日期和时间 |
| `TIMESTAMP` 及带时区变体 | `datetime` | 明确时区归一化规则 |
| `INTERVAL YEAR TO MONTH`、`INTERVAL DAY TO SECOND` | 待定 | 验证后映射或标记 unsupported |
| 未识别类型 | `unsupported` | 发现保留原生类型，读取前返回明确能力错误 |

类型映射函数必须对驱动返回的类型名做大小写和空白规范化。`NUMBER` 的目标类型需要结合 `DATA_PRECISION` 与 `DATA_SCALE` 判断，修正现有 Oracle 基础映射只按类型名处理的不足。

## 8. 查询能力

### 8.1 只读约束

仅允许单条只读查询。SQL 校验必须拒绝：

- INSERT、UPDATE、DELETE、MERGE；
- CREATE、ALTER、DROP、TRUNCATE；
- GRANT、REVOKE；
- 匿名块、存储过程调用和多语句输入；
- 通过注释、CTE 或嵌套语句绕过只读检测的输入。

不能只依靠字符串前缀判断，应使用支持 Oracle 方言的 SQL 解析结果实施校验。

### 8.2 SQL 方言

当前 SQLGlot 方言映射未包含 Oracle。需要增加：

```text
oceanbase_oracle -> oracle
```

并验证表名提取、只读检查和方言转写。对于 SQLGlot 与 OceanBase Oracle 存在差异的语法，首期应返回“不支持的 SQL 语法”，不做静默改写。

### 8.3 分页与计数

候选分页语法：

```sql
SELECT *
FROM (<validated_query>) _raw_query_page
OFFSET :offset ROWS FETCH NEXT :limit ROWS ONLY
```

必须在目标 OceanBase 版本验证。分页参数应使用安全整数参数或在完成范围校验后生成，不接受用户直接注入分页片段。

总数查询使用子查询包装。若原 SQL 含不兼容的 ORDER BY、FOR UPDATE 或其他尾部子句，应在校验阶段拒绝或做 AST 级处理，不能简单拼接后交给数据库报错。

### 8.4 标识符

- 默认未加引号标识符按 Oracle 规则转为大写。
- 加双引号的大小写敏感标识符必须保持原值。
- 资源标识不能只用字符串 `schema.table` 再按最后一个点拆分；需要使用结构化 Schema/Object 字段或可逆编码，以支持带点号、引号等特殊字符的合法标识符。

## 9. API 与文档影响

原则上复用现有接口：

- Connector Type：登记和查询 `oceanbase_oracle` 及字段定义；
- Catalog：创建、更新、连接测试和健康检查；
- Discovery Task：发现 OceanBase Oracle 资源；
- Resource / Resource Data：查看元数据和读取数据；
- Raw Query：执行受控只读 SQL。

实施时需要同步：

- Connector Type、Catalog、Raw Query、Resource Data 的 OpenAPI 示例和枚举说明；
- 中英文数据源用户手册；
- 部署依赖或驱动说明；
- 支持矩阵，明确已验证的 OceanBase 版本、连接方式和已知限制。

如果没有新增字段或接口，不提升 API 主版本；新增的连接器类型属于向后兼容扩展。

## 10. 测试方案

### 10.1 单元测试

- 配置解码、必填校验、端口边界和敏感字段声明；
- 用户名、密码和连接参数的安全编码；
- Schema 范围及大小写处理；
- 表、视图、字段、索引、约束元数据转换；
- `NUMBER` 精度/小数位及特殊类型映射；
- SQLGlot 方言映射、只读 SQL 接受与写 SQL 拒绝；
- 分页和 Count SQL 构造；
- 工厂注册、enabled/available 状态及查询支持列表；
- 查询取消、Rows/DB 关闭和下游错误传播。

单元测试使用 mock/sqlmock，不依赖真实 OceanBase。

### 10.2 集成测试

使用专用 OceanBase Oracle 测试租户，覆盖：

- 直连与 OBProxy（项目支持哪种就至少验收哪种）；
- 正确/错误密码、错误租户、网络超时；
- 无 Schema 限定、单 Schema、多 Schema和无权访问 Schema；
- 普通表、视图、物化视图及同名跨 Schema 表；
- 主键、组合索引、外键、注释和空元数据；
- 常用标量类型、LOB、时间/时区、精确数值和不支持类型；
- 数据预览、分页、总数、Raw Query 和取消请求；
- 账号只有最小只读权限时完成连接测试、发现和查询。

### 10.3 验收标准

- Connector Type 列表和详情可识别 `oceanbase_oracle`，且 `available/enabled/field_config` 正确。
- 使用合法配置可通过连接测试；非法配置返回稳定、无敏感信息的错误。
- 配置的 Schema 范围得到严格执行，不泄露范围外资源。
- 发现结果中的表、字段、索引和外键与数据库实际定义一致。
- 支持类型的数据预览和 Raw Query 返回正确值、字段类型、分页结果和总数。
- 写 SQL 和多语句输入被 VEGA 拒绝，数据库中无副作用。
- 中断请求后查询能够取消，连接和结果集无泄漏。
- 在声明支持的 OceanBase 版本与连接拓扑上完成集成测试。
- `make lint`、`make test` 通过；具备环境时 OceanBase 专项集成/验收测试通过。

## 11. 实施拆分

### 阶段一：兼容性 Spike

- 确认目标 OceanBase 版本、直连/OBProxy 拓扑和账号格式。
- 验证 Go 驱动、连接串、参数绑定和取消行为。
- 验证系统视图、分页语法和核心数据类型。
- 产出明确的驱动选择与兼容矩阵。

失败条件：无法用可接受的 Go 驱动稳定连接，或最小权限账号无法完成必要元数据读取。此时停止进入产品化实现，先确定驱动或权限替代方案。

### 阶段二：连接器与发现

- 新增独立类型、配置校验、本地工厂注册和字段定义。
- 复用/抽取 Oracle 风格元数据逻辑并适配 OceanBase 差异。
- 完成连接测试、Schema 限定、资源发现及元数据读取。

### 阶段三：查询接入

- 增加 SQLGlot Oracle 方言映射和只读校验。
- 完成预览、Raw Query、分页、计数和结果类型转换。
- 通过测试后加入统一查询支持列表。

### 阶段四：文档与验收

- 更新 OpenAPI 和中英文用户手册。
- 建立 OceanBase Oracle 集成/验收测试夹具。
- 在支持矩阵指定环境执行全链路验收并记录限制。

## 12. 风险与待确认项

| 风险/待确认项 | 影响 | 处理建议 |
| --- | --- | --- |
| 目标 OceanBase 版本未确定 | 驱动、系统视图和 SQL 能力无法定版 | 实现前固定最低和最高验证版本 |
| 直连还是 OBProxy 未确定 | 端口、用户名和连接行为不同 | 两者分别验证，产品文档明确支持范围 |
| `go-ora/v2` 兼容性未知 | 可能无法连接或存在类型读取差异 | Spike 阶段用真实环境验证并保留替换驱动方案 |
| Oracle 现有连接器尚未注册 | 复用代码不等于已有成熟能力 | 对公共逻辑补测试，OceanBase 独立注册和验收 |
| 系统视图兼容差异 | 发现失败或元数据不完整 | 按 OceanBase 版本验证查询，不盲目照搬 Oracle SQL |
| 特殊类型与 LOB | 返回值错误、内存占用过大 | 建立类型矩阵、单值大小限制和不支持类型错误 |
| 大小写敏感标识符 | 资源无法定位 | 使用结构化标识符并覆盖引号标识符测试 |
| Raw Query 方言校验缺失 | 查询不可用或存在写入风险 | 通过 SQLGlot Oracle 方言和负向安全测试后再开放 |

实施前需要业务或环境负责人确认：

1. 首批支持的 OceanBase 版本及 Oracle 兼容级别；
2. 使用直连 OceanBase Server、OBProxy，还是两者都支持；
3. 可用于集成测试的租户和最小权限账号；
4. 首期是否必须支持物化视图、LOB、带时区时间和跨 Schema 外键；
5. 产品上是否同时计划开放原生 Oracle；若是，应复用公共能力但分别维护支持矩阵。
