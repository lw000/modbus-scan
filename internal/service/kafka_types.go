package service

import (
	"context"

	"modbus-scan/internal/model"
)

// KafkaStore persists Kafka connection and per-device publishing configuration.
type KafkaStore interface {
	GetKafkaSettings(context.Context) (model.KafkaSettings, error)
	UpdateKafkaSettings(context.Context, model.KafkaSettings) (model.KafkaSettings, error)
	GetDeviceKafkaConfig(context.Context, int64) (model.DeviceKafkaConfig, error)
	UpdateDeviceKafkaConfig(context.Context, model.DeviceKafkaConfig) (model.DeviceKafkaConfig, error)
}

// KafkaRuntime applies configuration to the live publishing subsystem.
type KafkaRuntime interface {
	ApplySettings(context.Context, model.KafkaSettings) (model.KafkaStatus, error)
	ApplyDeviceConfig(model.DeviceKafkaConfig)
	Status() model.KafkaStatus
}

// KafkaSettingsInput carries write-only password update semantics.
type KafkaSettingsInput struct {
	model.KafkaSettings
	ClearSASLPassword bool `json:"clear_sasl_password"`
}

// KafkaSettingsView combines redacted persisted configuration and live status.
type KafkaSettingsView struct {
	Settings model.KafkaSettings `json:"settings"`
	Status   model.KafkaStatus   `json:"status"`
}

// KafkaService manages dynamic Kafka configuration.
type KafkaService struct {
	store   KafkaStore
	runtime KafkaRuntime
	baseDir string
}

// NewKafkaService creates a Kafka configuration service.
func NewKafkaService(store KafkaStore, runtime KafkaRuntime, baseDir string) *KafkaService {
	return &KafkaService{store: store, runtime: runtime, baseDir: baseDir}
}

// GetSettings returns redacted persisted settings and current runtime status.
func (s *KafkaService) GetSettings(ctx context.Context) (KafkaSettingsView, error) {
	cfg, err := s.store.GetKafkaSettings(ctx)
	if err != nil {
		return KafkaSettingsView{}, err
	}
	cfg.SASLPassword = ""
	return KafkaSettingsView{Settings: cfg, Status: s.runtime.Status()}, nil
}

// GetDeviceConfig returns one device's publishing configuration.
func (s *KafkaService) GetDeviceConfig(ctx context.Context, deviceID int64) (model.DeviceKafkaConfig, error) {
	return s.store.GetDeviceKafkaConfig(ctx, deviceID)
}
