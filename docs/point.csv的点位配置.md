# points.csv 点位配置与排查手册

> 本文档面向数据接入工程师，用于编写、检查和排查 `points.csv` 点位配置。
> 当前项目使用 7 列 CSV，不支持 `Scale` / `Offset` 工程值转换；采集结果是设备原始值经过字节序、位提取和数据类型解释后的值。

---

## 1. 快速模板

`points.csv` 第一行必须是表头：

```csv
TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description
```

常用示例：

```csv
TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description
Temperature,HoldingReg,0,Int16,0,16,0,温度
Pressure,HoldingReg,2,Int32,0,32,0,压力
RunStatus,HoldingReg,10,UInt16,0,1,0,运行状态
AlarmCode,HoldingReg,10,UInt16,4,4,0,报警码
MotorRunning,CoilStatus,0,Bool,0,0,0,电机运行
StartCommand,CoilStatus,1,Bool,0,0,1,启动命令
```

校验命令：

```powershell
go run ./cmd/modbus-scan -validate -csv configs/points.csv
```

看到 `[OK] CSV 验证通过` 表示整份 CSV 格式和规则通过。校验采用严格模式，任何一行错误都会使整份文件失败。

---

## 1.1 Web 管理入口

设备和点位配置存储在 SQLite，不再写入 `configs/config.toml`。启动服务后访问 `http://127.0.0.1:8080`：

1. 在设备列表添加设备。
2. 打开设备详情页。
3. 点击“导入 CSV”并选择点位文件。
4. 确认后，合法 CSV 会在一个事务中全量替换该设备点位。
5. 运行中的设备需要手动点击“重启”才能加载新点位。

任何行错误都会拒绝整份导入，原有点位不会被删除。页面会显示具体 CSV 行号和字段错误。导出功能会生成相同八列格式的 UTF-8 CSV。

---

## 2. 字段说明

| 字段 | 必填 | 示例 | 说明 |
|------|------|------|------|
| `TagName` | 是 | `Temperature` | 点位名称，同一个 CSV 内必须唯一；只允许英文、数字、下划线，且不能以数字开头。 |
| `RegType` | 是 | `HoldingReg` | Modbus 寄存器类型。 |
| `Address` | 是 | `0` | Modbus 协议地址，范围 `0~65535`。 |
| `DataType` | 是 | `Int16` | 数据类型。 |
| `BitOffset` | 是 | `0` | 位偏移，仅寄存器类型有效。线圈类型填 `0`。 |
| `BitLen` | 是 | `16` | 位长度，仅寄存器类型有效。线圈类型填 `0`。 |
| `Writeable` | 是 | `0` | `0` 表示只读，`1` 表示允许反向写入。 |
| `Description` | 是 | `温度` | 点位描述，可为空，最多 255 个字符。 |

---

## 3. RegType 怎么选

| RegType | 读功能码 | 写能力 | 典型用途 |
|---------|----------|--------|----------|
| `HoldingReg` | FC03 | 可写部分点位 | 常见数值、状态码、控制参数。 |
| `InputReg` | FC04 | 只读 | 传感器输入、测量值。 |
| `CoilStatus` | FC01 | 可写部分点位 | 开关量输出、启停命令。 |
| `InputStatus` | FC02 | 只读 | 开关量输入、限位、报警输入。 |

选择规则：

- 设备文档写 `Holding Register`、`4xxxx`、`FC03`，通常选 `HoldingReg`。
- 设备文档写 `Input Register`、`3xxxx`、`FC04`，通常选 `InputReg`。
- 设备文档写 `Coil`、`0xxxx`、`FC01`，通常选 `CoilStatus`。
- 设备文档写 `Discrete Input`、`1xxxx`、`FC02`，通常选 `InputStatus`。

---

## 4. Address 地址怎么填

本程序使用 Modbus 协议地址，通常从 `0` 开始。

很多设备说明书会写显示地址，例如 `40001`、`40002`。这些不是 CSV 里直接填写的地址。

常见换算：

| 设备文档写法 | RegType | CSV Address |
|--------------|---------|-------------|
| `40001` | `HoldingReg` | `0` |
| `40002` | `HoldingReg` | `1` |
| `30001` | `InputReg` | `0` |
| `30002` | `InputReg` | `1` |
| `00001` | `CoilStatus` | `0` |
| `10001` | `InputStatus` | `0` |

如果设备工具里显示地址 `1`，但程序读地址 `0` 一直是 `0`，请同时测试地址 `0` 和地址 `1`：

```csv
CheckAddr0,HoldingReg,0,Int16,0,16,0,
CheckAddr1,HoldingReg,1,Int16,0,16,0,
```

如果 `CheckAddr1` 才是预期值，说明该设备或模拟器界面地址与协议地址相差 1。

---

## 5. DataType 怎么选

| DataType | 占用寄存器 | 说明 |
|----------|------------|------|
| `Bool` | 线圈 1 位，寄存器中按 1 位解释 | 开关量。 |
| `Int16` | 1 | 16 位有符号整数，范围 `-32768~32767`。 |
| `UInt16` | 1 | 16 位无符号整数，范围 `0~65535`。 |
| `Int32` | 2 | 32 位有符号整数。 |
| `UInt32` | 2 | 32 位无符号整数。 |
| `Float32` | 2 | 32 位浮点数。 |
| `Double` | 4 | 64 位浮点数。 |

选择建议：

- 设备文档写有符号 16 位整数，选 `Int16`。
- 设备文档写无符号 16 位整数、状态字、位字段，通常选 `UInt16`。
- 设备文档写 32 位整数，选 `Int32` 或 `UInt32`，并确认字节序。
- 设备文档写 float、real，通常选 `Float32`，并确认字节序。
- 不确定有无符号时，先用 `UInt16` 读取完整值，再对照设备文档判断。

---

## 6. BitOffset / BitLen 怎么填

### 6.1 读取完整寄存器值

读取地址 `0` 的完整 `Int16`：

```csv
Value,HoldingReg,0,Int16,0,16,0,
```

读取地址 `0` 的完整 `UInt16`：

```csv
Value,HoldingReg,0,UInt16,0,16,0,
```

### 6.2 读取单个位

读取 bit0：

```csv
Bit0,HoldingReg,0,UInt16,0,1,0,
```

读取 bit1，有两种写法：

```csv
Bit1A,HoldingReg,0,UInt16,1,1,0,
Bit1B,HoldingReg,0,UInt16,1,0,0,
```

其中 `BitOffset>0` 且 `BitLen=0` 表示读取该偏移位置的 1 位。

### 6.3 读取一段位字段

从 bit4 开始读取 4 位，也就是 bit4~bit7：

```csv
StatusCode,HoldingReg,10,UInt16,4,4,0,
```

如果原始寄存器是：

```text
0000 0000 1011 0000
```

那么 bit4~bit7 是 `1011`，结果是 `11`。

### 6.4 Int16 位提取规则

`Int16` 完整读取时才按有符号数解释：

```csv
FullSigned,HoldingReg,0,Int16,0,16,0,
```

如果只读取部分位，则按无符号字段解释：

```csv
Low4Bits,HoldingReg,0,Int16,0,4,0,
```

---

## 7. Writeable 怎么填

`Writeable` 表示该点位是否允许程序反向写入。

建议默认填 `0`。

可以填 `1` 的场景：

- `HoldingReg` 控制参数，设备允许写入。
- `CoilStatus` 控制线圈，设备允许写入。

不要填 `1` 的场景：

- `InputReg`，只读。
- `InputStatus`，只读。
- 设备文档没有明确说明可写。
- 生产现场不允许程序控制该点位。

---

## 8. 常见问题排查

### 8.1 设备显示 32767，采集全是 0

先加两个完整读取点位：

```csv
CheckAddr0,HoldingReg,0,Int16,0,16,0,
CheckAddr1,HoldingReg,1,Int16,0,16,0,
```

在设备详情页查看实时值：

页面应显示 `CheckAddr0` 和 `CheckAddr1` 的当前值及更新时间。

判断：

- `CheckAddr0 = 32767`：地址 0 正确，继续检查位配置。
- `CheckAddr0 = 0` 且 `CheckAddr1 = 32767`：地址有 1 位偏移，CSV 应改用地址 1。
- 两个都不是 `32767`：检查 `RegType`、`slave_id`、设备写入位置、设备连接对象。

对于 `32767 = 0x7FFF`，以下点位不应全是 0：

```csv
Int16_Off00_Len01,HoldingReg,0,Int16,0,1,0,
Int16_Off00_Len02,HoldingReg,0,Int16,0,2,0,
Int16_Off01_Len00,HoldingReg,0,Int16,1,0,0,
Int16_Off01_Len01,HoldingReg,0,Int16,1,1,0,
```

理论结果：

```text
Int16_Off00_Len01 = 1
Int16_Off00_Len02 = 3
Int16_Off01_Len00 = 1
Int16_Off01_Len01 = 1
```

如果这些全是 0，说明程序实际读到的地址 0 原始值低位是 0，不是 `32767`。

### 8.2 CSV 验证通过，但实际采集没有值

检查：

- 页面中设备的 `address`、`port`、`slave_id` 是否正确。
- 页面中设备是否已经启动；修改配置后是否点击了“重启”。
- CSV 是否导入到了正确的设备。
- 设备是否支持对应功能码。
- 设备防火墙或端口是否开放。
- 日志中是否有 `[ERROR] Modbus 通信异常`。

### 8.3 CSV 导入或验证失败

运行：

```powershell
go run ./cmd/modbus-scan -validate -csv configs/points.csv
```

CLI 会返回具体行号和字段；页面导入会列出全部校验错误。任意一行错误都会使整次操作失败，不会跳过错误行。常见原因：

- 列数不是 7 列。
- `TagName` 为空、重复、以数字开头，或包含空格、横线、中文等非法字符。
- `RegType` 或 `DataType` 拼写错误。
- 非 Bool 寄存器点位写了 `BitOffset=0, BitLen=0`。
- `Float32` / `Double` 配置了位提取。
- `BitOffset + BitLen` 超出数据类型位宽。

### 8.4 Float32 或 Int32 数值不对

重点检查页面中设备配置的字节序。

可选值：

- `ABCD`
- `DCBA`
- `CDAB`
- `BADC`

单个 16 位寄存器不受该配置影响；32 位和 64 位数据会受影响。

---

## 9. 推荐接入流程

1. 先只配置一个完整读取点位，例如 `HoldingReg,0,Int16,0,16`。
2. 运行 `-validate` 确认 CSV 合法。
3. 启动采集，确认完整值与设备工具一致。
4. 如果完整值不一致，先排查地址、从站 ID、寄存器类型。
5. 完整值一致后，再配置位提取点位。
6. 位提取结果异常时，用二进制方式核对 `BitOffset` 和 `BitLen`。
7. 全部确认后，再批量导入更多点位。
