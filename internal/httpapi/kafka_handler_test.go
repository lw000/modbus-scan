package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"modbus-scan/internal/model"
	"modbus-scan/internal/service"
)

type stubKafka struct {
	settings service.KafkaSettingsView
	device   model.DeviceKafkaConfig
}

func (s *stubKafka) GetSettings(context.Context) (service.KafkaSettingsView, error) {
	return s.settings, nil
}
func (s *stubKafka) UpdateSettings(_ context.Context, input service.KafkaSettingsInput) (service.KafkaSettingsView, error) {
	s.settings.Settings = input.KafkaSettings
	return s.settings, nil
}
func (s *stubKafka) GetDeviceConfig(context.Context, int64) (model.DeviceKafkaConfig, error) {
	return s.device, nil
}
func (s *stubKafka) UpdateDeviceConfig(_ context.Context, id int64, cfg model.DeviceKafkaConfig) (model.DeviceKafkaConfig, error) {
	cfg.DeviceID = id
	s.device = cfg
	return cfg, nil
}

func TestGetKafkaNeverReturnsPassword(t *testing.T) {
	kafka := &stubKafka{settings: service.KafkaSettingsView{Settings: model.KafkaSettings{SASLPassword: "secret"}}}
	router := NewRouterWithKafka(slog.New(slog.NewTextHandler(io.Discard, nil)), &stubDevices{}, &stubPoints{}, kafka, nil, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/kafka", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "sasl_password") {
		t.Fatalf("response leaks password: %s", recorder.Body.String())
	}
}

func TestPutDeviceKafkaUsesPathDeviceID(t *testing.T) {
	kafka := &stubKafka{}
	router := NewRouterWithKafka(slog.New(slog.NewTextHandler(io.Discard, nil)), &stubDevices{}, &stubPoints{}, kafka, nil, nil)
	body := strings.NewReader(`{"device_id":99,"enabled":true,"topic":"plc","mode":"change","full_interval_sec":60}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/devices/7/kafka", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || kafka.device.DeviceID != 7 {
		t.Fatalf("status=%d device=%#v", recorder.Code, kafka.device)
	}
}
