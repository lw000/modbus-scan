package service

import (
	"context"
	"testing"

	"modbus-scan/internal/model"
)

type fakeKafkaStore struct {
	settings model.KafkaSettings
	device   model.DeviceKafkaConfig
}

func (s *fakeKafkaStore) GetKafkaSettings(context.Context) (model.KafkaSettings, error) {
	return s.settings, nil
}
func (s *fakeKafkaStore) UpdateKafkaSettings(_ context.Context, cfg model.KafkaSettings) (model.KafkaSettings, error) {
	s.settings = cfg
	return cfg, nil
}
func (s *fakeKafkaStore) GetDeviceKafkaConfig(context.Context, int64) (model.DeviceKafkaConfig, error) {
	return s.device, nil
}
func (s *fakeKafkaStore) UpdateDeviceKafkaConfig(_ context.Context, cfg model.DeviceKafkaConfig) (model.DeviceKafkaConfig, error) {
	s.device = cfg
	return cfg, nil
}

type fakeKafkaRuntime struct {
	applied       model.KafkaSettings
	deviceApplied model.DeviceKafkaConfig
}

func (r *fakeKafkaRuntime) ApplySettings(_ context.Context, cfg model.KafkaSettings) (model.KafkaStatus, error) {
	r.applied = cfg
	return model.KafkaStatus{State: "online"}, nil
}
func (r *fakeKafkaRuntime) ApplyDeviceConfig(cfg model.DeviceKafkaConfig) { r.deviceApplied = cfg }
func (r *fakeKafkaRuntime) Status() model.KafkaStatus                     { return model.KafkaStatus{State: "online"} }

func TestKafkaServicePreservesAndClearsPassword(t *testing.T) {
	store := &fakeKafkaStore{settings: model.KafkaSettings{SASLPassword: "saved", ClientID: "modbus-scan", KafkaVersion: "3.0.0", QueueCapacity: 1000}}
	runtime := &fakeKafkaRuntime{}
	service := NewKafkaService(store, runtime, t.TempDir())
	input := store.settings
	input.SASLPassword = ""
	if _, err := service.UpdateSettings(context.Background(), KafkaSettingsInput{KafkaSettings: input}); err != nil {
		t.Fatal(err)
	}
	if store.settings.SASLPassword != "saved" || runtime.applied.SASLPassword != "saved" {
		t.Fatalf("password was not preserved: store=%q runtime=%q", store.settings.SASLPassword, runtime.applied.SASLPassword)
	}
	if _, err := service.UpdateSettings(context.Background(), KafkaSettingsInput{KafkaSettings: input, ClearSASLPassword: true}); err != nil {
		t.Fatal(err)
	}
	if store.settings.SASLPassword != "" {
		t.Fatalf("password was not cleared: %q", store.settings.SASLPassword)
	}
}

func TestKafkaServiceAppliesDeviceConfigImmediately(t *testing.T) {
	store := &fakeKafkaStore{}
	runtime := &fakeKafkaRuntime{}
	service := NewKafkaService(store, runtime, t.TempDir())
	cfg := model.DeviceKafkaConfig{DeviceID: 7, Enabled: true, Topic: "plc", Mode: model.KafkaModeChange, FullIntervalSec: 60}
	if _, err := service.UpdateDeviceConfig(context.Background(), 7, cfg); err != nil {
		t.Fatal(err)
	}
	if runtime.deviceApplied.DeviceID != 7 || store.device.DeviceID != 7 {
		t.Fatalf("store=%#v runtime=%#v", store.device, runtime.deviceApplied)
	}
}
