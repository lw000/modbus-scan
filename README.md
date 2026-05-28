# modbus-scan

工业级 Modbus TCP 数据采集服务，支持在不可靠网络环境下持续、可靠地从 Modbus 从站设备采集寄存器数据。

## 功能特性

- **多寄存器类型** — 支持 HoldingReg、InputReg、CoilStatus、InputStatus 四种寄存器类型
- **多数据类型** — 支持 Bool、Int16、UInt16、Int32、UInt32、Float32、Double
- **地址合并优化** — 离散点位自动合并为批量读取块，减少网络 IO
- **字节序转换** — 支持 ABCD / DCBA / CDAB / BADC 四种字节序
- **高可用机制** — 断线重连（指数退避）+ 熔断（离线探测）+ 心跳恢复
- **反向写入** — 支持对 HoldingReg 写入数值、对 CoilStatus 写入线圈状态
- **CSV 验证** — 独立的配置校验模式，启动前即可验证点位配置
- **优雅退出** — 监听 SIGINT/SIGTERM 信号安全关闭

## 项目结构

```
modbus-scan/
├── cmd/
│   └── modbus-scan/
│       └── main.go            # 程序入口，命令行参数解析
├── internal/
│   ├── config/
│   │   ├── device_config.go   # 设备连接配置 (TOML)
│   │   └── point_config.go    # 点位配置与地址合并 (CSV)
│   ├── collector/
│   │   └── collector.go       # 采集器、连接管理、字节序转换
│   └── udm/
│       └── udm.go             # 通用数据模型 (内存共享)
├── configs/
│   ├── config.toml            # 设备连接配置文件
│   ├── points.csv             # 点位配置文件
│   └── points-coil.csv        # 线圈点位配置示例
├── build.bat                  # Windows 构建脚本
├── go.mod
└── go.sum
```

## 快速开始

### 编译

```bash
# 直接编译
go build -o modbus-scan.exe ./cmd/modbus-scan

# 或使用构建脚本
build.bat
```

### 运行

```bash
# 使用默认配置启动
modbus-scan.exe -config configs/config.toml -csv configs/points.csv

# 指定配置文件
modbus-scan.exe -config prod.toml -csv prod_points.csv

# 仅验证 CSV 配置
modbus-scan.exe -validate -csv configs/points.csv
modbus-scan.exe -validate -csv my_points.csv
```

### 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-config` | `config.toml` | TOML 配置文件路径 |
| `-csv` | `points.csv` | 点位 CSV 文件路径 |
| `-validate` | `false` | 仅验证 CSV 配置文件，不启动采集服务 |

## 配置说明

### config.toml

```toml
[modbus]
address           = "192.168.1.100"   # Modbus TCP 设备 IP 地址
port              = 502               # 端口号
slave_id          = 1                 # 从站 ID (1-247)
byte_order        = "CDAB"            # 字节序: ABCD / DCBA / CDAB / BADC
timeout_sec       = 5                 # 通信超时时间（秒）
scan_interval_ms  = 500               # 采集周期（毫秒）
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `address` | string | 是 | 设备 IP 地址 |
| `port` | int | 是 | 端口号，默认 502 |
| `slave_id` | int | 是 | 从站地址，范围 1-247 |
| `byte_order` | string | 否 | 字节序，默认 ABCD |
| `timeout_sec` | int | 否 | 通信超时，默认 5 秒 |
| `scan_interval_ms` | int | 否 | 采集周期，默认 2000 毫秒 |

### points.csv

```csv
TagName,RegType,Address,DataType,BitOffset,BitLen,Scale,Offset,Writeable
Temperature,HoldingReg,0,Int16,0,16,1,0,false
Pressure,HoldingReg,1,Int32,0,32,0.01,0,false
AlarmBit,HoldingReg,9,UInt16,3,1,1,0,false
StatusCode,HoldingReg,10,UInt16,4,4,1,0,false
ModeId,HoldingReg,10,UInt16,8,3,1,0,false
MotorSpeed,InputReg,0,UInt16,0,16,1,0,false
RunStatus,CoilStatus,0,Bool,0,0,1,0,false
AlarmOutput,CoilStatus,1,Bool,0,0,1,0,true
LimitSwitch,InputStatus,0,Bool,0,0,1,0,false
```

| 列名 | 说明 | 可选值 |
|------|------|--------|
| TagName | 标签名称，全局唯一 | 自定义字符串 |
| RegType | 寄存器类型 | `HoldingReg` / `InputReg` / `CoilStatus` / `InputStatus` |
| Address | 寄存器地址 | 0-65535 |
| DataType | 数据类型 | `Bool` / `Int16` / `UInt16` / `Int32` / `UInt32` / `Float32` / `Double` |
| BitOffset | 位偏移（寄存器类型有效） | 见下表 |
| BitLen | 位长度（寄存器类型有效） | 见下表 |
| Scale | 缩放系数 | 浮点数 |
| Offset | 偏移量 | 浮点数 |
| Writeable | 是否可写 | `true` / `false` |

> 最终值 = 原始值 × Scale + Offset

#### BitOffset / BitLen 有效范围

| 数据类型 | BitOffset | BitLen | BitOffset+BitLen |
|----------|-----------|--------|------------------|
| Bool | 0 | 0 | 0 |
| Int16 / UInt16 | 0~15 | BitOffset=0: 1~16; BitOffset>0: 0~15 | ≤16 |
| Int32 / UInt32 / Float32 | 0~31 | BitOffset=0: 1~32; BitOffset>0: 0~31 | ≤32 |
| Double | 0~63 | BitOffset=0: 1~64; BitOffset>0: 0~63 | ≤64 |

- **BitOffset=0, BitLen≥1**：显式位提取，从 BitOffset 开始提取 BitLen 位（Int16 用 BitLen=16 读取全部 16 位）
- **BitOffset>0, BitLen=0**：读取 BitOffset 位置的 1 位
- **BitOffset=0, BitLen=0**：不合法（Bool 类型除外）
- **BitOffset>0, BitLen≥1**：从 BitOffset 开始提取 BitLen 位
- **Bool 在寄存器中**：BitOffset 和 BitLen 只能为 0（每个地址即 1 个 Bool）
- **Float32/Double**：不支持位提取（BitOffset 和 BitLen 必须为 0）
- **线圈类型 (CoilStatus/InputStatus)**：忽略 BitOffset 和 BitLen

#### 位提取规则

**统一路径**：读取寄存器原始值 → 转 uint64 → 按 BitOffset+BitLen 提取 → 按 DataType 转换

| BitOffset | BitLen | effectiveBitLen | 说明 |
|-----------|--------|-----------------|------|
| 0 | ≥1 | BitLen | 从 BitOffset 开始提取 BitLen 位 |
| >0 | 0 | 1 | 读取 BitOffset 位置的 1 位 |
| >0 | ≥1 | BitLen | 从 BitOffset 开始提取 BitLen 位 |

**DataType 转换**：

| DataType | 提取结果 | 最终值 |
|----------|---------|--------|
| Bool | extracted==1 | `true`/`false` |
| Int16 | effectiveBitLen==16 → int16(extracted) | `float64(int16)×Scale+Offset` |
| Int16 | effectiveBitLen<16 → extracted (无符号) | `float64(extracted)×Scale+Offset` |
| Int32 | effectiveBitLen==32 → int32(extracted) | `float64(int32)×Scale+Offset` |
| Int32 | effectiveBitLen<32 → extracted (无符号) | `float64(extracted)×Scale+Offset` |
| UInt16 / UInt32 | extracted | `float64(extracted)×Scale+Offset` |
| Float32 | math.Float32frombits | `float64(float32)×Scale+Offset` |
| Double | math.Float64frombits | `float64×Scale+Offset` |

> 示例：`BitOffset=4, BitLen=4, DataType=UInt16` → 提取 bit4~bit7，值域 0~15
>
> 示例：`BitOffset=8, BitLen=3, DataType=UInt16` → 提取 bit8~bit10，值域 0~7
>
> 示例：`BitOffset=0, BitLen=16, DataType=Int16` → 提取全部 16 位，有符号转换
>
> 示例：`BitOffset=0, BitLen=1, DataType=Int16` → 提取 bit0（第 1 位），值域 0~1
>
> 示例：`BitOffset=15, BitLen=0, DataType=UInt16` → 提取 bit15（最后 1 位），值域 0~1
>
> CoilStatus/InputStatus 类型忽略 BitOffset 和 BitLen，每个地址即 1 个 Bool。

## 寄存器类型与功能码映射

| RegType | 读取功能码 | 写入功能码 | 数据限制 | 合并上限 |
|---------|-----------|-----------|---------|---------|
| HoldingReg | FC03 | FC06/FC16 | 数值类型 | 125 寄存器 |
| InputReg | FC04 | 只读 | 数值类型 | 125 寄存器 |
| CoilStatus | FC01 | FC05/FC15 | 仅 Bool | 2000 线圈 |
| InputStatus | FC02 | 只读 | 仅 Bool | 2000 线圈 |

## 字节序说明

Modbus 设备因厂商不同，多字节数据的存储字节序可能不同。本程序支持以下四种字节序（以 4 字节 `[A B C D]` 为例）：

| 字节序 | 含义 | 寄存器排列 | 适用场景 |
|--------|------|-----------|---------|
| ABCD | Big-Endian | R1=[AB], R2=[CD] | 默认，无需转换 |
| DCBA | Little-Endian | R1=[DC], R2=[BA] | 全字节反转 |
| CDAB | Big-Endian 字交换 | R1=[CD], R2=[AB] | 施耐德、西门子等 |
| BADC | Little-Endian 字交换 | R1=[BA], R2=[DC] | 每字内字节反转 |

> 单寄存器（16 位）在 Modbus 协议中固定大端传输，不受字节序配置影响。

## 高可用机制

```
在线 (Online)
  │ 通信异常
  ▼
重连中 (Reconnecting) ←── 指数退避: 1s → 2s → 4s → ... (上限 30s)
  │ 连续失败 10 次
  ▼
离线 (Offline)
  │ 每 60 秒心跳探测
  │ 恢复连通
  ▼
在线 (Online)
```

- **指数退避**：重连间隔按 2 的幂次递增，避免网络风暴
- **CAS 防并发**：同一时间只允许一个重连协程运行
- **熔断保护**：连续 10 次重连失败后进入离线状态，降低探测频率
- **心跳恢复**：离线期间每 60 秒尝试一次连接，自动恢复

## 依赖

- [goburrow/modbus](https://github.com/goburrow/modbus) — Modbus TCP 客户端
- [BurntSushi/toml](https://github.com/BurntSushi/toml) — TOML 配置解析
