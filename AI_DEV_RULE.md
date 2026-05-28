# AI_DEV_RULE.md — modbus-scan 实现规则

> 本文档是实现 modbus-scan 服务的完整规则集。AI 须严格遵循所有 MUST 规则实现，MAY 规则可自行决定。

---

## R1. 项目元信息

- **语言：** Go
- **模块名：** `modbus-scan`
- **Go 版本：** >= 1.21
- **外部依赖：**
  - `github.com/goburrow/modbus` v0.1.0 — Modbus TCP 客户端
  - `github.com/BurntSushi/toml` v1.6.0 — TOML 配置解析
- **构建命令：** `go build -o modbus-scan.exe ./cmd/modbus-scan`

---

## R2. 目录与包结构 (MUST)

```
modbus-scan/
├── cmd/modbus-scan/main.go        # package main — 程序入口
├── internal/config/
│   ├── device_config.go           # package config — 设备配置加载
│   └── point_config.go           # package config — 点位配置加载+校验+合并
├── internal/collector/
│   └── collector.go               # package collector — 连接管理+采集引擎+字节序+写入
├── internal/udm/
│   └── udm.go                     # package udm — 线程安全数据模型
├── configs/
│   ├── config.toml
│   └── points.csv
├── go.mod
└── go.sum
```

**包依赖约束 (MUST)：**
- `cmd/modbus-scan` → 可导入 `internal/config`, `internal/collector`, `internal/udm`
- `internal/collector` → 仅可导入 `internal/config`, `internal/udm`
- `internal/config` → 不导入其他 internal 包
- `internal/udm` → 不导入其他 internal 包
- **MUST NOT** 出现循环依赖

---

## R3. DeviceConfig 规则

### R3.1 结构体 (MUST)

```go
type DeviceConfig struct {
    Modbus struct {
        Address        string `toml:"address"`
        Port           int    `toml:"port"`
        SlaveID        byte   `toml:"slave_id"`
        ByteOrder      string `toml:"byte_order"`
        TimeoutSec     int    `toml:"timeout_sec"`
        ScanIntervalMs int    `toml:"scan_interval_ms"`
    } `toml:"modbus"`
}
```

### R3.2 加载函数 (MUST)

`func LoadDeviceConfig(filePath string) (*DeviceConfig, error)`

### R3.3 校验规则 (MUST)

| 字段 | 规则 |
|------|------|
| address | MUST 非空，否则返回 error |
| port | MUST > 0，否则返回 error |
| slave_id | MUST != 0，否则返回 error |
| byte_order | 为空时默认 "ABCD"；MUST 转大写后校验；MUST 为 ABCD/DCBA/CDAB/BADC 之一，否则返回 error |
| timeout_sec | <=0 时自动设为 5 |
| scan_interval_ms | <=0 时自动设为 2000 |

### R3.4 TOML 文件格式 (MUST)

```toml
[modbus]
address         = "127.0.0.1"
port            = 502
slave_id        = 1
byte_order      = "BADC"
timeout_sec     = 5
scan_interval_ms = 1000
```

---

## R4. PointConfig 规则

### R4.1 结构体 (MUST)

```go
type PointConfig struct {
    TagName   string
    RegType   string   // HoldingReg / InputReg / CoilStatus / InputStatus
    Address   uint16
    DataType  string   // Bool / Int16 / UInt16 / Int32 / UInt32 / Float32 / Double
    BitOffset int      // 仅寄存器类型有效
    BitLen    int      // 仅寄存器类型有效
    Scale     float64
    Offset    float64
    Writeable bool
}
```

### R4.2 辅助函数 (MUST)

```go
func IsRegTypeBit(regType string) bool      // CoilStatus 或 InputStatus 返回 true
func TypeBitWidth(dataType string) int       // Bool→1, Int16/UInt16→16, Int32/UInt32/Float32→32, Double→64
func (p *PointConfig) GetRegisterCount() uint16  // 返回占用寄存器数
```

**GetRegisterCount 映射 (MUST)：**

| DataType | 寄存器数 |
|----------|---------|
| Bool | 1 |
| Int16 / UInt16 | 1 |
| Int32 / UInt32 / Float32 | 2 |
| Double | 4 |

线圈类型 (IsRegTypeBit==true) 固定返回 1。

### R4.3 CSV 格式 (MUST)

9 列，首行为表头：

```
TagName,RegType,Address,DataType,BitOffset,BitLen,Scale,Offset,Writeable
```

### R4.4 加载函数 (MUST)

`func LoadPointsFromCSV(filePath string) ([]PointConfig, error)`

### R4.5 CSV 校验规则 (MUST — 逐行校验，失败跳过该行)

行号规则：第一行为表头跳过，第一行数据的 lineNum = 1（即 `lineNum = i`，i 从 0 起遍历 records，i=0 为表头 continue）。

**校验顺序与规则：**

| 序号 | 校验项 | 条件 | 失败处理 |
|------|--------|------|---------|
| 1 | 列数 | len(record) < 9 | 跳过 |
| 2 | TagName | 空字符串 | 跳过 |
| 3 | RegType | 不在 {HoldingReg, InputReg, CoilStatus, InputStatus} | 跳过 |
| 4 | DataType | 不在 {Bool, Int16, UInt16, Int32, UInt32, Float32, Double} | 跳过 |
| 5 | Address | 无法解析为 uint16 | 跳过 |
| 6 | BitOffset | 无法解析或 < 0（仅寄存器类型解析） | 跳过 |
| 7 | BitLen | 无法解析或 < 0（仅寄存器类型解析） | 跳过 |
| 8 | Bool 在寄存器中 | BitOffset>0 或 BitLen>0 | 跳过 |
| 9 | Float32/Double | BitOffset>0 或 BitLen>0 | 跳过 |
| 10 | BitOffset=0 且 BitLen=0 | DataType != "Bool" | 跳过（不合法） |
| 11 | BitLen 范围 | BitOffset=0: BitLen∈[1, typeBitWidth]；BitOffset>0: BitLen∈[0, typeBitWidth-1] | 超出则跳过 |
| 12 | BitOffset 范围 | BitOffset∈[0, typeBitWidth-1] | 超出则跳过 |
| 13 | BitOffset+effectiveBitLen | ≤ typeBitWidth（BitOffset>0 且 BitLen=0 时 effectiveBitLen=1） | 超出则跳过 |
| 14 | 线圈类型 DataType | != "Bool" | 跳过 |
| 15 | Scale | 无法解析为 float64 | 跳过 |
| 16 | Offset | 无法解析为 float64 | 跳过 |
| 17 | TagName 重复 | tagSet 中已存在 | 跳过 |

**关键规则 (MUST)：**
- 线圈类型 (CoilStatus/InputStatus) 不解析 BitOffset 和 BitLen，校验规则 6~13 对线圈类型不适用
- BitOffset=0 且 BitLen=0 对非 Bool 寄存器类型 **MUST 拒绝**

**失败日志格式 (MUST)：**
`[WARN] CSV 校验跳过: 第 N 行 [TagName]: 具体错误描述`

**最终行为 (MUST)：**
- 有 ≥1 个有效点位 → 返回 `([]PointConfig, nil)`，日志 `[INFO] 成功加载 N 个有效点位配置 (跳过 M 行错误)`
- 0 个有效点位 → 返回 `(nil, error("CSV 配置验证失败: 没有有效的点位配置"))`

---

## R5. 地址合并优化 (MUST)

### R5.1 数据结构

```go
type ReadChunk struct {
    StartAddr uint16
    Quantity  uint16
    Points    []*PointConfig
}
```

### R5.2 函数

`func OptimizeChunks(points []PointConfig) map[string][]ReadChunk`

### R5.3 算法 (MUST 严格遵循)

```
1. 按 RegType 分组 → map[string][]*PointConfig
2. 每组内按 Address 升序排序
3. 合并上限：寄存器类型 125，线圈类型 2000
4. 初始化：
   currentChunk.StartAddr = pts[0].Address
   chunkEndAddr = pts[0].Address + pts[0].GetRegisterCount() - 1
   currentChunk.Points = [pts[0]]
5. 遍历 pts[1:]:
   nextAddr = pts[i].Address
   nextEndAddr = pts[i].Address + pts[i].GetRegisterCount() - 1
   totalQty = nextEndAddr - currentChunk.StartAddr + 1
   IF nextAddr <= chunkEndAddr + 1 AND totalQty <= maxQty:
       合入当前 chunk
       IF nextEndAddr > chunkEndAddr: chunkEndAddr = nextEndAddr
   ELSE:
       currentChunk.Quantity = chunkEndAddr - currentChunk.StartAddr + 1
       输出当前 chunk
       开启新 chunk
6. 最后一个 chunk 收尾
```

---

## R6. 连接管理器 (ConnManager)

### R6.1 状态常量 (MUST)

```go
StateOnline       int32 = 0
StateReconnecting int32 = 1
StateOffline      int32 = 2
```

### R6.2 结构体 (MUST)

```go
type ConnManager struct {
    cfg          *config.DeviceConfig
    client       modbus.Client
    handler      *modbus.TCPClientHandler
    mu           sync.Mutex
    state        int32           // 原子操作
    retryCount   int32           // 原子操作
    maxRetries   int32           // 固定 10
    reconnecting int32           // CAS 原子标志，防并发重连
}
```

### R6.3 Connect (MUST)

```go
func (cm *ConnManager) Connect() error
```
- 创建 TCPClientHandler，设置地址 `host:port`
- 设置 Timeout = cfg.TimeoutSec 秒
- 设置 SlaveId = cfg.SlaveID
- 调用 handler.Connect()
- 创建 client = modbus.NewClient(handler)

### R6.4 ReconnectWithBackoff (MUST)

```go
func (cm *ConnManager) ReconnectWithBackoff()
```

**规则 (MUST)：**
1. CAS 原子操作 `CompareAndSwapInt32(&reconnecting, 0, 1)` 失败则直接返回
2. defer 恢复 `reconnecting = 0`
3. 加锁关闭旧连接，设 client=nil，设 state=StateReconnecting
4. 循环重连，最多 maxRetries (10) 次：
   - 退避延迟 = `min(1s * 2^(attempt-1), 30s)`
   - 尝试 Connect()
   - 成功：retryCount=0, state=StateOnline, 返回
   - 失败：retryCount=attempt, 继续
5. 超过 maxRetries：
   - retryCount=0, state=StateOffline
   - `go startHeartbeatProbe()`
   - 返回

### R6.5 startHeartbeatProbe (MUST)

```go
func (cm *ConnManager) startHeartbeatProbe()
```

- 每 60 秒尝试 Connect() 一次
- 退出条件：`state != StateOffline`
- 恢复后：retryCount=0, state=StateOnline

### R6.6 GetClient (MUST)

- state==StateOffline 或 StateReconnecting → 返回 nil
- state==StateOnline → 加锁返回 client

---

## R7. 采集引擎 (Collector)

### R7.1 结构体 (MUST)

```go
type Collector struct {
    connMgr   *ConnManager
    udm       *udm.UniversalDataModel
    chunks    map[string][]config.ReadChunk
    byteOrder string   // 大写
}
```

### R7.2 ScanLoop (MUST)

```go
func (c *Collector) ScanLoop(interval time.Duration)
```
- `time.NewTicker(interval)`
- 每个 tick 调用 `scanOnce()`

### R7.3 scanOnce (MUST)

```
1. client = connMgr.GetClient()
2. IF client == nil: return
3. FOR 每个 RegType 的每个 ReadChunk:
   a. 按 RegType 调用读取:
      HoldingReg  → client.ReadHoldingRegisters(chunk.StartAddr, chunk.Quantity)
      InputReg    → client.ReadInputRegisters(chunk.StartAddr, chunk.Quantity)
      CoilStatus  → client.ReadCoils(chunk.StartAddr, chunk.Quantity)
      InputStatus → client.ReadDiscreteInputs(chunk.StartAddr, chunk.Quantity)
   b. IF err != nil:
      log "[ERROR] Modbus 通信异常"
      go connMgr.ReconnectWithBackoff()
      return    ← MUST 立即退出，跳过本周期剩余所有 chunk
   c. parseAndUpdate(chunk, results)
4. udm.LogAll()
```

### R7.4 parseAndUpdate — 线圈类型 (MUST)

```go
// CoilStatus / InputStatus
bitIdx    = pt.Address - chunk.StartAddr
byteIdx   = bitIdx / 8
bitOffset = bitIdx % 8
// 越界检查
IF byteIdx >= len(raw): log WARN, continue
finalVal = (raw[byteIdx] >> bitOffset) & 0x01 == 1   // bool
```

### R7.5 parseAndUpdate — 寄存器类型 (MUST 严格遵循 7 步)

**步骤 1：提取字节**
```
byteOffset    = (pt.Address - chunk.StartAddr) * 2
requiredBytes = pt.GetRegisterCount() * 2
// 越界检查
IF byteOffset + requiredBytes > len(raw): log WARN, continue
dataSlice = raw[byteOffset : byteOffset + requiredBytes]
```

**步骤 2：字节序转换**
```
reorderedData = applyGlobalByteOrder(dataSlice)
```

**步骤 3：转 uint64**
```
len==2 → rawUint = uint64(binary.BigEndian.Uint16(reorderedData))
len==4 → rawUint = uint64(binary.BigEndian.Uint32(reorderedData))
len==8 → rawUint = binary.BigEndian.Uint64(reorderedData)
```

**步骤 4：计算 effectiveBitLen**
```
effectiveLen = pt.BitLen
IF pt.BitOffset > 0 AND effectiveLen == 0:
    effectiveLen = 1
IF effectiveLen == 0:
    effectiveLen = TypeBitWidth(pt.DataType)   // 仅 Bool 类型会走到这里
```

**步骤 5：位提取**
```
mask      = (1 << effectiveLen) - 1
extracted = (rawUint >> pt.BitOffset) & mask
```

**步骤 6：DataType 转换**
```
Bool:    finalVal = (extracted == 1)
Int16:   IF effectiveLen==16 → float64(int16(extracted)) * Scale + Offset   // 有符号
         ELSE               → float64(extracted) * Scale + Offset           // 无符号
UInt16:  finalVal = float64(extracted) * Scale + Offset
Int32:   IF effectiveLen==32 → float64(int32(extracted)) * Scale + Offset   // 有符号
         ELSE               → float64(extracted) * Scale + Offset           // 无符号
UInt32:  finalVal = float64(extracted) * Scale + Offset
Float32: finalVal = float64(math.Float32frombits(uint32(extracted))) * Scale + Offset
Double:  finalVal = math.Float64frombits(extracted) * Scale + Offset
```

**步骤 7：写入 UDM**
```
udm.Update(pt.TagName, finalVal)
```

### R7.6 工程值公式 (MUST)

`工程值 = 原始值 × Scale + Offset`

---

## R8. 字节序转换 (MUST)

```go
func (c *Collector) applyGlobalByteOrder(data []byte) []byte
```

### R8.1 规则

- byteOrder=="ABCD" 或空 → 不转换，返回 data
- **2 字节（1 个寄存器）→ MUST NOT 转换**（Modbus 协议固定大端传输）
- 4 字节和 8 字节按以下映射转换：

### R8.2 4 字节映射 (MUST)

原始顺序 `[A, B, C, D]`（R1=[AB], R2=[CD]）：

| byte_order | 结果 |
|-----------|------|
| DCBA | `[D, C, B, A]` — 全字节反转 |
| CDAB | `[C, D, A, B]` — 交换 R1↔R2 |
| BADC | `[B, A, D, C]` — 每字内字节互换 |

### R8.3 8 字节映射 (MUST)

原始顺序 `[R1_H, R1_L, R2_H, R2_L, R3_H, R3_L, R4_H, R4_L]`：

| byte_order | 操作 |
|-----------|------|
| DCBA | 全字节反转 `[data[7],data[6],...,data[0]]` |
| CDAB | R1↔R2, R3↔R4：`[data[2],data[3],data[0],data[1],data[6],data[7],data[4],data[5]]` |
| BADC | 每字内字节互换：`[data[1],data[0],data[3],data[2],data[5],data[4],data[7],data[6]]` |

---

## R9. BitOffset / BitLen 语义 (MUST)

### R9.1 寄存器类型

| BitOffset | BitLen | effectiveBitLen | 含义 |
|-----------|--------|-----------------|------|
| 0 | ≥1 | BitLen | 从 bit0 开始提取 BitLen 位 |
| >0 | 0 | 1 | 提取 BitOffset 位置的 1 位 |
| >0 | ≥1 | BitLen | 从 BitOffset 开始提取 BitLen 位 |
| 0 | 0 | — | **不合法**（Bool 除外） |

**位编号：** bit0 = LSB，bit15 = MSB（UInt16），与 Go 位运算一致。

### R9.2 线圈类型

忽略 BitOffset 和 BitLen，每个地址即 1 个 Bool。

### R9.3 示例

| BitOffset | BitLen | DataType | 提取范围 | 值域 |
|-----------|--------|----------|---------|------|
| 0 | 1 | UInt16 | bit0 | 0~1 |
| 0 | 16 | Int16 | bit0~bit15 全部 | int16 范围 |
| 4 | 4 | UInt16 | bit4~bit7 | 0~15 |
| 15 | 0 | UInt16 | bit15 | 0~1 |

---

## R10. 反向写入 (MUST)

```go
func (c *Collector) WriteTag(tagName string, value interface{}) error
```

### R10.1 前置检查 (MUST 按顺序)

1. findPoint(tagName) → nil 则返回 `"tag not found"`
2. Writeable==false → 返回 `"tag is read-only"`
3. GetClient()==nil → 返回 `"device offline"`

### R10.2 写入映射 (MUST)

| value 类型 | target RegType | 操作 |
|-----------|---------------|------|
| uint16 | HoldingReg | WriteSingleRegister(address, value) |
| int16 | HoldingReg | WriteSingleRegister(address, uint16(value)) |
| int | HoldingReg | 范围校验 0~65535, WriteSingleRegister |
| bool | CoilStatus | WriteSingleCoil(address, 0xFF00/0x0000) |
| 其他 | — | 返回 `"unsupported write type"` |

**MUST NOT** 支持 FC16 多寄存器写入。

---

## R11. UDM 规则 (MUST)

```go
type UniversalDataModel struct {
    mu   sync.RWMutex
    data map[string]interface{}
}
```

| 方法 | 锁 | 说明 |
|------|----|------|
| `New() *UniversalDataModel` | — | 初始化 data map |
| `Update(tag string, val interface{})` | 写锁 | 写入/覆盖 |
| `Get(tag string) (interface{}, bool)` | 读锁 | 读取 |
| `GetAll() map[string]interface{}` | 读锁 | 深拷贝快照 |
| `LogAll()` | 读锁 | 输出 `[DATA] TagName = value` |

---

## R12. 启动流程 (MUST)

### R12.1 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| -config | config.toml | TOML 配置路径 |
| -csv | points.csv | CSV 点位路径 |
| -validate | false | 仅验证 CSV |

### R12.2 验证模式

```
IF -validate:
    points, err = LoadPointsFromCSV(csvFile)
    IF err: stderr "[FAIL]", exit(1)
    ELSE: stdout "[OK] N 个有效点位" + 格式化表格, exit(0)
```

### R12.3 正常模式 (MUST 按顺序)

```
1. cfg = LoadDeviceConfig(configFile)           → 失败则 log.Fatalf
2. connMgr = NewConnManager(cfg)
3. connMgr.Connect()                            → 失败则 log.Fatalf
4. points = LoadPointsFromCSV(csvFile)           → 失败则 log.Fatalf
5. chunks = OptimizeChunks(points)
6. udmInst = udm.New()
7. collector = NewCollector(connMgr, udmInst, chunks, cfg)
8. go collector.ScanLoop(scanInterval)
9. 监听 SIGINT/SIGTERM → log 退出信息
```

---

## R13. Modbus 功能码映射 (MUST)

| RegType | 读取方法 | 写入方法 | 数据限制 | 批量上限 |
|---------|---------|---------|---------|---------|
| HoldingReg | ReadHoldingRegisters (FC03) | WriteSingleRegister (FC06) | 数值类型 | 125 寄存器 |
| InputReg | ReadInputRegisters (FC04) | 只读 | 数值类型 | 125 寄存器 |
| CoilStatus | ReadCoils (FC01) | WriteSingleCoil (FC05) | 仅 Bool | 2000 线圈 |
| InputStatus | ReadDiscreteInputs (FC02) | 只读 | 仅 Bool | 2000 线圈 |

---

## R14. 日志规范 (MUST)

| 前缀 | 级别 | 场景 |
|------|------|------|
| `[INFO]` | 正常 | 启动、连接成功、重连成功、配置加载、心跳恢复 |
| `[WARN]` | 警告 | CSV 校验跳过、重连等待、未知 RegType、数据越界 |
| `[ERROR]` | 错误 | 通信异常、熔断触发 |
| `[DATA]` | 数据 | 每周期采集值 |

---

## R15. 关键约束清单 (MUST)

1. **单寄存器不转字节序** — 2 字节数据 applyGlobalByteOrder 必须返回原始 data
2. **Bool 在寄存器中** — BitOffset=0, BitLen=0 合法，effectiveBitLen=1
3. **通信异常即终止** — scanOnce 中任何 chunk 读取失败，MUST 立即 return，不再处理后续 chunk
4. **CSV 校验非阻塞** — 失败行仅 WARN 跳过，不阻断整体加载，≥1 有效点位即可
5. **TagName 全局唯一** — 重复的 TagName 第二个出现 MUST 被跳过
6. **CAS 防并发重连** — 同一时间 MUST 只有一个重连协程运行
7. **离线不采集** — GetClient 返回 nil 时 scanOnce 直接返回
8. **心跳协程不泄漏** — startHeartbeatProbe 在 state!=Offline 时自动退出
9. **有符号转换条件** — Int16 仅 effectiveLen==16 做有符号，Int32 仅 effectiveLen==32 做有符号
10. **写入仅 FC06/FC05** — 不支持 FC16 多寄存器写入

---

## R16. 配置文件示例

### config.toml

```toml
[modbus]
address         = "127.0.0.1"
port            = 502
slave_id        = 1
byte_order      = "BADC"
timeout_sec     = 5
scan_interval_ms = 1000
```

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
