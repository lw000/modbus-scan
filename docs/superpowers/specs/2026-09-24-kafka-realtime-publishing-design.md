# Kafka 实时数据推送设计

## 目标

为每台 Modbus 设备提供独立的 Kafka 实时数据推送能力。设备可独立开启或关闭推送、配置 Topic，并在全量推送与变化推送之间二选一。Kafka 全局连接配置和设备推送配置均持久化到 SQLite，且支持运行时动态启停和修改，无需重启服务或设备采集任务。

## 现状与约束

- 项目使用 Go 1.21、Gin、SQLite 和内存 UDM。
- `Collector` 在点位更新后将值写入 UDM，并通过现有回调发布到 WebSocket Hub。
- 设备配置、点位配置和持久启停状态保存在 SQLite。
- Kafka 实现参考 `D:\work\go_work\src\kafka_work` 中的 IBM Sarama、SASL 和 TLS 参数约定；参考工程是消费端，本项目需新增生产端实现。
- Kafka 故障不得阻塞 Modbus 采集。
- 允许在 Kafka 长时间不可用或内存队列溢出时丢失消息，服务重启后不补发历史数据。
- 全量与变化是互斥模式，同一设备不能同时启用两种模式。

## 总体架构

保留现有逐点实时链路：

```text
Collector -> UDM -> WebSocket Hub
```

在每次完整扫描结束后增加设备级快照事件：

```text
Collector -> Scan Snapshot -> Kafka Coordinator -> Bounded Queue -> Kafka Producer
```

Kafka Coordinator 与 Collector 解耦。Collector 只提交不可变的扫描快照，不等待 Kafka 网络 I/O。Coordinator 根据设备配置执行全量周期判断或变化比较，生成设备级消息并写入有界内存队列。后台 Kafka Producer 负责连接、重连、序列化和发送。

WebSocket 继续按点位发布，避免改变现有页面的订阅协议和实时曲线行为。

## 扫描快照

每轮扫描结束后产生一个设备快照，包含：

- 设备 ID 和设备名称。
- 本轮扫描完成时间，使用 UTC。
- 本轮成功读取的点位值。

读取失败的点位不包含在本轮成功值集合中，不被解释为删除或变化。快照必须在交给 Coordinator 前完成复制，避免后续 UDM 更新产生数据竞争。

## 推送模式

### 全量模式

- 模式值为 `full`。
- 设备推送启用后的第一轮成功扫描立即发送全量消息。
- 之后按照设备配置的 `full_interval_sec` 周期发送。
- 全量消息包含当前 UDM 中全部有效点位，而不仅是本轮成功读取的点位；尚未获得有效值的点位不发送。
- 周期以最近一次成功加入内存队列的全量消息时间为基准。

### 变化模式

- 模式值为 `change`。
- 设备推送启用后的第一轮成功扫描只建立比较基线，不发送消息。
- 后续扫描将本轮成功读取值与已保存基线进行精确比较。
- 至少一个点位变化时发送一条设备级消息，其中只包含变化点位。
- 浮点值不设置额外死区，按解析后的 Go 值精确比较。
- 本轮读取失败的点位不改变对应基线。

### 状态重置

以下事件清空设备的变化基线和全量计时状态：

- 设备级推送由关闭切换为开启。
- Topic 或模式发生变化。
- 设备采集停止或重启。
- Kafka Coordinator 重启。

切换到 `full` 后下一轮立即发送全量消息；切换到 `change` 后下一轮只建立基线。

## SQLite 数据模型

### 全局配置表

`kafka_settings` 是单例表，迁移时插入一条默认关闭记录。字段定义如下：

```sql
CREATE TABLE kafka_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL DEFAULT 0,
    brokers TEXT NOT NULL DEFAULT '[]',
    client_id TEXT NOT NULL DEFAULT 'modbus-scan',
    kafka_version TEXT NOT NULL DEFAULT '3.0.0',
    security_protocol TEXT NOT NULL DEFAULT '',
    sasl_mechanism TEXT NOT NULL DEFAULT '',
    sasl_username TEXT NOT NULL DEFAULT '',
    sasl_password TEXT NOT NULL DEFAULT '',
    ssl_ca_location TEXT NOT NULL DEFAULT '',
    ssl_certificate_location TEXT NOT NULL DEFAULT '',
    ssl_key_location TEXT NOT NULL DEFAULT '',
    ssl_endpoint_identification_algorithm TEXT NOT NULL DEFAULT '',
    queue_capacity INTEGER NOT NULL DEFAULT 1000,
    updated_at DATETIME NOT NULL
);
```

`brokers` 使用 JSON 字符串数组存储。TLS 相对路径以服务 TOML 配置文件所在目录为基准。全局配置不再放入 `config.toml`。

### 设备推送配置表

设备配置独立存储，并通过外键与设备形成一对一关系：

```sql
CREATE TABLE device_kafka_configs (
    device_id INTEGER PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    enabled INTEGER NOT NULL DEFAULT 0,
    topic TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'change',
    full_interval_sec INTEGER NOT NULL DEFAULT 60,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CHECK (mode IN ('full', 'change'))
);
```

创建设备时在同一事务内创建默认 Kafka 配置。删除设备时由外键级联删除。Kafka 配置独立更新，不递增设备 `config_version`，因为变更通过 Coordinator 热加载，不要求重启采集任务。

## 配置校验

全局 Kafka 配置遵循以下规则：

- 启用时至少存在一个合法的 `host:port` broker。
- `client_id` 不能为空。
- Kafka 版本必须能被 Sarama 解析。
- `queue_capacity` 范围为 1 到 100000。
- 安全协议只允许空值、`SASL_PLAINTEXT`、`SSL` 或 `SASL_SSL`。
- SASL 机制只允许空值、`PLAIN`、`SCRAM-SHA-256` 或 `SCRAM-SHA-512`，并与安全协议保持一致。
- TLS CA、客户端证书和私钥按所选协议校验；客户端证书和私钥必须成对提供。
- Endpoint Identification Algorithm 只允许空值、`none` 或 `https`。

设备配置遵循以下规则：

- 模式只允许 `full` 或 `change`。
- 推送启用时 Topic 必须为 1 到 249 个字符，只允许 Kafka Topic 的字母、数字、点、下划线和连字符，并拒绝 `.` 与 `..`。
- `full_interval_sec` 始终保存，范围为 1 到 86400 秒；在 `change` 模式下不参与调度。

## Kafka 消息契约

Kafka Message Key 是十进制设备 ID，以保证同一设备在 Topic 分区内保持顺序。Message Value 是 UTF-8 JSON：

```json
{
  "schema_version": 1,
  "message_type": "change",
  "device": {
    "id": 12,
    "name": "PLC-01"
  },
  "collected_at": "2026-09-24T10:30:00.123Z",
  "points": {
    "Temperature": 25.6,
    "Running": true,
    "Counter": 1024
  }
}
```

- `schema_version` 固定为 `1`。
- `message_type` 只能是 `full` 或 `change`。
- `collected_at` 使用扫描轮次完成时间，编码为 RFC3339Nano UTC 时间。
- 点位保持采集解析后的 JSON 类型：布尔值为 boolean，整数和浮点数为 number。
- `points` 是以 TagName 为键的 JSON 对象，字段顺序不构成协议的一部分。
- 全量消息包含全部当前有效点位；变化消息只包含发生变化的点位。

## 动态配置与 API

新增全局 Kafka 管理 API，用于读取状态、读取脱敏配置和更新配置。新增设备 Kafka 配置 API：

```text
GET /api/v1/kafka
PUT /api/v1/kafka
GET /api/v1/devices/:id/kafka
PUT /api/v1/devices/:id/kafka
```

全局查询响应包含配置、运行状态和最近错误，但永不返回 SASL 密码原文。更新请求中密码为空表示保留已保存密码；另提供显式清除密码的布尔字段，避免无法删除旧密码。

全局动态更新采用先验证、后切换的方式：

1. 校验候选配置并创建候选生产者。
2. 候选生产者连接成功后，在 SQLite 事务中保存配置。
3. 原子切换 Coordinator 使用的生产者和运行配置。
4. 停止接收旧生产者的新消息，尽量排空并关闭旧生产者。

若候选连接失败，则不保存候选配置；现有生产者和已保存配置继续运行。关闭 Kafka 是一个显式例外：先停止接收新 Kafka 消息，按关闭超时尽量排空队列，关闭生产者，然后保存关闭状态。

设备配置更新成功后主动刷新 Coordinator 缓存并重置该设备推送状态，下一轮扫描按新配置处理。

## 管理页面

设备详情页增加 Kafka 推送区域：

- 设备推送开关。
- Topic 输入框。
- `full` 与 `change` 单选模式。
- 全量周期输入框；变化模式下禁用，但保留原值。
- 独立保存操作，保存后立即生效。

全局管理页面增加 Kafka 设置入口，支持配置连接、安全参数、队列容量和总开关，并展示 `disabled`、`connecting`、`online`、`reconnecting`、`stopping`、`error` 状态与脱敏的最近错误。当前项目没有认证，页面与 API 只适合监听在可信的本机地址；README 必须明确这一安全边界。

## 启动顺序

服务按以下顺序启动，确保消费实时数据的基础设施先于采集任务就绪：

1. 加载服务 TOML 配置并打开 SQLite。
2. 创建 WebSocket Hub、HTTP Router 和监听端口。
3. 启动 HTTP/WebSocket 服务。
4. 从 SQLite 加载 Kafka 全局配置和全部设备配置。
5. 启动 Kafka Coordinator、内存队列和发送工作线程。
6. 若 Kafka 全局启用，立即启动生产者连接；连接暂时失败时保持 Kafka 子系统运行并后台退避重连，队列仍可接收数据。
7. 启动所有持久化为启用状态的设备采集任务。
8. 向 Windows SCM 报告服务就绪。

服务启动时 Kafka 全局开关为启用状态，就必须启动 Kafka 子系统。Broker 不可达表示运行状态为 `reconnecting`，不等同于 Kafka 未启动，也不阻止 HTTP 和采集启动。

## 停止顺序

停止顺序与数据产生方向相反：

1. 停止所有设备采集，确保不再产生新快照。
2. 停止 Kafka Coordinator 接收新快照。
3. 在服务 shutdown 超时内尽量排空 Kafka 队列并关闭生产者。
4. 关闭 HTTP/WebSocket 服务。
5. 关闭 SQLite。

超时后允许丢弃尚未发送的内存消息，记录丢弃数量并继续退出。

## 队列、重连与错误隔离

- 使用 IBM Sarama 异步生产者，确认级别为 `WaitForLocal`，并启用有限次数发送重试。
- Coordinator 的输入队列有固定容量，采集提交不得因 Kafka 网络状态而阻塞。
- 队列满时丢弃最旧消息，记录设备 ID、Topic 和累计丢弃数量；告警必须限频。
- 不记录消息正文、SASL 密码、私钥或完整认证配置。
- 序列化失败只丢弃单条消息，不影响后续消息。
- Broker 不可达时后台按有上限的指数退避持续重连。
- 全局关闭时不创建生产者，也不接受设备快照进入 Kafka 队列。
- 全局启用但设备推送关闭时，该设备不产生 Kafka 消息。

## 测试策略

### 配置与存储

- 迁移创建全局单例记录和设备配置表。
- 创建设备同步创建默认推送配置。
- 删除设备级联删除推送配置。
- 全局与设备配置的全部校验边界。
- 设备配置独立更新且不修改设备 `config_version`。
- 密码查询脱敏、空密码保留和显式清除。

### 推送协调器

- 全量模式首轮立即发送和后续周期判断。
- 变化模式首轮只建立基线，后续只发送变化点位。
- 部分读取失败不产生错误变化。
- 开关、Topic 和模式变更后状态重置。
- 队列满时丢弃最旧消息且采集提交不阻塞。
- 使用可控时钟避免依赖真实时间。

### Kafka 生命周期

- 服务启动时按 SQLite 开关启动或跳过生产者。
- Broker 不可达时进入重连状态且不阻止采集。
- 动态开启、关闭和原子替换连接。
- 候选连接失败时旧配置和旧生产者保持不变。
- 停止时排空队列，超时后安全退出。
- 使用伪生产者测试，不要求自动化测试环境存在真实 Kafka。

### API 与页面

- 全局配置和状态 API。
- 设备配置读取、更新、校验错误和不存在设备。
- 页面字段装载、模式联动、脱敏密码与保存后的即时生效。
- README 提供配置说明、消息样例、动态启停行为和真实 Kafka 联调步骤。

## 依赖与兼容性

- 新增 `github.com/IBM/sarama`，版本在实施时固定并提交对应 `go.mod` 与 `go.sum`。
- 不改变现有 WebSocket 消息协议和 Modbus 点位配置格式。
- 数据库迁移必须兼容已有数据库；默认 Kafka 全局关闭、设备推送关闭，因此升级后现有部署行为不变。
- 自动化验证至少包括 `gofmt`、`go test ./...`、`go test -race ./...`、`go vet ./...` 和 Windows 构建。
