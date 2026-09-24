package kafkapub

import (
	"testing"

	"modbus-scan/internal/model"
)

func TestValidateDeviceConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     model.DeviceKafkaConfig
		wantErr bool
	}{
		{name: "valid change", cfg: model.DeviceKafkaConfig{Enabled: true, Topic: "plc.values", Mode: model.KafkaModeChange, FullIntervalSec: 60}},
		{name: "valid full", cfg: model.DeviceKafkaConfig{Enabled: true, Topic: "plc-values", Mode: model.KafkaModeFull, FullIntervalSec: 1}},
		{name: "invalid mode", cfg: model.DeviceKafkaConfig{Enabled: true, Topic: "plc", Mode: "full,change", FullIntervalSec: 60}, wantErr: true},
		{name: "invalid topic", cfg: model.DeviceKafkaConfig{Enabled: true, Topic: "bad topic", Mode: model.KafkaModeFull, FullIntervalSec: 60}, wantErr: true},
		{name: "reserved topic", cfg: model.DeviceKafkaConfig{Enabled: true, Topic: ".", Mode: model.KafkaModeFull, FullIntervalSec: 60}, wantErr: true},
		{name: "invalid interval", cfg: model.DeviceKafkaConfig{Mode: model.KafkaModeFull, FullIntervalSec: 0}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if (ValidateDeviceConfig(tt.cfg) != nil) != tt.wantErr {
				t.Fatalf("ValidateDeviceConfig(%#v) error mismatch", tt.cfg)
			}
		})
	}
}

func TestValidateSettingsRequiresBrokerOnlyWhenEnabled(t *testing.T) {
	disabled := model.KafkaSettings{ClientID: "modbus-scan", KafkaVersion: "3.0.0", QueueCapacity: 1000}
	if _, err := NormalizeSettings(disabled, t.TempDir()); err != nil {
		t.Fatalf("disabled settings error = %v", err)
	}
	enabled := disabled
	enabled.Enabled = true
	if _, err := NormalizeSettings(enabled, t.TempDir()); err == nil {
		t.Fatal("enabled settings without brokers accepted")
	}
	enabled.Brokers = []string{"127.0.0.1:9092"}
	if _, err := NormalizeSettings(enabled, t.TempDir()); err != nil {
		t.Fatalf("valid enabled settings error = %v", err)
	}
}

func TestNormalizeSettingsRejectsInvalidSecurityCombinations(t *testing.T) {
	base := model.KafkaSettings{Enabled: true, Brokers: []string{"127.0.0.1:9092"}, ClientID: "client", KafkaVersion: "3.0.0", QueueCapacity: 1000}
	tests := []struct {
		name string
		edit func(*model.KafkaSettings)
	}{
		{name: "bad protocol", edit: func(c *model.KafkaSettings) { c.SecurityProtocol = "BAD" }},
		{name: "SASL without mechanism", edit: func(c *model.KafkaSettings) { c.SecurityProtocol = "SASL_PLAINTEXT" }},
		{name: "certificate without key", edit: func(c *model.KafkaSettings) { c.SecurityProtocol = "SSL"; c.SSLCertificatePath = "client.crt" }},
		{name: "bad endpoint identification", edit: func(c *model.KafkaSettings) { c.SecurityProtocol = "SSL"; c.SSLEndpointIdentificationAlgorithm = "bad" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.edit(&cfg)
			if _, err := NormalizeSettings(cfg, t.TempDir()); err == nil {
				t.Fatalf("settings accepted: %#v", cfg)
			}
		})
	}
}
