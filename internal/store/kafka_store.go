package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"modbus-scan/internal/model"
)

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// GetKafkaSettings returns the singleton Kafka connection settings.
func (s *Store) GetKafkaSettings(ctx context.Context) (model.KafkaSettings, error) {
	var cfg model.KafkaSettings
	var enabled int
	var brokers string
	err := s.db.QueryRowContext(ctx, `SELECT enabled, brokers, client_id, kafka_version, security_protocol, sasl_mechanism, sasl_username, sasl_password, ssl_ca_location, ssl_certificate_location, ssl_key_location, ssl_endpoint_identification_algorithm, queue_capacity, updated_at FROM kafka_settings WHERE id=1`).Scan(
		&enabled, &brokers, &cfg.ClientID, &cfg.KafkaVersion, &cfg.SecurityProtocol, &cfg.SASLMechanism,
		&cfg.SASLUsername, &cfg.SASLPassword, &cfg.SSLCAPath, &cfg.SSLCertificatePath, &cfg.SSLKeyPath,
		&cfg.SSLEndpointIdentificationAlgorithm, &cfg.QueueCapacity, &cfg.UpdatedAt,
	)
	if err != nil {
		return model.KafkaSettings{}, mapError("get Kafka settings", err)
	}
	cfg.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(brokers), &cfg.Brokers); err != nil {
		return model.KafkaSettings{}, fmt.Errorf("decode Kafka brokers: %w", err)
	}
	return cfg, nil
}

// UpdateKafkaSettings replaces the singleton Kafka connection settings.
func (s *Store) UpdateKafkaSettings(ctx context.Context, cfg model.KafkaSettings) (model.KafkaSettings, error) {
	brokers, err := json.Marshal(cfg.Brokers)
	if err != nil {
		return model.KafkaSettings{}, fmt.Errorf("encode Kafka brokers: %w", err)
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE kafka_settings SET enabled=?, brokers=?, client_id=?, kafka_version=?, security_protocol=?, sasl_mechanism=?, sasl_username=?, sasl_password=?, ssl_ca_location=?, ssl_certificate_location=?, ssl_key_location=?, ssl_endpoint_identification_algorithm=?, queue_capacity=?, updated_at=? WHERE id=1`,
		boolInt(cfg.Enabled), string(brokers), cfg.ClientID, cfg.KafkaVersion, cfg.SecurityProtocol, cfg.SASLMechanism,
		cfg.SASLUsername, cfg.SASLPassword, cfg.SSLCAPath, cfg.SSLCertificatePath, cfg.SSLKeyPath,
		cfg.SSLEndpointIdentificationAlgorithm, cfg.QueueCapacity, now,
	)
	if err != nil {
		return model.KafkaSettings{}, mapError("update Kafka settings", err)
	}
	if err := requireAffected("update Kafka settings", result); err != nil {
		return model.KafkaSettings{}, err
	}
	return s.GetKafkaSettings(ctx)
}

// GetDeviceKafkaConfig returns one device's Kafka publishing configuration.
func (s *Store) GetDeviceKafkaConfig(ctx context.Context, deviceID int64) (model.DeviceKafkaConfig, error) {
	var cfg model.DeviceKafkaConfig
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT device_id, enabled, topic, mode, full_interval_sec, created_at, updated_at FROM device_kafka_configs WHERE device_id=?`, deviceID).Scan(
		&cfg.DeviceID, &enabled, &cfg.Topic, &cfg.Mode, &cfg.FullIntervalSec, &cfg.CreatedAt, &cfg.UpdatedAt,
	)
	if err != nil {
		return model.DeviceKafkaConfig{}, mapError("get device Kafka config", err)
	}
	cfg.Enabled = enabled != 0
	return cfg, nil
}

// ListDeviceKafkaConfigs returns every device publishing configuration.
func (s *Store) ListDeviceKafkaConfigs(ctx context.Context) ([]model.DeviceKafkaConfig, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT device_id, enabled, topic, mode, full_interval_sec, created_at, updated_at FROM device_kafka_configs ORDER BY device_id`)
	if err != nil {
		return nil, fmt.Errorf("list device Kafka configs: %w", err)
	}
	defer rows.Close()
	configs := make([]model.DeviceKafkaConfig, 0)
	for rows.Next() {
		var cfg model.DeviceKafkaConfig
		var enabled int
		if err := rows.Scan(&cfg.DeviceID, &enabled, &cfg.Topic, &cfg.Mode, &cfg.FullIntervalSec, &cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan device Kafka config: %w", err)
		}
		cfg.Enabled = enabled != 0
		configs = append(configs, cfg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list device Kafka configs: %w", err)
	}
	return configs, nil
}

// UpdateDeviceKafkaConfig replaces one device's Kafka publishing configuration.
func (s *Store) UpdateDeviceKafkaConfig(ctx context.Context, cfg model.DeviceKafkaConfig) (model.DeviceKafkaConfig, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE device_kafka_configs SET enabled=?, topic=?, mode=?, full_interval_sec=?, updated_at=? WHERE device_id=?`,
		boolInt(cfg.Enabled), cfg.Topic, cfg.Mode, cfg.FullIntervalSec, time.Now().UTC(), cfg.DeviceID,
	)
	if err != nil {
		return model.DeviceKafkaConfig{}, mapError("update device Kafka config", err)
	}
	if err := requireAffected("update device Kafka config", result); err != nil {
		return model.DeviceKafkaConfig{}, err
	}
	return s.GetDeviceKafkaConfig(ctx, cfg.DeviceID)
}
