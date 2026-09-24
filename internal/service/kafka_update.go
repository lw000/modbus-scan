package service

import (
	"context"
	"fmt"

	"modbus-scan/internal/kafkapub"
	"modbus-scan/internal/model"
)

// UpdateSettings validates, applies, and persists global settings.
func (s *KafkaService) UpdateSettings(ctx context.Context, input KafkaSettingsInput) (KafkaSettingsView, error) {
	current, err := s.store.GetKafkaSettings(ctx)
	if err != nil {
		return KafkaSettingsView{}, err
	}
	if input.ClearSASLPassword {
		input.SASLPassword = ""
	} else if input.SASLPassword == "" {
		input.SASLPassword = current.SASLPassword
	}
	normalized, err := kafkapub.NormalizeSettings(input.KafkaSettings, s.baseDir)
	if err != nil {
		return KafkaSettingsView{}, &ValidationError{Fields: []model.FieldError{{Field: "kafka", Message: err.Error()}}}
	}
	status, err := s.runtime.ApplySettings(ctx, normalized)
	if err != nil {
		return KafkaSettingsView{}, fmt.Errorf("apply Kafka settings: %w", err)
	}
	saved, err := s.store.UpdateKafkaSettings(ctx, normalized)
	if err != nil {
		return KafkaSettingsView{}, err
	}
	saved.SASLPassword = ""
	return KafkaSettingsView{Settings: saved, Status: status}, nil
}

// UpdateDeviceConfig validates, persists, and hot-applies one device's policy.
func (s *KafkaService) UpdateDeviceConfig(ctx context.Context, deviceID int64, cfg model.DeviceKafkaConfig) (model.DeviceKafkaConfig, error) {
	cfg.DeviceID = deviceID
	if err := kafkapub.ValidateDeviceConfig(cfg); err != nil {
		return model.DeviceKafkaConfig{}, &ValidationError{Fields: []model.FieldError{{Field: "kafka", Message: err.Error()}}}
	}
	saved, err := s.store.UpdateDeviceKafkaConfig(ctx, cfg)
	if err != nil {
		return model.DeviceKafkaConfig{}, err
	}
	s.runtime.ApplyDeviceConfig(saved)
	return saved, nil
}
