package config

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
)

// validDataTypes 支持的数据类型集合
var validDataTypes = map[string]bool{
	"Bool": true, "Int16": true, "UInt16": true,
	"Int32": true, "UInt32": true, "Float32": true, "Double": true,
}

// validRegTypes 支持的寄存器类型集合
var validRegTypes = map[string]bool{
	"HoldingReg":  true, // 保持寄存器 (FC03/FC06/FC16)
	"InputReg":    true, // 输入寄存器 (FC04)
	"CoilStatus":  true, // 线圈 (FC01/FC05/FC15)
	"InputStatus": true, // 离散输入 (FC02)
}

// IsRegTypeBit 判断寄存器类型是否为按位访问 (线圈/离散输入)
func IsRegTypeBit(regType string) bool {
	return regType == "CoilStatus" || regType == "InputStatus"
}

// TypeBitWidth 返回数据类型的位宽度
func TypeBitWidth(dataType string) int {
	switch dataType {
	case "Bool":
		return 1
	case "Int16", "UInt16":
		return 16
	case "Int32", "UInt32", "Float32":
		return 32
	case "Double":
		return 64
	default:
		return 16
	}
}

// PointConfig 单个数据点位的配置
type PointConfig struct {
	TagName   string
	RegType   string
	Address   uint16
	DataType  string
	BitOffset int // 位偏移 (仅寄存器类型有效，线圈类型忽略)
	BitLen    int // 位长度 (1~N=显式位提取; BitOffset>0时允许0表示读取该偏移位的1位)
	Scale     float64
	Offset    float64
	Writeable bool
}

// GetRegisterCount 返回该数据类型占用的 Modbus 寄存器数量
// 对于 CoilStatus/InputStatus（按位访问类型），每个点位占 1 个位，在合并时以 1 位为 1 单位
func (p *PointConfig) GetRegisterCount() uint16 {
	if IsRegTypeBit(p.RegType) {
		return 1 // 每个点位占 1 位
	}
	switch p.DataType {
	case "Int32", "UInt32", "Float32":
		return 2
	case "Double":
		return 4
	default:
		return 1
	}
}

// LoadPointsFromCSV 从 CSV 文件加载点位配置
// CSV 列顺序: TagName, RegType, Address, DataType, BitOffset, BitLen, Scale, Offset, Writeable
func LoadPointsFromCSV(filePath string) ([]PointConfig, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("打开配置文件失败: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("解析 CSV 格式失败: %w", err)
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("CSV 文件至少需要包含表头和一行数据")
	}

	var points []PointConfig
	tagSet := make(map[string]bool)
	var validationErrors []string

	for i, record := range records {
		lineNum := i // 跳过表头后，第1行数据为行号1
		if i == 0 { // 跳过表头
			continue
		}
		if len(record) < 9 {
			validationErrors = append(validationErrors,
				fmt.Sprintf("第 %d 行: 列数不足，需要至少 9 列，当前 %d 列", lineNum, len(record)))
			continue
		}

		tagName := strings.TrimSpace(record[0])
		regType := strings.TrimSpace(record[1])
		dataType := strings.TrimSpace(record[3])

		if tagName == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("第 %d 行: TagName 不能为空", lineNum))
			continue
		}
		if !validRegTypes[regType] {
			validationErrors = append(validationErrors,
				fmt.Sprintf("第 %d 行 [%s]: RegType 无效: %s (支持: HoldingReg/InputReg/CoilStatus/InputStatus)", lineNum, tagName, regType))
			continue
		}
		if !validDataTypes[dataType] {
			validationErrors = append(validationErrors,
				fmt.Sprintf("第 %d 行 [%s]: DataType 无效: %s (支持: Bool/Int16/UInt16/Int32/UInt32/Float32/Double)", lineNum, tagName, dataType))
			continue
		}

		addr, addrErr := strconv.ParseUint(strings.TrimSpace(record[2]), 10, 16)
		if addrErr != nil {
			validationErrors = append(validationErrors,
				fmt.Sprintf("第 %d 行 [%s]: Address [%s] 不是有效的 uint16", lineNum, tagName, record[2]))
			continue
		}

		bitOff := 0
		bitLen := 0 // 0 仅在 BitOffset>0 时合法，表示读取该偏移位 1 位
		if !IsRegTypeBit(regType) {
			// 寄存器类型: 解析 BitOffset 和 BitLen
			bitOff, err = strconv.Atoi(strings.TrimSpace(record[4]))
			if err != nil || bitOff < 0 {
				validationErrors = append(validationErrors,
					fmt.Sprintf("第 %d 行 [%s]: BitOffset 无效，当前值: %s", lineNum, tagName, record[4]))
				continue
			}
			bitLen, err = strconv.Atoi(strings.TrimSpace(record[5]))
			if err != nil || bitLen < 0 {
				validationErrors = append(validationErrors,
					fmt.Sprintf("第 %d 行 [%s]: BitLen 无效，当前值: %s", lineNum, tagName, record[5]))
				continue
			}
			// Bool 在寄存器中 BitOffset/BitLen 只能为 0
			if dataType == "Bool" && (bitOff > 0 || bitLen > 0) {
				validationErrors = append(validationErrors,
					fmt.Sprintf("第 %d 行 [%s]: Bool 类型 BitOffset/BitLen 只能为 0", lineNum, tagName))
				continue
			}
			// Float32/Double 不支持位提取
			if (dataType == "Float32" || dataType == "Double") && (bitOff > 0 || bitLen > 0) {
				validationErrors = append(validationErrors,
					fmt.Sprintf("第 %d 行 [%s]: %s 类型不支持位提取 (BitOffset/BitLen 应为 0)", lineNum, tagName, dataType))
				continue
			}
			// BitOffset=0, BitLen=0 不合法 (Bool 类型除外)
			if bitOff == 0 && bitLen == 0 && dataType != "Bool" {
				validationErrors = append(validationErrors,
					fmt.Sprintf("第 %d 行 [%s]: BitOffset=0 且 BitLen=0 不合法，请指定有效的 BitLen", lineNum, tagName))
				continue
			}
			// BitLen 有效范围:
			//   BitOffset=0: BitLen 范围 1~typeBitWidth
			//   BitOffset>0: BitLen 范围 0~(typeBitWidth-1), 0 表示读取该偏移位 1 位
			maxBitLen := TypeBitWidth(dataType)
			if bitOff > 0 {
				maxBitLen = TypeBitWidth(dataType) - 1
			}
			if bitLen > maxBitLen {
				if bitOff > 0 {
					validationErrors = append(validationErrors,
						fmt.Sprintf("第 %d 行 [%s]: BitLen(%d) 超出 %s 有效范围(0~%d)",
							lineNum, tagName, bitLen, dataType, maxBitLen))
				} else {
					validationErrors = append(validationErrors,
						fmt.Sprintf("第 %d 行 [%s]: BitLen(%d) 超出 %s 有效范围(1~%d)",
							lineNum, tagName, bitLen, dataType, maxBitLen))
				}
				continue
			}
			// BitOffset 有效范围: 0 ~ typeBitWidth-1
			bitOffLimit := TypeBitWidth(dataType) - 1
			if bitOff > bitOffLimit {
				validationErrors = append(validationErrors,
					fmt.Sprintf("第 %d 行 [%s]: BitOffset(%d) 超出 %s 有效范围(0~%d)",
						lineNum, tagName, bitOff, dataType, bitOffLimit))
				continue
			}
			// BitOffset+effectiveBitLen 上限: typeBitWidth
			// BitLen=0 且 BitOffset>0 时，effectiveBitLen=1
			effectiveLen := bitLen
			if bitOff > 0 && effectiveLen == 0 {
				effectiveLen = 1
			}
			if bitOff+effectiveLen > TypeBitWidth(dataType) {
				validationErrors = append(validationErrors,
					fmt.Sprintf("第 %d 行 [%s]: BitOffset(%d)+BitLen(%d)=%d 超出 %s 有效范围(1~%d)",
						lineNum, tagName, bitOff, effectiveLen, bitOff+effectiveLen, dataType, TypeBitWidth(dataType)))
				continue
			}
		}
		// 线圈/离散输入类型只允许 Bool 数据类型
		if IsRegTypeBit(regType) && dataType != "Bool" {
			validationErrors = append(validationErrors,
				fmt.Sprintf("第 %d 行 [%s]: %s 类型仅支持 Bool 数据类型", lineNum, tagName, regType))
			continue
		}

		scale, scaleErr := strconv.ParseFloat(strings.TrimSpace(record[6]), 64)
		if scaleErr != nil {
			validationErrors = append(validationErrors,
				fmt.Sprintf("第 %d 行 [%s]: Scale [%s] 不是有效的数值", lineNum, tagName, record[5]))
			continue
		}

		offset, offsetErr := strconv.ParseFloat(strings.TrimSpace(record[7]), 64)
		if offsetErr != nil {
			validationErrors = append(validationErrors,
				fmt.Sprintf("第 %d 行 [%s]: Offset [%s] 不是有效的数值", lineNum, tagName, record[6]))
			continue
		}

		writeable := strings.ToLower(strings.TrimSpace(record[8])) == "true"

		if tagSet[tagName] {
			validationErrors = append(validationErrors,
				fmt.Sprintf("第 %d 行 [%s]: TagName 重复", lineNum, tagName))
			continue
		}
		tagSet[tagName] = true

		points = append(points, PointConfig{
			TagName:   tagName,
			RegType:   regType,
			Address:   uint16(addr),
			DataType:  dataType,
			BitOffset: bitOff,
			BitLen:    bitLen,
			Scale:     scale,
			Offset:    offset,
			Writeable: writeable,
		})
	}

	// 输出校验失败的行（不阻断加载，仅跳过错误行）
	for _, errMsg := range validationErrors {
		log.Printf("[WARN] CSV 校验跳过: %s", errMsg)
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("CSV 配置验证失败: 没有有效的点位配置")
	}
	log.Printf("[INFO] 成功加载 %d 个有效点位配置 (跳过 %d 行错误)", len(points), len(validationErrors))
	return points, nil
}

// ReadChunk 合并后的批量读取块
type ReadChunk struct {
	StartAddr uint16
	Quantity  uint16
	Points    []*PointConfig
}

// OptimizeChunks 将离散点位按寄存器类型分组，并在地址连续时合并为批量读取块
// 寄存器类型 (HoldingReg/InputReg) 每块最多 125 个寄存器
// 线圈类型 (CoilStatus/InputStatus) 每块最多 2000 个线圈
func OptimizeChunks(points []PointConfig) map[string][]ReadChunk {
	groups := make(map[string][]*PointConfig)
	for i := range points {
		key := points[i].RegType
		groups[key] = append(groups[key], &points[i])
	}

	chunksMap := make(map[string][]ReadChunk)
	const maxRegisters uint16 = 125
	const maxCoils uint16 = 2000

	for key, pts := range groups {
		sort.Slice(pts, func(i, j int) bool { return pts[i].Address < pts[j].Address })

		// 线圈类型每个点位占 1 位，寄存器类型每个点位占 getRegisterCount 个寄存器
		isBit := IsRegTypeBit(key)
		maxQty := maxRegisters
		if isBit {
			maxQty = maxCoils
		}

		var chunks []ReadChunk
		currentChunk := ReadChunk{StartAddr: pts[0].Address, Points: []*PointConfig{pts[0]}}
		chunkEndAddr := pts[0].Address + pts[0].GetRegisterCount() - 1

		for i := 1; i < len(pts); i++ {
			nextAddr := pts[i].Address
			nextEndAddr := pts[i].Address + pts[i].GetRegisterCount() - 1

			totalQty := nextEndAddr - currentChunk.StartAddr + 1
			if nextAddr <= chunkEndAddr+1 && totalQty <= maxQty {
				currentChunk.Points = append(currentChunk.Points, pts[i])
				if nextEndAddr > chunkEndAddr {
					chunkEndAddr = nextEndAddr
				}
			} else {
				currentChunk.Quantity = chunkEndAddr - currentChunk.StartAddr + 1
				chunks = append(chunks, currentChunk)
				currentChunk = ReadChunk{StartAddr: nextAddr, Points: []*PointConfig{pts[i]}}
				chunkEndAddr = nextEndAddr
			}
		}
		currentChunk.Quantity = chunkEndAddr - currentChunk.StartAddr + 1
		chunks = append(chunks, currentChunk)
		chunksMap[key] = chunks

		var totalQty uint16
		for _, c := range chunks {
			totalQty += c.Quantity
		}
		unit := "寄存器"
		if isBit {
			unit = "线圈"
		}
		log.Printf("[INFO] 寄存器类型 [%s]: %d 个点位合并为 %d 个批量读取块, 总计 %d 个%s",
			key, len(pts), len(chunks), totalQty, unit)
	}
	return chunksMap
}
