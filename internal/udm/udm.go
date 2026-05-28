package udm

import (
	"fmt"
	"log"
	"sync"
)

// UniversalDataModel 通用数据模型，提供线程安全的数据读写
type UniversalDataModel struct {
	mu   sync.RWMutex
	data map[string]interface{}
}

// New 创建新的 UDM 实例
func New() *UniversalDataModel {
	return &UniversalDataModel{data: make(map[string]interface{})}
}

// Update 更新指定标签的数据
func (udm *UniversalDataModel) Update(tag string, val interface{}) {
	udm.mu.Lock()
	defer udm.mu.Unlock()
	udm.data[tag] = val
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
	udm.mu.RLock()
	defer udm.mu.RUnlock()
	if len(udm.data) == 0 {
		return
	}
	for tag, val := range udm.data {
		log.Printf("[DATA] %s = %v", tag, fmt.Sprintf("%v", val))
	}
}
