# AI_DEV.md — modbus-scan 开发规格文档

> 本文档面向 AI 辅助开发，涵盖所有业务理解、算法细节和边界条件，确保 AI 可据此完整实现本服务。

---

## 1. 项目概述

**modbus-scan** 是一个工业级 Modbus TCP 数据采集服务，核心职责：

1. 从 CSV 文件读取点位配置
2. 将离散点位合并为批量读取块（减少网络 IO）
3. 周期性从 Modbus 从站设备批量读取寄存器/线圈数据
4. 解析原始字节 → 字节序转换 → 位提取 → 数据类型转换
5. 将解析后的设备原始值写入线程安全的通用数据模型 (UDM)
6. 支持反向写入（HoldingReg 写数值、CoilStatus 写线圈）
7. 具备断线重连（指数退避）+ 熔断（离线探测）+ 心跳恢复

---

## 2. 项目结构

```
modbus-scan/
├── cmd/
│   └── modbus-scan/
│       └── main.go                  # 程序入口：参数解析、组装调用
├── internal/
│   ├── config/
│   │   ├── device_config.go         # DeviceConfig 结构体 + TOML 加载/校验
│   │   └── point_config.go          # PointConfig 结构体 + CSV 加载/校验 + 合并优化
│   ├── collector/
│   │   └── collector.go             # ConnManager (连接管理) + Collector (采集引擎) + 字节序转换 + 反向写入
│   └── udm/
│       └── udm.go                   # UniversalDataModel (线程安全内存数据模型)
├── configs/
│   ├── config.toml                  # 默认设备连接配置
│   ├── points.csv                   # 默认点位配置
│   └── points-coil.csv              # 线圈点位配置示例
├── build.bat                        # Windows 构建脚本
├── go.mod                           # module modbus-scan
└── go.sum
```

### 2.1 包依赖关系

```
cmd/modbus-scan (main)
  ├── internal/config       (LoadDeviceConfig, LoadPointsFromCSV, OptimizeChunks)
  ├── internal/collector    (NewConnManager, NewCollector)
  └── internal/udm          (New)

internal/collector
  ├── internal/config       (PointConfig, ReadChunk, IsRegTypeBit, TypeBitWidth)
  └── internal/udm          (UniversalDataModel)

internal/config — 无内部依赖
internal/udm    — 无内部依赖
```

### 2.2 外部依赖

| 依赖 | 版本 | 用途 |
|------|------|------|
| `github.com/goburrow/modbus` | v0.1.0 | Modbus TCP 客户端（连接、读写寄存器/线圈） |
| `github.com/BurntSushi/toml` | v1.6.0 | TOML 配置文件解析 |

---

## 3. 数据流（端到端）

```
config.toml ──→ LoadDeviceConfigs() ──→ []*DeviceConfig
                       │
                       ├── enabled=false 的设备：跳过采集启动
                       └── enabled=true 或省略 enabled 的设备：加载点位并启动采集
                                │
                                ▼
points.csv / device.csv ──→ LoadPointsFromCSV() ──→ []PointConfig
                     │
                     ├── 校验失败的行：log.Printf("[WARN] CSV 校验跳过: 第 N 行 [TagName]: ...")，跳过
                     └── 校验通过的行：加入 points 列表
                            │
                            ▼
                  OptimizeChunks(points)
                            │
                            ▼
              map[string][]ReadChunk   ← 按 RegType 分组，同组内按地址排序，连续地址合并
              {
                "HoldingReg": [{StartAddr:0, Qty:10, Points:[...]}, ...],
                "InputReg":   [...],
                "CoilStatus": [...],
              }
                            │
                            ▼
               NewCollector(connMgr, udm, chunks, cfg)
                            │
                            ▼
               ScanLoop(scanInterval)    ← 定时触发
                   │
                   ▼ (每个周期)
               scanOnce()
                   │
                   ├── connMgr.GetClient() → nil? 跳过本周期
                   │
                   ├── 遍历每个 RegType 的每个 ReadChunk
                   │     │
                   │     ├── 按 RegType 调用对应 Modbus 功能码读取
                   │     │     HoldingReg  → ReadHoldingRegisters (FC03)
                   │     │     InputReg    → ReadInputRegisters    (FC04)
                   │     │     CoilStatus  → ReadCoils             (FC01)
                   │     │     InputStatus → ReadDiscreteInputs    (FC02)
                   │     │
                   │     ├── 通信异常 → go connMgr.ReconnectWithBackoff() → return (跳过本周期剩余)
                   │     │
                   │     └── 通信成功 → parseAndUpdate(chunk, rawBytes)
                   │           │
                   │           ▼ (对 chunk 中每个 PointConfig)
                   │         ┌─────────────────────────────────┐
                   │         │  线圈类型 (CoilStatus/InputStatus) │
                   │         │  bitIdx = pt.Address - chunk.StartAddr
                   │         │  byteIdx = bitIdx / 8
                   │         │  bitOffset = bitIdx % 8
                   │         │  finalVal = (raw[byteIdx] >> bitOffset) & 0x01 == 1
                   │         └─────────────────────────────────┘
                   │         ┌───────────────────────────────────────────────────┐
                   │         │  寄存器类型 (HoldingReg/InputReg)                   │
                   │         │  1. byteOffset = (pt.Address - chunk.StartAddr) * 2
                   │         │  2. dataSlice = raw[byteOffset : byteOffset + regCount*2]
                   │         │  3. reorderedData = applyGlobalByteOrder(dataSlice)
                   │         │  4. rawUint = 按长度转 uint16/uint32/uint64 → uint64
                   │         │  5. effectiveLen = 计算 effectiveBitLen
                   │         │  6. extracted = (rawUint >> BitOffset) & ((1<<effectiveLen)-1)
                   │         │  7. 按 DataType 转换 → finalVal
                   │         └───────────────────────────────────────────────────┘
                   │              │
                   │              ▼
                   │         udm.Update(pt.TagName, finalVal)
                   │
                   └── udm.LogAll()   ← 输出本周期所有采集值
```

---

## 4. 配置规格

### 4.1 DeviceConfig (config.toml)

```toml
[[devices]]
name              = "plc-1"           # 可选，默认 device-N
enabled           = true              # 可选，默认 true；false 时不启动采集
address           = "192.168.1.100"   # 必填，设备 IP
port              = 502               # 必填，>0
slave_id          = 1                 # 必填，1~247
byte_order        = "ABCD"            # 可选，默认 ABCD，可选: ABCD/DCBA/CDAB/BADC
timeout_sec       = 5                 # 可选，默认 5，>0
scan_interval_ms  = 2000              # 可选，默认 2000，>0
csv               = "configs/points.csv" # 可选，省略时使用命令行 -csv
```

**校验规则：**
- `[[devices]]` 支持多设备；旧版 `[modbus]` 单设备配置保持兼容
- `enabled` 省略时默认 `true`，为 `false` 时服务启动时跳过该设备采集
- `csv` 为空时使用命令行 `-csv` 指定的默认点位文件
- `address` 和 `port` 必须有效（address 非空, port > 0），否则返回 error
- `slave_id` 不能为 0，否则返回 error
- `timeout_sec <= 0` 时自动设为 5
- `scan_interval_ms <= 0` 时自动设为 2000
- `byte_order` 为空时默认 "ABCD"，统一转大写后校验
- `byte_order` 必须是 ABCD/DCBA/CDAB/BADC 之一

### 4.2 PointConfig (points.csv)

CSV 列顺序（8列，第一行为表头）：

| 列序号 | 列名 | 类型 | 说明 |
|--------|------|------|------|
| 0 | TagName | string | 全局唯一标识，必填，不能为空 |
| 1 | RegType | string | HoldingReg / InputReg / CoilStatus / InputStatus |
| 2 | Address | uint16 | 0~65535 |
| 3 | DataType | string | Bool / Int16 / UInt16 / Int32 / UInt32 / Float32 / Double |
| 4 | BitOffset | int | 位偏移（仅寄存器类型有效） |
| 5 | BitLen | int | 位长度（仅寄存器类型有效） |
| 6 | Writeable | int | 0（只读）或 1（允许写入） |
| 7 | Description | string | 可为空，最多 255 个字符 |

**CSV 校验规则（逐行校验，失败的行输出 WARN 并跳过，仅加载通过校验的行）：**

1. 列数不是 8 列 → 跳过
2. TagName 为空 → 跳过
3. RegType 不在有效集合 → 跳过
4. DataType 不在有效集合 → 跳过
5. Address 无法解析为 uint16 → 跳过
6. BitOffset 无法解析或 < 0 → 跳过
7. BitLen 无法解析或 < 0 → 跳过
8. **寄存器类型 (HoldingReg/InputReg) 特殊规则：**
   - Bool 类型：BitOffset 和 BitLen 只能为 0
   - Float32/Double 类型：BitOffset 和 BitLen 必须为 0（不支持位提取）
   - BitOffset=0 且 BitLen=0：**不合法**（Bool 除外）
   - BitLen 范围校验：
     - BitOffset=0 时：BitLen 范围 1 ~ typeBitWidth
     - BitOffset>0 时：BitLen 范围 0 ~ (typeBitWidth - 1)，0 表示读取该偏移位 1 位
   - BitOffset 范围：0 ~ (typeBitWidth - 1)
   - BitOffset + effectiveBitLen ≤ typeBitWidth（其中 BitOffset>0 且 BitLen=0 时 effectiveBitLen=1）
9. **线圈类型 (CoilStatus/InputStatus) 规则：**
   - 只允许 DataType=Bool
   - 忽略 BitOffset 和 BitLen（解析时不读取这两列）
10. TagName 重复 → 跳过

**行号规则：** 第一行为表头，跳过。第一行数据的行号为 1（即 `lineNum = i`，其中 i 从 0 开始，i=0 是表头被跳过）。

**校验失败日志格式：** `[WARN] CSV 校验跳过: 第 N 行 [TagName]: 具体错误描述`（TagName 为空时仅显示行号）。

**最终行为：**
- 至少有 1 个有效点位 → 返回 `([]PointConfig, nil)`
- 0 个有效点位 → 返回 `(nil, error)`
- 日志输出：`[INFO] 成功加载 N 个有效点位配置 (跳过 M 行错误)`

### 4.3 数据类型位宽与寄存器占用

| DataType | typeBitWidth | 占用寄存器数 (GetRegisterCount) |
|----------|-------------|-------------------------------|
| Bool | 1 | 1（线圈类型）/ 1（寄存器中 Bool 无意义，会被校验拦住） |
| Int16 | 16 | 1 |
| UInt16 | 16 | 1 |
| Int32 | 32 | 2 |
| UInt32 | 32 | 2 |
| Float32 | 32 | 2 |
| Double | 64 | 4 |

---

## 5. 地址合并优化算法 (OptimizeChunks)

**目的：** 将离散的点位配置合并为尽可能少的批量读取块，减少 Modbus 网络请求次数。

**算法步骤：**

1. 按 RegType 分组
2. 每组内按 Address 升序排序
3. 初始化第一个 chunk：StartAddr = pts[0].Address，chunkEndAddr = pts[0].Address + pts[0].GetRegisterCount() - 1
4. 遍历后续点位：
   - `nextAddr = pts[i].Address`
   - `nextEndAddr = pts[i].Address + pts[i].GetRegisterCount() - 1`
   - `totalQty = nextEndAddr - currentChunk.StartAddr + 1`
   - 如果 `nextAddr <= chunkEndAddr + 1`（连续或重叠）**且** `totalQty <= maxQty` → 合入当前 chunk
   - 否则 → 当前 chunk 结束（Quantity = chunkEndAddr - StartAddr + 1），开启新 chunk
5. 最后一个 chunk 收尾

**合并上限：**
- 寄存器类型 (HoldingReg/InputReg)：每块最多 **125** 个寄存器
- 线圈类型 (CoilStatus/InputStatus)：每块最多 **2000** 个线圈

**重叠处理：** 如果两个点位地址范围有重叠（如 Int32 从地址 0 占用 0~1，UInt16 从地址 1 占用 1），它们会被合并到同一个 chunk 中，各自独立解析。

---

## 6. 连接管理器 (ConnManager)

### 6.1 状态机

```
                ┌──────────────┐
                │   Online     │ ← 初始连接成功 / 重连成功 / 心跳恢复
                │  state=0     │
                └──────┬───────┘
                       │ 通信异常
                       ▼
                ┌──────────────┐
                │ Reconnecting │ ← CAS 原子操作保证仅一个重连协程
                │  state=1     │
                └──────┬───────┘
                       │ 连续失败 10 次 (maxRetries)
                       ▼
                ┌──────────────┐
                │   Offline    │ → go startHeartbeatProbe()
                │  state=2     │
                └──────┬───────┘
                       │ 心跳探测成功
                       ▼
                ┌──────────────┐
                │   Online     │
                │  state=0     │
                └──────────────┘
```

### 6.2 指数退避重连 (ReconnectWithBackoff)

- **CAS 防并发：** `atomic.CompareAndSwapInt32(&cm.reconnecting, 0, 1)` — 确保同一时间只有一个重连协程
- **退避公式：** `delay = min(baseDelay * 2^(attempt-1), maxDelay)`，其中 baseDelay=1s, maxDelay=30s
- **退避序列：** 1s, 2s, 4s, 8s, 16s, 30s, 30s, 30s, 30s, 30s（共 10 次）
- **熔断触发：** 连续失败 10 次后，state → Offline，启动心跳探测协程
- **重连成功：** retryCount 清零，state → Online

### 6.3 心跳探测 (startHeartbeatProbe)

- 触发条件：进入 Offline 状态时自动启动
- 探测频率：每 60 秒一次
- 探测方式：调用 `cm.Connect()` 尝试建立连接
- 恢复条件：连接成功 → retryCount 清零，state → Online，探测协程退出
- 退出条件：state 不再是 Offline 时退出（防止状态已变化后仍继续探测）

### 6.4 GetClient

- StateOffline 或 StateReconnecting 时返回 nil（调用方跳过采集）
- StateOnline 时返回当前 client（加锁读取）

---

## 7. 采集引擎 (Collector)

### 7.1 ScanLoop

- 使用 `time.NewTicker(interval)` 定时触发
- 每个周期调用 `scanOnce()`

### 7.2 scanOnce

1. 获取 client → nil 则跳过
2. 遍历所有 RegType 的所有 ReadChunk
3. 按 RegType 调用对应 Modbus 读取方法
4. **通信异常处理：** `go connMgr.ReconnectWithBackoff()` → **立即 return**，跳过本周期剩余所有 chunk
5. 通信成功 → `parseAndUpdate(chunk, results)`
6. 所有 chunk 处理完毕 → `udm.LogAll()`

### 7.3 parseAndUpdate — 数据解析核心

#### 7.3.1 线圈类型 (CoilStatus/InputStatus)

```
bitIdx    = pt.Address - chunk.StartAddr    // 该点位在批量读取中的线圈偏移
byteIdx   = bitIdx / 8                      // 所在字节索引
bitOffset = bitIdx % 8                      // 所在字节内的位偏移

Modbus 规范：第一个请求的线圈对应响应第一个字节的 bit 0 (LSB)

finalVal = (raw[byteIdx] >> bitOffset) & 0x01 == 1   // bool 类型
```

#### 7.3.2 寄存器类型 (HoldingReg/InputReg) — 统一位提取路径

**步骤 1：提取原始字节**

```
byteOffset    = (pt.Address - chunk.StartAddr) * 2
requiredBytes = pt.GetRegisterCount() * 2
dataSlice     = raw[byteOffset : byteOffset + requiredBytes]
```

**步骤 2：字节序转换**

```
reorderedData = applyGlobalByteOrder(dataSlice)
```

**步骤 3：转为 uint64**

```
len(reorderedData) == 2 → rawUint = uint64(binary.BigEndian.Uint16(reorderedData))
len(reorderedData) == 4 → rawUint = uint64(binary.BigEndian.Uint32(reorderedData))
len(reorderedData) == 8 → rawUint = binary.BigEndian.Uint64(reorderedData)
```

**步骤 4：计算 effectiveBitLen**

```
effectiveLen = pt.BitLen
if pt.BitOffset > 0 && effectiveLen == 0:
    effectiveLen = 1          // BitOffset>0, BitLen=0 → 读取 1 位
if effectiveLen == 0:
    effectiveLen = typeBitWidth(pt.DataType)   // Bool 类型的 fallback (BitOffset=0, BitLen=0)
```

**步骤 5：位提取**

```
mask      = (1 << effectiveLen) - 1
extracted = (rawUint >> pt.BitOffset) & mask
```

**步骤 6：按 DataType 转换为设备原始值**

```
Bool:    finalVal = (extracted == 1)                          → bool
Int16:   effectiveLen==16 → float64(int16(extracted))   // 有符号
         effectiveLen<16  → float64(extracted)          // 无符号
UInt16:  finalVal = float64(extracted)
Int32:   effectiveLen==32 → float64(int32(extracted))   // 有符号
         effectiveLen<32  → float64(extracted)          // 无符号
UInt32:  finalVal = float64(extracted)
Float32: finalVal = float64(math.Float32frombits(uint32(extracted)))
Double:  finalVal = math.Float64frombits(extracted)
```

**最终值语义：** 保存设备原始值经过字节序、位提取和数据类型解释后的结果，不执行 Scale/Offset 工程值转换。

**关键语义：**
- Int16/Int32 在 effectiveBitLen 等于类型完整位宽时才做有符号转换，否则按无符号处理
- Float32/Double 不做位提取（校验层已拦截 BitOffset/BitLen > 0），直接用完整位宽解析

---

## 8. 字节序转换 (applyGlobalByteOrder)

**前提：** Modbus 协议中，单寄存器（16 位）固定大端传输，不受 byte_order 配置影响。

**4 字节（2 个寄存器）原始顺序：** `[R1_H, R1_L, R2_H, R2_L]`，即 `[A, B, C, D]`

| byte_order | 含义 | 转换操作 | 结果 |
|-----------|------|---------|------|
| ABCD | Big-Endian | 不转换 | `[A, B, C, D]` |
| DCBA | Little-Endian | 全字节反转 | `[D, C, B, A]` |
| CDAB | Big-Endian 字交换 | R1↔R2 交换 | `[C, D, A, B]` |
| BADC | Little-Endian 字交换 | 每字内字节互换 | `[B, A, D, C]` |

**8 字节（4 个寄存器）原始顺序：** `[R1_H, R1_L, R2_H, R2_L, R3_H, R3_L, R4_H, R4_L]`

| byte_order | 转换操作 |
|-----------|---------|
| ABCD | 不转换 |
| DCBA | 全字节反转 `[7↔0, 6↔1, 5↔2, 4↔3]` |
| CDAB | R1↔R2, R3↔R4（每对相邻 16 位字互换） |
| BADC | 每个 16 位字内高低字节互换 |

**2 字节（1 个寄存器）：** 不转换（Modbus 固定大端）。

---

## 9. BitOffset / BitLen 完整语义

### 9.1 寄存器类型 (HoldingReg/InputReg)

| BitOffset | BitLen | effectiveBitLen | 含义 | 示例 (UInt16) |
|-----------|--------|-----------------|------|--------------|
| 0 | 1 | 1 | 提取 bit0 | 值域 0~1 |
| 0 | 4 | 4 | 提取 bit0~bit3 | 值域 0~15 |
| 0 | 16 | 16 | 提取全部 16 位 | 完整 UInt16 |
| 3 | 1 | 1 | 提取 bit3 | 值域 0~1 |
| 4 | 4 | 4 | 提取 bit4~bit7 | 值域 0~15 |
| 15 | 0 | 1 | 提取 bit15（最后 1 位） | 值域 0~1 |
| 0 | 0 | — | **不合法** | 校验拦截 |

**位编号约定：** bit0 = 最低位 (LSB)，bit15 = 最高位 (MSB)，与 Go 的位运算一致。

### 9.2 线圈类型 (CoilStatus/InputStatus)

- 忽略 BitOffset 和 BitLen
- 每个地址即 1 个 Bool 值

### 9.3 特殊约束

- **Bool 在寄存器中：** BitOffset=0, BitLen=0（校验层已允许，采集时 effectiveBitLen = typeBitWidth("Bool") = 1）
- **Float32/Double：** BitOffset=0, BitLen=0（不支持位提取，校验层强制）

---

## 10. 反向写入 (WriteTag)

### 10.1 写入类型映射

| value 类型 | RegType | Modbus 功能码 | 说明 |
|-----------|---------|-------------|------|
| uint16 | HoldingReg | FC06 (WriteSingleRegister) | 写单个保持寄存器 |
| int16 | HoldingReg | FC06 | 转为 uint16 写入 |
| int | HoldingReg | FC06 | 范围校验 0~65535 后写入 |
| bool | CoilStatus | FC05 (WriteSingleCoil) | true=0xFF00, false=0x0000 |
| 其他 | — | — | 返回 "unsupported write type" 错误 |

### 10.2 写入前提

1. 点位必须存在（findPoint 遍历所有 chunk 查找）
2. 点位 Writeable 必须为 1
3. 设备必须在线（GetClient 非 nil）

---

## 11. UDM (UniversalDataModel)

- **数据结构：** `map[string]interface{}`，key = TagName，value = 解析后的设备原始值
- **线程安全：** `sync.RWMutex` — Update 用写锁，Get/GetAll 用读锁
- **Update(tag, val)：** 写入/覆盖指定标签的值
- **Get(tag)：** 读取指定标签的值，返回 `(interface{}, bool)`
- **GetAll()：** 返回当前所有数据的快照（深拷贝 map），用于批量导出
- **LogAll()：** 遍历输出所有标签的值，格式 `[DATA] TagName = value`
- **LogAllWithDevice(deviceName)：** 遍历输出所有标签的值，采集日志格式 `[DATA] deviceName.TagName = value`

---

## 12. 启动流程与命令行参数

### 12.1 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-config` | `config.toml` | TOML 设备配置文件路径 |
| `-csv` | `points.csv` | 点位 CSV 文件路径 |
| `-validate` | `false` | 仅验证 CSV，不启动采集 |

### 12.2 验证模式 (-validate)

```
1. 调用 LoadPointsFromCSV(csvFile)
2. 失败 → 输出 [FAIL] + 错误信息 → exit(1)
3. 成功 → 输出 [OK] + 有效点位数量 + 格式化表格 → exit(0)
```

### 12.3 正常启动模式

```
1. LoadDeviceConfigs(configFile, csvFile) → 失败则 fatal
2. 遍历设备列表：enabled=false 的设备跳过
3. 为每个启用设备选择点位文件：device.csv 优先，否则使用命令行 -csv
4. LoadPointsFromCSV(deviceCSV)      → 失败则记录错误并继续下一个设备
5. NewConnManager(cfg)
6. connMgr.Connect()                 → 失败则记录错误并继续下一个设备
7. OptimizeChunks(points)
8. udm.New()
9. NewCollector(connMgr, udm, chunks, cfg)
10. go c.ScanLoop(scanInterval)
11. 若没有任何设备成功启动则 fatal
12. 监听 SIGINT/SIGTERM → 优雅退出
```

---

## 13. 寄存器类型与 Modbus 功能码映射

| RegType | 读取功能码 | 写入功能码 | 数据类型限制 | 批量读取上限 |
|---------|-----------|-----------|------------|------------|
| HoldingReg | FC03 ReadHoldingRegisters | FC06 WriteSingleRegister | 数值类型 | 125 寄存器 |
| InputReg | FC04 ReadInputRegisters | 只读 | 数值类型 | 125 寄存器 |
| CoilStatus | FC01 ReadCoils | FC05 WriteSingleCoil | 仅 Bool | 2000 线圈 |
| InputStatus | FC02 ReadDiscreteInputs | 只读 | 仅 Bool | 2000 线圈 |

---

## 14. 配置文件示例

### 14.1 config.toml

```toml
[[devices]]
name             = "plc-1"
enabled          = true
address          = "127.0.0.1"
port             = 502
slave_id         = 1
byte_order       = "BADC"
timeout_sec      = 5
scan_interval_ms = 1000
csv              = "configs/points.csv"

[[devices]]
name             = "plc-2"
enabled          = false
address          = "127.0.0.1"
port             = 1503
slave_id         = 1
byte_order       = "ABCD"
timeout_sec      = 5
scan_interval_ms = 1000
csv              = "configs/points-coil.csv"
```

### 14.2 points.csv

```csv
TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description
Temperature,HoldingReg,0,Int16,0,16,0,
Pressure,HoldingReg,1,Int32,0,32,0,
AlarmBit,HoldingReg,9,UInt16,3,1,0,
StatusCode,HoldingReg,10,UInt16,4,4,0,
ModeId,HoldingReg,10,UInt16,8,3,0,
MotorSpeed,InputReg,0,UInt16,0,16,0,
RunStatus,CoilStatus,0,Bool,0,0,0,
AlarmOutput,CoilStatus,1,Bool,0,0,1,
LimitSwitch,InputStatus,0,Bool,0,0,0,
```

---

## 15. 构建与运行

```bash
# 编译
go build -o modbus-scan.exe ./cmd/modbus-scan

# 或使用构建脚本
build.bat

# 运行
modbus-scan.exe -config configs/config.toml -csv configs/points.csv

# 仅验证 CSV
modbus-scan.exe -validate -csv configs/points.csv
```

---

## 16. 日志规范

| 级别 | 前缀 | 场景 |
|------|------|------|
| INFO | `[INFO]` | 启动、连接成功、重连成功、加载配置成功、心跳恢复 |
| WARN | `[WARN]` | CSV 校验跳过行、重连等待、未知 RegType、数据越界 |
| ERROR | `[ERROR]` | 通信异常、熔断触发 |
| DATA | `[DATA]` | 每周期采集值输出 |

---

## 17. 边界条件与注意事项

1. **单寄存器字节序：** 2 字节数据不做字节序转换，因为 Modbus 协议固定大端传输
2. **Bool 在寄存器中：** 校验允许 BitOffset=0, BitLen=0，采集时 effectiveBitLen = 1
3. **地址重叠：** 多个点位可能指向同一地址的不同位（如同一个 UInt16 地址提取不同 BitOffset 的位），合并算法会正确处理
4. **通信异常即终止本周期：** 任何 chunk 读取失败后不再尝试后续 chunk，直接触发重连
5. **CSV 校验非阻塞：** 校验失败的行仅 WARN 跳过，不阻断整体加载，只要至少有 1 个有效点位
6. **TagName 全局唯一：** 重复的 TagName 会被跳过（第二个重复的出现会被拦截）
7. **WriteTag 只支持单寄存器写入：** 不支持多寄存器批量写入（FC16），仅 FC06 单寄存器和 FC05 单线圈
8. **离线期间采集暂停：** GetClient 返回 nil 时 scanOnce 直接返回，不读取任何数据
9. **心跳协程生命周期：** startHeartbeatProbe 在 state != Offline 时自动退出，不会泄漏
10. **默认配置路径：** 命令行参数 `-config` 和 `-csv` 的默认值是相对路径（`config.toml` / `points.csv`），建议运行时显式指定 `configs/` 下的路径
