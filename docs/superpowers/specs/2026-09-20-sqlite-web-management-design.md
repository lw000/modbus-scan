# SQLite Web 管理功能设计

## 1. 目标与范围

将现有 Modbus TCP 命令行采集程序改造为模块化单体服务，在一个 Go 进程中同时提供：

- SQLite 设备与点位配置存储。
- Gin HTTP JSON API。
- 独立 HTML、CSS、JavaScript 管理页面，不使用 Go HTML 模板和前端构建链。
- 设备添加、编辑、删除、启动、停止和重启。
- 点位添加、编辑、删除，以及 CSV 严格导入和兼容导出。
- 设备连接状态、最后错误、最后采集时间和当前点位值展示。
- TOML 服务配置，以及控制台和滚动文件日志。

本期不提供登录认证、HTTPS、点位写值、历史数据存储、WebSocket、审计日志和多用户并发编辑。HTTP 服务默认只监听本机地址。

## 2. 技术方案

采用模块化单体：单个 `modbus-scan.exe` 集成 HTTP、SQLite 和所有设备采集任务。主要模块如下：

- `internal/appconfig`：加载并校验服务 TOML 配置。
- `internal/logging`：初始化结构化日志和文件轮转。
- `internal/store`：SQLite schema、迁移及设备和点位 CRUD。
- `internal/service`：设备配置、点位配置和 CSV 业务规则。
- `internal/runtime`：设备采集实例的启动、停止、重启和状态管理。
- `internal/httpapi`：Gin 路由、请求校验和 JSON 响应。
- `internal/collector`：保留并改造现有 Modbus 连接、采集和解析逻辑。
- `internal/udm`：保留实时值内存快照能力。
- `web`：静态 HTML、CSS 和 JavaScript。

新增依赖：

- `github.com/gin-gonic/gin`
- `modernc.org/sqlite`
- `gopkg.in/natefinch/lumberjack.v2`

数据库使用标准库 `database/sql` 和显式 SQL，不引入 ORM。SQLite 驱动使用纯 Go 实现，Windows 构建不依赖 CGO。

## 3. 启动与退出流程

启动流程：

1. 通过 `-config configs/config.toml` 加载服务配置。
2. 初始化控制台和滚动文件日志。
3. 创建数据库及日志目录。
4. 打开 SQLite，启用外键、WAL 和 busy timeout。
5. 执行可重复的 schema 迁移；首次启动得到空数据库。
6. 创建设备运行时管理器。
7. 尝试启动数据库中所有 `enabled=true` 的设备；单设备失败不阻止服务启动。
8. 启动 Gin HTTP 服务。
9. 收到 SIGINT 或 SIGTERM 后停止接收 HTTP 请求，并发停止所有设备，等待任务退出，最后关闭数据库和日志输出。

程序不自动读取或迁移旧设备 TOML 和点位 CSV。设备由页面手动添加，点位由页面新增或手动导入 CSV。数据库为空是正常状态。

## 4. 服务配置与日志

`configs/config.toml` 只保存服务基础设施配置，不保存设备或点位：

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
path = "data/modbus-scan.db"
busy_timeout_ms = 5000

[log]
level = "info"
format = "text"
console = true
file = "logs/modbus-scan.log"
max_size_mb = 50
max_backups = 10
max_age_days = 30
compress = true
```

约束：

- `server.host` 默认为 `127.0.0.1`，`server.port` 必须在 `1..65535`。
- 相对路径以进程当前工作目录为基准，缺失目录自动创建。
- 服务配置仅在启动时读取，修改后需要重启整个程序。
- 配置缺失、字段非法、日志初始化失败或监听失败时明确报错并退出。
- 日志级别支持 `debug`、`info`、`warn` 和 `error`，格式支持 `text` 和 `json`。
- Gin 访问日志包含方法、路径、状态码、耗时和客户端地址。
- 不记录请求体、CSV 内容、敏感信息和每个扫描周期的完整点位值。
- 文件日志按大小滚动，支持备份数量、保留天数和 gzip 压缩。
- 数据库、日志和运行产物加入 `.gitignore`。

## 5. 数据模型

### 5.1 devices

```text
id                  INTEGER PRIMARY KEY
name                TEXT UNIQUE NOT NULL
enabled             INTEGER NOT NULL
address             TEXT NOT NULL
port                INTEGER NOT NULL
slave_id            INTEGER NOT NULL
byte_order           TEXT NOT NULL
timeout_sec          INTEGER NOT NULL
scan_interval_ms    INTEGER NOT NULL
config_version      INTEGER NOT NULL
created_at           DATETIME NOT NULL
updated_at           DATETIME NOT NULL
```

### 5.2 points

```text
id             INTEGER PRIMARY KEY
device_id      INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE
tag_name       TEXT NOT NULL
reg_type       TEXT NOT NULL
address        INTEGER NOT NULL
data_type      TEXT NOT NULL
bit_offset     INTEGER NOT NULL
bit_len        INTEGER NOT NULL
writeable      INTEGER NOT NULL
created_at     DATETIME NOT NULL
updated_at     DATETIME NOT NULL

UNIQUE(device_id, tag_name)
```

每台设备拥有独立点位集合。设备或点位配置发生修改时，设备的 `config_version` 在同一事务中递增。运行实例记录其加载版本，数据库版本较新时页面显示“配置待重启生效”。连接状态、错误、采集时间和实时值只保存在内存，避免高频写 SQLite。

## 6. 设备运行时

`DeviceManager` 以设备 ID 管理线程安全的 `DeviceRuntime`。每个运行实例拥有独立的 context、cancel、WaitGroup、连接管理器、采集器、UDM、已加载配置版本和状态快照。

状态集合：

- `stopped`
- `starting`
- `online`
- `reconnecting`
- `offline`
- `stopping`
- `error`

操作语义：

- 启动：持久化 `enabled=true`，从 SQLite 加载完整配置快照并启动。首次连接失败进入重连和离线探测，不放弃该设备。重复启动幂等。
- 停止：持久化 `enabled=false`，取消 context，关闭 TCP 连接并等待所有 goroutine 退出。保留最后值供页面查看。重复停止幂等。
- 重启：不修改 `enabled`；停止旧实例，重新读取 SQLite 并启动新实例。只有启用设备允许重启。新配置失败时进入 `error` 或 `offline`，不回滚旧实例。
- 编辑：只写 SQLite 并递增版本，不影响正在运行的实例。
- 删除：运行中的设备先安全停止，再删除设备和级联点位。

同一设备的启动、停止和重启串行化。管理器不持有全局锁执行网络连接或等待 goroutine。重连退避改用可响应 context 的 timer，使停止能立即打断等待。

## 7. HTTP API

所有业务接口使用 `/api/v1` 前缀。

```text
GET    /api/v1/devices
POST   /api/v1/devices
GET    /api/v1/devices/:id
PUT    /api/v1/devices/:id
DELETE /api/v1/devices/:id
POST   /api/v1/devices/:id/start
POST   /api/v1/devices/:id/stop
POST   /api/v1/devices/:id/restart
GET    /api/v1/devices/:id/status
GET    /api/v1/devices/:id/values

GET    /api/v1/devices/:id/points
POST   /api/v1/devices/:id/points
GET    /api/v1/devices/:id/points/:pointID
PUT    /api/v1/devices/:id/points/:pointID
DELETE /api/v1/devices/:id/points/:pointID

POST   /api/v1/devices/:id/points/import
GET    /api/v1/devices/:id/points/export
```

JSON 成功响应使用 `{"data": ...}`。错误响应使用固定结构：

```json
{
  "error": {
    "code": "validation_failed",
    "message": "点位配置校验失败",
    "details": [
      {"row": 3, "field": "bit_len", "message": "BitOffset + BitLen 超出数据类型位宽"}
    ]
  }
}
```

状态码约定：`400` 校验失败、`404` 资源不存在、`409` 名称或状态冲突、`413` 上传过大、`500` 内部错误、`503` 设备暂时不可用。JSON 请求限制大小并拒绝未知字段。错误响应不泄露数据库路径、堆栈或底层网络细节。

## 8. CSV 导入导出

导入使用 `multipart/form-data` 的 `file` 字段，请求体上限 10 MiB。必须严格匹配以下七列表头：

```csv
TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable
```

规则：

- 严格校验所有行，收集可识别的行号和字段错误。
- `Writeable` 只接受 `true` 或 `false`。
- 同一设备的 `TagName` 不得重复。
- 多寄存器点位结束地址不得超过 `65535`。
- 任意错误导致整份导入失败，数据库不发生变化。
- 全部通过后在单个事务中删除该设备旧点位、插入新点位并递增 `config_version`。
- 成功导入不自动重启设备。

导出直接流式写入响应，不创建临时文件。格式与七列 CSV 兼容，按 `RegType`、`Address`、`TagName` 排序，安全文件名为 `<device-name>-points.csv`。

`-validate -csv <file>` 作为离线 CSV 校验工具保留；正常运行只使用 `-config` 服务配置参数。

## 9. 管理页面

静态目录：

```text
web/
├── index.html
├── device.html
├── css/app.css
└── js/
    ├── api.js
    ├── devices.js
    └── device.js
```

Gin 直接提供静态文件，JavaScript 使用 `fetch` 调用 API，不使用 Go HTML 模板、Node、npm 或外部 CDN。

设备列表页展示设备地址、启用状态、运行状态、最后采集时间、最后错误、点位数量和配置待生效标识，提供新增、编辑、删除、启动、停止、重启和详情入口。

设备详情页包含：

1. 设备配置、运行状态、配置版本、最后错误及启停重启操作。
2. 可分页、搜索和筛选的点位管理，以及 CSV 导入导出。
3. 当前点位值、类型和更新时间，只读展示，不提供点位写值。

状态和值默认每 2 秒轮询，页面进入后台后降低频率。删除设备和全量导入必须二次确认；写操作期间禁用按钮；错误显示在页面通知区域，不使用浏览器 `alert`。

## 10. 安全与可靠性

- 默认仅绑定 `127.0.0.1`；本期无认证，因此文档明确不建议暴露到不可信网络。
- HTTP server 设置读取头、读取、写入、空闲和优雅退出超时。
- 所有 SQL 使用参数绑定，关联写操作使用事务。
- 上传大小受限，不将用户输入拼接为文件路径或响应头。
- 所有外部输入验证类型、范围、枚举和长度。
- 单设备故障不影响其他设备或管理页面。
- 高频实时值只存内存，日志只记录状态变化和异常。

## 11. 测试与验收

数据库测试覆盖 schema 幂等、外键、级联删除、唯一约束、CRUD、事务回滚和版本递增。每个测试使用独立临时数据库。

运行时测试通过可注入的 Modbus 小接口覆盖启动、幂等操作、停止、重启、配置待生效、首次连接失败恢复、可取消退避和全局退出，不依赖真实 PLC。

HTTP 测试使用 `httptest` 覆盖 CRUD、状态操作、无效 JSON、未知字段、重复名称、缺失资源、CSV 超限、错误表头、事务替换和导出格式。

采集测试覆盖四种字节序、线圈位解析、16/32/64 位类型、中间位段提取、地址溢出和响应长度不足。并发路径运行 race 检测。

前端使用人工验收清单覆盖新增、编辑、删除、导入、导出、启停、重启、待生效提示和实时值刷新，首版不引入浏览器测试框架。

交付验证命令：

```powershell
go test ./...
go test -race ./...
go vet ./...
go build -o modbus-scan.exe ./cmd/modbus-scan
```

完成标准：单个 Windows exe 能创建空 SQLite 数据库、启动管理页面、管理设备与点位、严格导入导出 CSV、持久化启停状态、手动重启加载新配置、展示实时状态和值，并在退出时关闭全部任务和连接。README、服务配置示例、CSV 文档和 `.gitignore` 必须同步更新。
