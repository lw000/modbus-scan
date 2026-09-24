package model

import "time"

const (
	// KafkaModeFull publishes complete device snapshots.
	KafkaModeFull = "full"
	// KafkaModeChange publishes only changed point values.
	KafkaModeChange = "change"
)

// KafkaSettings contains the singleton Kafka connection configuration.
type KafkaSettings struct {
	Enabled                            bool      `json:"enabled"`
	Brokers                            []string  `json:"brokers"`
	ClientID                           string    `json:"client_id"`
	KafkaVersion                       string    `json:"kafka_version"`
	SecurityProtocol                   string    `json:"security_protocol"`
	SASLMechanism                      string    `json:"sasl_mechanism"`
	SASLUsername                       string    `json:"sasl_username"`
	SASLPassword                       string    `json:"-"`
	SSLCAPath                          string    `json:"ssl_ca_location"`
	SSLCertificatePath                 string    `json:"ssl_certificate_location"`
	SSLKeyPath                         string    `json:"ssl_key_location"`
	SSLEndpointIdentificationAlgorithm string    `json:"ssl_endpoint_identification_algorithm"`
	QueueCapacity                      int       `json:"queue_capacity"`
	UpdatedAt                          time.Time `json:"updated_at"`
}

// DeviceKafkaConfig contains one device's publishing policy.
type DeviceKafkaConfig struct {
	DeviceID        int64     `json:"device_id"`
	Enabled         bool      `json:"enabled"`
	Topic           string    `json:"topic"`
	Mode            string    `json:"mode"`
	FullIntervalSec int       `json:"full_interval_sec"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// KafkaStatus describes the live producer state without exposing credentials.
type KafkaStatus struct {
	State           string `json:"state"`
	LastError       string `json:"last_error,omitempty"`
	DroppedMessages uint64 `json:"dropped_messages"`
}
