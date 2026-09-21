package udm

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Value is a point value and its last update time.
type Value struct {
	Value     interface{} `json:"value"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// UniversalDataModel 通用数据模型，提供线程安全的数据读写
type UniversalDataModel struct {
	mu    sync.RWMutex
	data  map[string]interface{}
	times map[string]time.Time
}

// New 创建新的 UDM 实例
func New() *UniversalDataModel {
	return &UniversalDataModel{data: make(map[string]interface{}), times: make(map[string]time.Time)}
}

// Update 更新指定标签的数据
func (udm *UniversalDataModel) Update(tag string, val interface{}) {
	udm.UpdateAt(tag, val, time.Now().UTC())
}

// UpdateAt updates a tag and records the supplied observation time.
func (udm *UniversalDataModel) UpdateAt(tag string, val interface{}, at time.Time) {
	udm.mu.Lock()
	defer udm.mu.Unlock()
	udm.data[tag] = val
	udm.times[tag] = at
}

// Snapshot returns a detached snapshot of values and update times.
func (udm *UniversalDataModel) Snapshot() map[string]Value {
	udm.mu.RLock()
	defer udm.mu.RUnlock()
	snapshot := make(map[string]Value, len(udm.data))
	for tag, value := range udm.data {
		snapshot[tag] = Value{Value: value, UpdatedAt: udm.times[tag]}
	}
	return snapshot
}

// Get 获取指定标签的数据
func (udm *UniversalDataModel) Get(tag string) (interface{}, bool) {
	udm.mu.RLock()
	defer udm.mu.RUnlock()
	val, ok := udm.data[tag]
	return val, ok
}

// GetAll 返回当前所有数据的快照，用于批量导出
func (udm *UniversalDataModel) GetAll() map[string]interface{} {
	udm.mu.RLock()
	defer udm.mu.RUnlock()
	snapshot := make(map[string]interface{}, len(udm.data))
	for k, v := range udm.data {
		snapshot[k] = v
	}
	return snapshot
}

// LogAll 输出当前所有标签的采集值
func (udm *UniversalDataModel) LogAll() {
	udm.LogAllWithDevice("")
}

// LogAllWithDevice 输出当前所有标签的采集值，并在标签前附加设备名。
func (udm *UniversalDataModel) LogAllWithDevice(deviceName string) {
	udm.mu.RLock()
	defer udm.mu.RUnlock()
	if len(udm.data) == 0 {
		return
	}
	for tag, val := range udm.data {
		if deviceName == "" {
			log.Printf("[DATA] %s = %v", tag, fmt.Sprintf("%v", val))
			continue
		}
		log.Printf("[DATA] %s.%s = %v", deviceName, tag, fmt.Sprintf("%v", val))
	}
}
