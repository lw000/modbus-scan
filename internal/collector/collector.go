package collector

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goburrow/modbus"

	"modbus-scan/internal/config"
	"modbus-scan/internal/udm"
)

// ================= 连接状态常量 =================

const (
	StateOnline int32 = iota
	StateReconnecting
	StateOffline
)

// ================= 连接管理器 =================

// ConnManager 带熔断机制的 Modbus 连接管理器
type ConnManager struct {
	cfg          *config.DeviceConfig
	client       modbus.Client
	handler      *modbus.TCPClientHandler
	mu           sync.Mutex
	state        int32
	retryCount   int32
	maxRetries   int32
	reconnecting int32 // 原子标志，防止并发重连
	ctx          context.Context
}

// NewConnManager 创建连接管理器
func NewConnManager(cfg *config.DeviceConfig, ctx context.Context) *ConnManager {
	return &ConnManager{cfg: cfg, maxRetries: 10, ctx: ctx}
}

// Connect 建立 Modbus TCP 连接
func (cm *ConnManager) Connect() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", cm.cfg.Modbus.Address, cm.cfg.Modbus.Port)
	cm.handler = modbus.NewTCPClientHandler(addr)
	cm.handler.Timeout = time.Duration(cm.cfg.Modbus.TimeoutSec) * time.Second
	cm.handler.SlaveId = cm.cfg.Modbus.SlaveID

	if err := cm.handler.Connect(); err != nil {
		return err
	}
	cm.client = modbus.NewClient(cm.handler)
	log.Printf("[INFO] Modbus 设备连接成功: %s", addr)
	return nil
}

// ReconnectWithBackoff 使用指数退避策略进行断线重连
// 通过 CAS 原子操作确保同一时间只有一个重连协程在运行
func (cm *ConnManager) ReconnectWithBackoff() {
	if !atomic.CompareAndSwapInt32(&cm.reconnecting, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&cm.reconnecting, 0)

	cm.mu.Lock()
	if cm.handler != nil {
		cm.handler.Close()
	}
	cm.client = nil
	atomic.StoreInt32(&cm.state, StateReconnecting)
	cm.mu.Unlock()

	baseDelay := 1 * time.Second
	maxDelay := 30 * time.Second

	for attempt := int32(1); ; attempt++ {
		if attempt > cm.maxRetries {
			log.Printf("[ERROR] 连续 %d 次重连失败，触发熔断！设备进入离线休眠状态", attempt-1)
			atomic.StoreInt32(&cm.retryCount, 0)
			atomic.StoreInt32(&cm.state, StateOffline)
			go cm.startHeartbeatProbe(cm.ctx)
			return
		}

		delay := time.Duration(math.Min(
			float64(baseDelay)*math.Pow(2, float64(attempt-1)),
			float64(maxDelay),
		))
		log.Printf("[WARN] 第 %d/%d 次重连，等待 %v...", attempt, cm.maxRetries, delay)
		timer := time.NewTimer(delay)
		select {
		case <-cm.ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}

		if err := cm.Connect(); err == nil {
			log.Println("[INFO] 断线重连成功！")
			atomic.StoreInt32(&cm.retryCount, 0)
			atomic.StoreInt32(&cm.state, StateOnline)
			return
		}
		atomic.StoreInt32(&cm.retryCount, attempt)
	}
}

// Close closes the active Modbus connection.
func (cm *ConnManager) Close() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.client = nil
	if cm.handler == nil {
		return nil
	}
	err := cm.handler.Close()
	cm.handler = nil
	return err
}

// startHeartbeatProbe 熔断后的低频心跳探测，每 60 秒尝试一次连接恢复
func (cm *ConnManager) startHeartbeatProbe(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[INFO] 心跳探测收到退出信号")
			return
		case <-ticker.C:
			if atomic.LoadInt32(&cm.state) != StateOffline {
				return
			}
			log.Printf("[INFO] [离线探测] 尝试向 %s 发起连通性测试...", cm.cfg.Modbus.Address)
			if err := cm.Connect(); err == nil {
				log.Println("[INFO] 离线探测成功，设备已恢复在线！")
				atomic.StoreInt32(&cm.retryCount, 0)
				atomic.StoreInt32(&cm.state, StateOnline)
				return
			}
		}
	}
}

// GetClient 获取当前可用的 Modbus 客户端，离线或重连中返回 nil
func (cm *ConnManager) GetClient() modbus.Client {
	state := atomic.LoadInt32(&cm.state)
	if state == StateOffline || state == StateReconnecting {
		return nil
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.client
}

// State returns the current connection state.
func (cm *ConnManager) State() int32 { return atomic.LoadInt32(&cm.state) }

// ================= 采集器核心引擎 =================

// Collector Modbus 数据采集器
type Collector struct {
	connMgr   *ConnManager
	udm       *udm.UniversalDataModel
	chunks    map[string][]config.ReadChunk
	byteOrder string
	publish   func(string, udm.Value)
}

// NewCollector 创建采集器
func NewCollector(connMgr *ConnManager, u *udm.UniversalDataModel, chunks map[string][]config.ReadChunk, cfg *config.DeviceConfig, publishers ...func(string, udm.Value)) *Collector {
	c := &Collector{
		connMgr:   connMgr,
		udm:       u,
		chunks:    chunks,
		byteOrder: strings.ToUpper(cfg.Modbus.ByteOrder),
	}
	if len(publishers) > 0 {
		c.publish = publishers[0]
	}
	return c
}

func (c *Collector) updateValue(tag string, value any) {
	at := time.Now().UTC()
	c.udm.UpdateAt(tag, value, at)
	if c.publish != nil {
		c.publish(tag, udm.Value{Value: value, UpdatedAt: at})
	}
}

// ScanLoop 定时采集主循环，通过 ctx 支持优雅退出
func (c *Collector) ScanLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[INFO] ScanLoop 收到退出信号，采集循环停止")
			return
		case <-ticker.C:
			c.scanOnce()
		}
	}
}

// scanOnce 执行一次完整的采集周期
// 遇到通信异常时跳过本周期剩余部分，避免在断连期间继续发送无效请求
func (c *Collector) scanOnce() {
	client := c.connMgr.GetClient()
	if client == nil {
		return
	}

	for _, readChunks := range c.chunks {
		for _, chunk := range readChunks {
			var results []byte
			var err error

			regType := chunk.Points[0].RegType
			switch regType {
			case "HoldingReg":
				results, err = client.ReadHoldingRegisters(chunk.StartAddr, chunk.Quantity)
			case "InputReg":
				results, err = client.ReadInputRegisters(chunk.StartAddr, chunk.Quantity)
			case "CoilStatus":
				results, err = client.ReadCoils(chunk.StartAddr, chunk.Quantity)
			case "InputStatus":
				results, err = client.ReadDiscreteInputs(chunk.StartAddr, chunk.Quantity)
			default:
				log.Printf("[WARN] 未知 RegType: %s", regType)
				continue
			}

			if err != nil {
				log.Printf("[ERROR] Modbus 通信异常 [Addr=%d, Qty=%d]: %v，准备重连...",
					chunk.StartAddr, chunk.Quantity, err)
				go c.connMgr.ReconnectWithBackoff()
				return
			}

			c.parseAndUpdate(chunk, results)
		}
	}

}

// parseAndUpdate 将原始字节数据解析为各点位的采集值并更新到 UDM
// 对于寄存器类型 (HoldingReg/InputReg)，raw 为字节数组 (每寄存器 2 字节)
// 对于线圈类型 (CoilStatus/InputStatus)，raw 为位packed数组 (每位 1 个线圈，MSB first)
func (c *Collector) parseAndUpdate(chunk config.ReadChunk, raw []byte) {
	isBit := config.IsRegTypeBit(chunk.Points[0].RegType)

	for _, pt := range chunk.Points {
		var finalVal interface{}

		if isBit {
			// 线圈/离散输入: 每个 bit 代表一个点位的 bool 值
			// Modbus 规范: 第一个线圈对应第一个字节的 LSB (bit 0)
			bitIdx := pt.Address - chunk.StartAddr
			byteIdx := bitIdx / 8
			bitOffset := bitIdx % 8
			if int(byteIdx) >= len(raw) {
				log.Printf("[WARN] 点位 [%s] 数据越界: byteIdx=%d, available=%d",
					pt.TagName, byteIdx, len(raw))
				continue
			}
			finalVal = (raw[byteIdx]>>bitOffset)&0x01 == 1
		} else {
			// 寄存器类型: 统一位提取路径
			byteOffset := (pt.Address - chunk.StartAddr) * 2
			requiredBytes := pt.GetRegisterCount() * 2
			if byteOffset+requiredBytes > uint16(len(raw)) {
				log.Printf("[WARN] 点位 [%s] 数据越界: offset=%d, need=%d, available=%d",
					pt.TagName, byteOffset, requiredBytes, len(raw))
				continue
			}

			dataSlice := raw[byteOffset : byteOffset+requiredBytes]
			reorderedData := c.applyGlobalByteOrder(dataSlice)

			// 1. 将字节序处理后的数据转为 uint64
			var rawUint uint64
			switch len(reorderedData) {
			case 2:
				rawUint = uint64(binary.BigEndian.Uint16(reorderedData))
			case 4:
				rawUint = uint64(binary.BigEndian.Uint32(reorderedData))
			case 8:
				rawUint = binary.BigEndian.Uint64(reorderedData)
			}

			// 2. 计算 effectiveBitLen
			// BitOffset>0, BitLen=0: 读取 BitOffset 位置的 1 位
			// BitLen>0: 显式位提取，使用指定长度
			// Bool 类型 BitOffset=0, BitLen=0 时使用类型位宽 (1)
			effectiveLen := pt.BitLen
			if pt.BitOffset > 0 && effectiveLen == 0 {
				effectiveLen = 1
			}
			if effectiveLen == 0 {
				effectiveLen = config.TypeBitWidth(pt.DataType)
			}

			// 3. 位提取: (rawUint >> BitOffset) & mask
			mask := uint64((1 << effectiveLen) - 1)
			extracted := (rawUint >> pt.BitOffset) & mask

			// 4. 按 DataType 转换
			switch pt.DataType {
			case "Bool":
				finalVal = extracted == 1
			case "Int16":
				if effectiveLen == 16 {
					finalVal = float64(int16(extracted))
				} else {
					finalVal = float64(extracted)
				}
			case "UInt16":
				finalVal = float64(extracted)
			case "Int32":
				if effectiveLen == 32 {
					finalVal = float64(int32(extracted))
				} else {
					finalVal = float64(extracted)
				}
			case "UInt32":
				finalVal = float64(extracted)
			case "Float32":
				val := math.Float32frombits(uint32(extracted))
				finalVal = float64(val)
			case "Double":
				val := math.Float64frombits(extracted)
				finalVal = val
			default:
				finalVal = float64(extracted)
			}
		}
		c.updateValue(pt.TagName, finalVal)
	}
}

// applyGlobalByteOrder 根据 byteOrder 配置对原始字节数据进行字节序重排
//
//	Modbus 字节序约定 (以 4 字节为例，顺序为 [A B C D]，每个字母代表一个字节)：
//	  - ABCD: Big-Endian，无需转换
//	    寄存器序: R1=[AB], R2=[CD]，R1 为高字
//	  - DCBA: Little-Endian，全字节反转
//	    寄存器序: R1=[DC], R2=[BA]，R1 为低字
//	  - CDAB: Big-Endian 字交换，每对相邻 16 位字互换位置
//	    寄存器序: R1=[CD], R2=[AB]，高字和低字交换
//	  - BADC: Little-Endian 字交换，每个 16 位字内高字节和低字节互换
//	    寄存器序: R1=[BA], R2=[DC]，每个字内字节反转
func (c *Collector) applyGlobalByteOrder(data []byte) []byte {
	if c.byteOrder == "ABCD" || c.byteOrder == "" {
		return data
	}

	res := make([]byte, len(data))

	switch len(data) {
	case 2: // 单寄存器 (16 位) — Modbus 协议固定大端传输，无需字节序转换
		return data

	case 4: // 双寄存器 (32 位)
		switch c.byteOrder {
		case "DCBA":
			res[0], res[1], res[2], res[3] = data[3], data[2], data[1], data[0]
		case "CDAB":
			// 交换 R1 和 R2 (16 位字交换)
			res[0], res[1], res[2], res[3] = data[2], data[3], data[0], data[1]
		case "BADC":
			// 每个字内字节互换
			res[0], res[1], res[2], res[3] = data[1], data[0], data[3], data[2]
		}

	case 8: // 四寄存器 (64 位)
		switch c.byteOrder {
		case "DCBA":
			// 全字节反转
			for i := 0; i < 8; i++ {
				res[i] = data[7-i]
			}
		case "CDAB":
			// 交换每对相邻 16 位字: R1↔R2, R3↔R4
			res[0], res[1] = data[2], data[3] // R2
			res[2], res[3] = data[0], data[1] // R1
			res[4], res[5] = data[6], data[7] // R4
			res[6], res[7] = data[4], data[5] // R3
		case "BADC":
			// 每个 16 位字内字节互换
			for i := 0; i < 8; i += 2 {
				res[i], res[i+1] = data[i+1], data[i]
			}
		}

	default:
		copy(res, data)
	}
	return res
}

// ================= 反向写入透传机制 =================

// findPoint 在所有 chunk 中查找指定 TagName 的点位配置
func (c *Collector) findPoint(tagName string) *config.PointConfig {
	for _, chunks := range c.chunks {
		for _, chunk := range chunks {
			for _, pt := range chunk.Points {
				if pt.TagName == tagName {
					return pt
				}
			}
		}
	}
	return nil
}

// WriteTag 向设备写入指定标签的值
// 支持 uint16、int16、int 类型写入，仅对 Writeable=true 的点位生效
func (c *Collector) WriteTag(tagName string, value interface{}) error {
	targetPt := c.findPoint(tagName)
	if targetPt == nil {
		return fmt.Errorf("tag not found: %s", tagName)
	}
	if !targetPt.Writeable {
		return fmt.Errorf("tag %s is read-only", tagName)
	}

	client := c.connMgr.GetClient()
	if client == nil {
		return fmt.Errorf("device offline")
	}

	switch v := value.(type) {
	case uint16:
		if targetPt.RegType == "HoldingReg" {
			_, err := client.WriteSingleRegister(targetPt.Address, v)
			return err
		}
		return fmt.Errorf("uint16 write only supported for HoldingReg, got %s", targetPt.RegType)
	case int16:
		if targetPt.RegType == "HoldingReg" {
			_, err := client.WriteSingleRegister(targetPt.Address, uint16(v))
			return err
		}
		return fmt.Errorf("int16 write only supported for HoldingReg, got %s", targetPt.RegType)
	case int:
		if targetPt.RegType == "HoldingReg" {
			if v < 0 || v > 65535 {
				return fmt.Errorf("value %d out of uint16 range", v)
			}
			_, err := client.WriteSingleRegister(targetPt.Address, uint16(v))
			return err
		}
		return fmt.Errorf("int write only supported for HoldingReg, got %s", targetPt.RegType)
	case bool:
		if targetPt.RegType == "CoilStatus" {
			val := uint16(0x0000)
			if v {
				val = 0xFF00
			}
			_, err := client.WriteSingleCoil(targetPt.Address, val)
			return err
		}
		return fmt.Errorf("bool write only supported for CoilStatus, got %s", targetPt.RegType)
	default:
		return fmt.Errorf("unsupported write type: %T", value)
	}
}
