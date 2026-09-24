// Package kafkapub coordinates and publishes collected device values to Kafka.
package kafkapub

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/IBM/sarama"

	"modbus-scan/internal/model"
)

var topicPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// NormalizeSettings trims, resolves, and validates global Kafka settings.
func NormalizeSettings(cfg model.KafkaSettings, baseDir string) (model.KafkaSettings, error) {
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.KafkaVersion = strings.TrimSpace(cfg.KafkaVersion)
	cfg.SecurityProtocol = strings.ToUpper(strings.TrimSpace(cfg.SecurityProtocol))
	cfg.SASLMechanism = strings.ToUpper(strings.TrimSpace(cfg.SASLMechanism))
	cfg.SASLUsername = strings.TrimSpace(cfg.SASLUsername)
	cfg.SSLEndpointIdentificationAlgorithm = strings.ToLower(strings.TrimSpace(cfg.SSLEndpointIdentificationAlgorithm))
	for i := range cfg.Brokers {
		cfg.Brokers[i] = strings.TrimSpace(cfg.Brokers[i])
		if _, _, err := net.SplitHostPort(cfg.Brokers[i]); err != nil {
			return model.KafkaSettings{}, fmt.Errorf("broker %q must use host:port: %w", cfg.Brokers[i], err)
		}
	}
	if cfg.Enabled && len(cfg.Brokers) == 0 {
		return model.KafkaSettings{}, fmt.Errorf("at least one broker is required when Kafka is enabled")
	}
	if cfg.ClientID == "" {
		return model.KafkaSettings{}, fmt.Errorf("client ID must not be empty")
	}
	if _, err := sarama.ParseKafkaVersion(cfg.KafkaVersion); err != nil {
		return model.KafkaSettings{}, fmt.Errorf("parse Kafka version: %w", err)
	}
	if cfg.QueueCapacity < 1 || cfg.QueueCapacity > 100000 {
		return model.KafkaSettings{}, fmt.Errorf("queue capacity must be between 1 and 100000")
	}
	switch cfg.SecurityProtocol {
	case "", "SASL_PLAINTEXT", "SSL", "SASL_SSL":
	default:
		return model.KafkaSettings{}, fmt.Errorf("unsupported security protocol %q", cfg.SecurityProtocol)
	}
	usesSASL := cfg.SecurityProtocol == "SASL_PLAINTEXT" || cfg.SecurityProtocol == "SASL_SSL"
	if usesSASL {
		switch cfg.SASLMechanism {
		case "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512":
		default:
			return model.KafkaSettings{}, fmt.Errorf("unsupported SASL mechanism %q", cfg.SASLMechanism)
		}
	} else if cfg.SASLMechanism != "" {
		return model.KafkaSettings{}, fmt.Errorf("SASL mechanism requires a SASL security protocol")
	}
	if (cfg.SSLCertificatePath == "") != (cfg.SSLKeyPath == "") {
		return model.KafkaSettings{}, fmt.Errorf("SSL certificate and key must be configured together")
	}
	switch cfg.SSLEndpointIdentificationAlgorithm {
	case "", "none", "https":
	default:
		return model.KafkaSettings{}, fmt.Errorf("SSL endpoint identification algorithm must be none or https")
	}
	cfg.SSLCAPath = resolveOptionalPath(baseDir, cfg.SSLCAPath)
	cfg.SSLCertificatePath = resolveOptionalPath(baseDir, cfg.SSLCertificatePath)
	cfg.SSLKeyPath = resolveOptionalPath(baseDir, cfg.SSLKeyPath)
	return cfg, nil
}

func resolveOptionalPath(baseDir, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(baseDir, value))
}

// ValidateDeviceConfig validates one device publishing policy.
func ValidateDeviceConfig(cfg model.DeviceKafkaConfig) error {
	cfg.Topic = strings.TrimSpace(cfg.Topic)
	if cfg.Mode != model.KafkaModeFull && cfg.Mode != model.KafkaModeChange {
		return fmt.Errorf("mode must be full or change")
	}
	if cfg.FullIntervalSec < 1 || cfg.FullIntervalSec > 86400 {
		return fmt.Errorf("full interval must be between 1 and 86400 seconds")
	}
	if !cfg.Enabled {
		return nil
	}
	if len(cfg.Topic) < 1 || len(cfg.Topic) > 249 || !topicPattern.MatchString(cfg.Topic) || cfg.Topic == "." || cfg.Topic == ".." {
		return fmt.Errorf("topic must be a valid Kafka topic with 1 to 249 characters")
	}
	return nil
}
