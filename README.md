# modbus-scan

Modbus TCP 数据采集与本地 Web 管理服务。设备和点位配置存储在 SQLite，管理页面使用 Gin 提供的 JSON API 和内嵌 HTML/CSS/JavaScript。

## 功能

- 管理设备连接参数、点位和持久化启停状态。
- 支持 HoldingReg、InputReg、CoilStatus、InputStatus。
- 支持 Bool、Int16、UInt16、Int32、UInt32、Float32、Double。
- 支持 ABCD、DCBA、CDAB、BADC 字节序。
- 支持连续位段提取、地址合并、断线重连和离线探测。
- 支持点位 CSV 严格全量导入和兼容导出。
- 展示设备状态、最后错误、最后采集时间和当前点位值。
- 服务日志同时输出到控制台和滚动日志文件。

## 构建和启动

要求 Go 1.21 或更高版本。

```powershell
go build -o modbus-scan.exe ./cmd/modbus-scan
./modbus-scan.exe -config configs/config.toml
```

默认打开：<http://127.0.0.1:8080>

首次启动会创建空的 `data/modbus-scan.db`。程序不会导入旧 TOML 设备配置；请在页面添加设备，然后逐条添加点位或导入 CSV。

离线校验 CSV：

```powershell
./modbus-scan.exe -validate -csv configs/points.csv
```

## 注册为 Windows 服务

请先把可执行文件和配置文件放到固定目录，然后在管理员 PowerShell 中执行：

```powershell
.\modbus-scan.exe install -config .\configs\config.toml
.\modbus-scan.exe start
.\modbus-scan.exe status
.\modbus-scan.exe restart
.\modbus-scan.exe stop
.\modbus-scan.exe uninstall
```

服务名固定为 `modbus-scan`，显示名为 `Modbus Scan`，使用 Windows `LocalSystem` 账户并随系统自动启动。`start` 对已运行服务、`stop` 对已停止服务均视为成功；卸载前必须先停止服务。

安装时会保存可执行文件和配置文件的绝对路径。安装后不要直接移动这两个文件；如需迁移，请先停止并卸载服务，在新目录重新安装。安装、卸载、启动、停止和重启通常需要管理员权限。

数据库和日志的相对路径以配置文件所在目录为基准，而不是 PowerShell 或 Windows 服务的工作目录。绝对路径保持不变。

## 服务配置

`configs/config.toml` 只配置服务、数据库和日志，不保存设备参数：

```toml
[server]
host = "127.0.0.1"
port = 8080
read_header_timeout_sec = 5
read_timeout_sec = 15
write_timeout_sec = 30
idle_timeout_sec = 60
shutdown_timeout_sec = 15

[database]
path = "../data/modbus-scan.db"
busy_timeout_ms = 5000

[log]
level = "info"
format = "text"
console = true
file = "../logs/modbus-scan.log"
max_size_mb = 50
max_backups = 10
max_age_days = 30
compress = true
```

服务配置仅在程序启动时读取，修改后需要重启整个程序。当前版本没有登录认证，默认仅监听 `127.0.0.1`；不要直接暴露到不可信网络。

## 页面操作

1. 在设备列表点击“添加设备”。
2. 进入设备详情，新增点位或导入 CSV。
3. 点击“启动”开始采集。
4. 修改设备或点位只更新 SQLite，运行实例继续使用旧配置。
5. 页面出现“配置待重启生效”后，点击“重启”加载新配置。
6. “停止”会持久化状态，程序下次启动时不会自动启动该设备。

删除设备会先停止采集，再级联删除该设备的点位。实时值只存内存，不写入 SQLite。

## CSV 格式

```csv
TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description
Temperature,HoldingReg,0,Int16,0,16,0,温度
OperationMode,HoldingReg,10,UInt16,4,2,0,运行模式
Running,CoilStatus,0,Bool,0,0,0,运行状态
```

导入必须严格匹配八列表头。`Writeable` 使用 `0`（只读）或 `1`（允许写入），`Description` 可为空且最多 255 个字符。任意一行错误都会拒绝整份文件，原点位保持不变。导入成功后会全量替换该设备点位，但不会自动重启设备。详细规则见 [点位配置手册](docs/point.csv的点位配置.md)。

## 运行状态

- `stopped`：已停止。
- `starting`：正在启动。
- `online`：在线采集。
- `reconnecting`：正在重连。
- `offline`：离线探测。
- `stopping`：正在停止。
- `error`：启动或配置错误。

单台设备失败不会阻止管理页面或其他设备运行。SIGINT/SIGTERM 会先关闭 HTTP 服务，再停止并等待全部采集任务。

`runtime.operation` 表示当前生命周期操作，可为 `start`、`stop`、`restart`；字段不存在或为空表示当前没有操作。同一设备正在执行生命周期操作时，后续启动、停止或重启请求不会排队，而是返回 HTTP `409 Conflict`：

```json
{"error":{"code":"device_busy","message":"设备正在操作，请稍后重试"}}
```

设备列表页和详情页通过状态轮询同步该字段并禁用生命周期按钮。不同设备之间仍可并行操作。

## 数据备份

停止程序后备份 `data/modbus-scan.db`。运行期间 SQLite 使用 WAL，直接复制单个数据库文件可能无法得到一致快照。

日志默认位于 `logs/modbus-scan.log`。数据库、日志、构建产物和 Go 缓存均已排除在 Git 之外。

## 验证

```powershell
go test ./...
go test -race ./...
go vet ./...
go build -o modbus-scan.exe ./cmd/modbus-scan
```

## 主要目录

```text
cmd/modbus-scan/       程序入口和生命周期装配
internal/appconfig/    服务 TOML 配置
internal/logging/      结构化日志与文件轮转
internal/store/        SQLite schema 与持久化
internal/service/      设备、点位和 CSV 用例
internal/runtime/      设备启停和运行状态
internal/httpapi/      Gin JSON API 与静态资源服务
internal/collector/    Modbus 连接、重连、采集和解析
internal/udm/          实时值内存快照
web/                   内嵌管理页面
```

## Kafka 实时推送

Kafka 全局连接配置和设备推送配置均保存在 SQLite，可在管理页面动态修改，无需重启服务或设备采集。新安装及数据库升级后 Kafka 默认关闭。

- 设备列表页的“Kafka 设置”维护 brokers、Kafka 版本、SASL/TLS、队列容量和全局开关。
- 设备详情页单独配置推送开关、Topic 和推送模式。
- `full` 为全量模式：启用后的首轮扫描立即推送，之后按设备配置周期推送。
- `change` 为变化模式：首轮建立基线，之后仅推送发生变化的点位。
- 全量与变化模式互斥，每台设备只能选择一种。

消息 Key 为设备 ID，Value 为 UTF-8 JSON：

```json
{
  "schema_version": 1,
  "message_type": "change",
  "device": {"id": 12, "name": "PLC-01"},
  "collected_at": "2026-09-24T10:30:00.123Z",
  "points": {"Temperature": 25.6, "Running": true}
}
```

服务先启动 HTTP/WebSocket 和 Kafka 内存队列，再启动设备采集。Broker 暂时不可达时采集继续运行，Kafka 在后台重连；队列满后丢弃最旧消息并记录告警，服务重启后不会补发历史消息。停止时先停止设备采集，再关闭 Kafka，最后关闭 HTTP 服务。

管理接口包括 `GET/PUT /api/v1/kafka` 和 `GET/PUT /api/v1/devices/:id/kafka`。查询不会返回 SASL 密码；更新时密码留空表示保留原密码。当前管理页面没有登录认证，请保持监听 `127.0.0.1`，不要直接暴露到不可信网络。

联调时先创建测试 Topic，在页面启用全局 Kafka 并为设备配置该 Topic，然后使用 Kafka 自带消费者验证：

```powershell
kafka-console-consumer.bat --bootstrap-server 127.0.0.1:9092 --topic modbus-values --from-beginning
```
# 点位批量删除与实时值

设备详情页支持删除当前筛选结果的当前页点位。后端接口为 `DELETE /api/v1/devices/:id/points`，JSON 请求体为 `{"ids":[1,2]}`；一次允许 1 到 500 个不重复的正整数 ID，所有点位必须属于该设备，删除在一个事务中完成。

点位操作栏的“实时值”通过 `/api/v1/ws` 订阅采集更新。客户端发送 `{"action":"subscribe","device_id":1,"tags":["TagA"]}`，取消订阅使用相同结构并将 `action` 改为 `unsubscribe`。服务端只推送已订阅的设备和位号；曲线最多保留弹窗本次打开期间的 300 个样本，不写入数据库。
