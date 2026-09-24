package kafkapub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/IBM/sarama"
	"github.com/xdg-go/scram"

	"modbus-scan/internal/model"
)

// ProducerMessage is one encoded Kafka record.
type ProducerMessage struct {
	Topic string
	Key   string
	Value []byte
}

// Producer sends encoded Kafka records.
type Producer interface {
	Send(context.Context, ProducerMessage) error
	Close() error
}

// ProducerFactory opens configured producers.
type ProducerFactory interface {
	Open(context.Context, model.KafkaSettings) (Producer, error)
}

// SaramaProducerFactory creates synchronous Sarama producers used by the background worker.
type SaramaProducerFactory struct{}

// Open creates and verifies a Sarama producer.
func (SaramaProducerFactory) Open(_ context.Context, settings model.KafkaSettings) (Producer, error) {
	version, err := sarama.ParseKafkaVersion(settings.KafkaVersion)
	if err != nil {
		return nil, fmt.Errorf("parse Kafka version: %w", err)
	}
	cfg := sarama.NewConfig()
	cfg.Version = version
	cfg.ClientID = settings.ClientID
	cfg.Producer.RequiredAcks = sarama.WaitForLocal
	cfg.Producer.Retry.Max = 3
	cfg.Producer.Retry.Backoff = time.Second
	cfg.Producer.Return.Successes = true
	cfg.Producer.Partitioner = sarama.NewHashPartitioner
	if settings.SecurityProtocol == "SASL_PLAINTEXT" || settings.SecurityProtocol == "SASL_SSL" {
		cfg.Net.SASL.Enable = true
		cfg.Net.SASL.User = settings.SASLUsername
		cfg.Net.SASL.Password = settings.SASLPassword
		switch settings.SASLMechanism {
		case "PLAIN":
			cfg.Net.SASL.Mechanism = sarama.SASLTypePlaintext
		case "SCRAM-SHA-256":
			cfg.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
			cfg.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &scramClient{hash: scram.SHA256} }
		case "SCRAM-SHA-512":
			cfg.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
			cfg.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &scramClient{hash: scram.SHA512} }
		}
	}
	if settings.SecurityProtocol == "SSL" || settings.SecurityProtocol == "SASL_SSL" {
		tlsConfig, err := buildTLSConfig(settings)
		if err != nil {
			return nil, err
		}
		cfg.Net.TLS.Enable = true
		cfg.Net.TLS.Config = tlsConfig
	}
	producer, err := sarama.NewSyncProducer(settings.Brokers, cfg)
	if err != nil {
		return nil, fmt.Errorf("open Kafka producer: %w", err)
	}
	return &saramaProducer{producer: producer}, nil
}

func buildTLSConfig(settings model.KafkaSettings) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: settings.SSLEndpointIdentificationAlgorithm == "none"} //nolint:gosec // Explicit operator setting.
	if settings.SSLCAPath != "" {
		pem, err := os.ReadFile(settings.SSLCAPath)
		if err != nil {
			return nil, fmt.Errorf("read Kafka CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("parse Kafka CA")
		}
		cfg.RootCAs = pool
	}
	if settings.SSLCertificatePath != "" {
		certificate, err := tls.LoadX509KeyPair(settings.SSLCertificatePath, settings.SSLKeyPath)
		if err != nil {
			return nil, fmt.Errorf("load Kafka client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{certificate}
	}
	return cfg, nil
}

type saramaProducer struct{ producer sarama.SyncProducer }

type scramClient struct {
	hash         scram.HashGeneratorFcn
	client       *scram.Client
	conversation *scram.ClientConversation
}

func (c *scramClient) Begin(userName, password, authzID string) error {
	client, err := c.hash.NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	c.client = client
	c.conversation = client.NewConversation()
	return nil
}

func (c *scramClient) Step(challenge string) (string, error) { return c.conversation.Step(challenge) }
func (c *scramClient) Done() bool                            { return c.conversation.Done() }

func (p *saramaProducer) Send(_ context.Context, message ProducerMessage) error {
	_, _, err := p.producer.SendMessage(&sarama.ProducerMessage{Topic: message.Topic, Key: sarama.StringEncoder(message.Key), Value: sarama.ByteEncoder(message.Value)})
	return err
}
func (p *saramaProducer) Close() error { return p.producer.Close() }
