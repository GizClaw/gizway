# Development-stage breaking refactors

## 1. 当前阶段

GizWay/GizPay 目前处于开发阶段，尚未上线，没有生产存量数据库、外部兼容承诺或必须持续服务的
旧客户端。

因此，每一个 Milestone 都是一次 **breaking refactor**。新 Milestone 可以直接重写前一阶段的
数据库、API、配置、代码结构、术语和测试。实现完成后，仓库只保留当前 Milestone 规定的一套有效
实现，不为旧设计保留运行时兼容。

该规则适用于 GizPay、各区域 GizWay、嵌入式 Bifrost adapter、ZITADEL 集成、PowerSync 集成、
OpenAPI、数据库和测试。

## 2. 唯一当前契约

每个 Milestone 必须明确自己的目标、Breaking Changes 和验收条件。进入该 Milestone 的实现后：

- 当前 Milestone 文档是新增或改变范围的唯一规范；
- 前一 Milestone 文档只记录当时设计，不反向修改成新设计；
- 当前代码、OpenAPI、Schema、Config 和测试必须全部收敛到新规范；
- 同一个概念不能同时保留新旧两套名称、路径、字段或实现；
- 旧测试不能为了验证已经删除的契约而继续保留。

如果前一 Milestone 与当前 Milestone 冲突，以当前 Milestone 明确声明的 breaking change 为准。

## 3. 数据库

开发阶段不做生产数据迁移：

- 不编写旧表到新表的数据迁移工具；
- 不保留兼容表、兼容 View、Trigger 桥接或双写；
- 不执行 shadow database、历史数据 reconciliation 或旧新 Schema 切换；
- 不维护为了升级存量环境而存在的 migration chain；
- 直接修改 GizPay/GizWay 各自唯一的初始 Schema；
- 测试和开发数据库删除后，从空库重新初始化并重新注入 Seed。

数据库回退方式是停止当前开发版本、修复代码和 Schema、重新初始化测试环境，不是把服务切回旧
数据结构。

## 4. API 与协议

Milestone 修改 API 时执行完整 breaking replacement：

- 直接删除旧 path、method、operation ID、Schema、parameter、JSON field 和 error code；
- 不提供旧 path alias、deprecated Handler 或新旧 payload 兼容解析；
- 不进行 API 双版本、双路由或内部 fallback；
- OpenAPI inventory、bundle、生成代码和 Hurl tests 必须一起更新；
- 删除只覆盖旧 API 的测试，并为新的全部 OpenAPI operation 建立当前用户故事覆盖；
- 必要时增加测试，确认旧 route 已经不再注册。

OpenAI、Anthropic、Gemini 等外部兼容协议属于产品主动承诺的协议，不是 GizWay 内部旧版本兼容层。
是否支持某项外部协议，以当前 Milestone 明确列出的兼容矩阵为准。

## 5. 代码、配置与术语

Breaking refactor 必须贯穿整个实现：

- 删除旧 Handler、Store method、type、field、adapter 和 worker；
- 删除旧 Server Config section、CLI flag、默认值和解析逻辑；
- 示例 Config、Compose、Seed、Fixture 和测试变量全部使用新结构；
- 数据库、OpenAPI、Go 标识、日志字段和文档使用同一领域术语；
- 不增加“暂时兼容”的宏、环境变量、feature flag 或隐藏分支；
- 不因为旧代码已经存在就把它继续带入新 binary。

第三方服务使用其官方配置和协议；本规则不要求修改 ZITADEL、PowerSync 或 Bifrost 自身的内部
命名，只要求 GizWay/GizPay 的领域边界和 adapter 使用当前契约。

## 6. 测试

每个 Milestone 的测试从当前唯一契约出发：

1. 先写当前用户故事、OpenAPI、数据库约束和 E2E；
2. 删除与新契约冲突或只验证旧设计的测试；
3. 从空 PostgreSQL database 和全新本地状态执行；
4. 不编写旧数据库升级、旧 API 兼容、双写或切流测试；
5. 验证当前 Schema、API、Config、binary 和第三方集成能够独立启动；
6. 验证旧名称、旧 route 和旧配置已经从当前实现消失；
7. 只有当前 Milestone 的全部门禁通过，才认为该 breaking refactor 完成。

测试失败时修复当前实现，不通过重新引入旧行为让测试通过。

## 7. 何时停止使用该规则

只有项目明确进入生产、开始保留真实用户数据或对外承诺稳定兼容后，才能改变本规则。届时必须通过
新的架构决策明确：

- 数据 migration 与回滚；
- API versioning 和 deprecation；
- 灰度、双写或兼容周期；
- 在线升级、数据核对和灾难恢复。

在出现这项明确决策以前，任何实现者都不得自行假设存在生产存量，也不得为不存在的迁移和兼容
需求增加代码。

## 8. Milestone 文档要求

每个新的 Milestone 至少应说明：

- 本次唯一有效的目标和范围；
- 相对上一 Milestone 的 breaking changes；
- 必须删除的旧 API、Schema、Config、代码和测试；
- 新的空库初始化方式；
- 当前 API/SDK/Compose/E2E 验收条件；
- 明确延后到后续 Milestone 的 User Stories。

一句话原则：

> 项目尚未上线；每个 Milestone 都是一次从空环境执行的 breaking refactor，仓库最终只保留当前
> Milestone 的唯一实现，不迁移旧数据，也不兼容旧契约。
