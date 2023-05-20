package amqpjobs

import (
	"crypto/tls"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/roadrunner-server/errors"
)

// TSL auth type
type ClientAuthType string

const (
	NoClientCert               ClientAuthType = "no_client_cert"
	RequestClientCert          ClientAuthType = "request_client_cert"
	RequireAnyClientCert       ClientAuthType = "require_any_client_cert"
	VerifyClientCertIfGiven    ClientAuthType = "verify_client_cert_if_given"
	RequireAndVerifyClientCert ClientAuthType = "require_and_verify_client_cert"
)

// pipeline rabbitmq info
const (
	exchangeKey   string = "exchange"
	exchangeType  string = "exchange_type"
	queue         string = "queue"
	routingKey    string = "routing_key"
	consumeAll    string = "consume_all"
	prefetch      string = "prefetch"
	exclusive     string = "exclusive"
	durable       string = "durable"
	deleteOnStop  string = "delete_queue_on_stop"
	priority      string = "priority"
	multipleAck   string = "multiple_ack"
	requeueOnFail string = "requeue_on_fail"

	// new in 2.12
	redialTimeout      string = "redial_timeout"
	exchangeDurable    string = "exchange_durable"
	exchangeAutoDelete string = "exchange_auto_delete"
	queueAutoDelete    string = "queue_auto_delete"

	dlx           string = "x-dead-letter-exchange"
	dlxRoutingKey string = "x-dead-letter-routing-key"
	dlxTTL        string = "x-message-ttl"
	dlxExpires    string = "x-expires"

	// new in 2.12.2
	queueHeaders string = "queue_headers"

	// new in 2023.1.0
	consumerIDKey string = "consumer_id"

	contentType string = "application/octet-stream"
)

// config is used to parse pipeline configuration
type config struct {
	// global
	Addr string `mapstructure:"addr"`

	// global TLS option
	TLS *TLS `mapstructure:"tls"`

	// local
	Prefetch     int    `mapstructure:"prefetch"`
	Queue        string `mapstructure:"queue"`
	Priority     int64  `mapstructure:"priority"`
	Exchange     string `mapstructure:"exchange"`
	ExchangeType string `mapstructure:"exchange_type"`

	RoutingKey        string `mapstructure:"routing_key"`
	ConsumeAll        bool   `mapstructure:"consume_all"`
	Exclusive         bool   `mapstructure:"exclusive"`
	Durable           bool   `mapstructure:"durable"`
	DeleteQueueOnStop bool   `mapstructure:"delete_queue_on_stop"`
	MultipleAck       bool   `mapstructure:"multiple_ack"`
	RequeueOnFail     bool   `mapstructure:"requeue_on_fail"`

	// new in 2.12.1
	ExchangeDurable    bool `mapstructure:"exchange_durable"`
	ExchangeAutoDelete bool `mapstructure:"exchange_auto_delete"`
	QueueAutoDelete    bool `mapstructure:"queue_auto_delete"`
	RedialTimeout      int  `mapstructure:"redial_timeout"`

	// new in 2.12.2
	QueueHeaders map[string]any `mapstructure:"queue_headers"`
	// new in 2023.1.0
	ConsumerID string `mapstructure:"consumer_id"`
}

// TLS
type TLS struct {
	RootCA   string         `mapstructure:"root_ca"`
	Key      string         `mapstructure:"key"`
	Cert     string         `mapstructure:"cert"`
	AuthType ClientAuthType `mapstructure:"client_auth_type"`
	// auth type internal
	auth tls.ClientAuthType
}

func (c *config) InitDefault() error {
	const op = errors.Op("amqp_init_default")
	// all options should be in sync with the pipeline defaults in the ConsumerFromPipeline method
	if c.ExchangeType == "" {
		c.ExchangeType = "direct"
	}

	if c.Exchange == "" {
		c.Exchange = "amqp.default"
	}

	if c.RedialTimeout == 0 {
		c.RedialTimeout = 60
	}

	if c.Prefetch == 0 {
		c.Prefetch = 10
	}

	if c.Priority == 0 {
		c.Priority = 10
	}

	if c.Addr == "" {
		c.Addr = "amqp://guest:guest@127.0.0.1:5672/"
	}

	if c.ConsumerID == "" {
		c.ConsumerID = fmt.Sprintf("roadrunner-%s", uuid.NewString())
	}

	if c.enableTLS() {
		if _, err := os.Stat(c.TLS.Key); err != nil {
			if os.IsNotExist(err) {
				return errors.E(op, errors.Errorf("key file '%s' does not exists", c.TLS.Key))
			}

			return errors.E(op, err)
		}

		if _, err := os.Stat(c.TLS.Cert); err != nil {
			if os.IsNotExist(err) {
				return errors.E(op, errors.Errorf("cert file '%s' does not exists", c.TLS.Cert))
			}

			return errors.E(op, err)
		}

		// RootCA is optional, but if provided - check it
		if c.TLS.RootCA != "" {
			if _, err := os.Stat(c.TLS.RootCA); err != nil {
				if os.IsNotExist(err) {
					return errors.E(op, errors.Errorf("root ca path provided, but key file '%s' does not exists", c.TLS.RootCA))
				}
				return errors.E(op, err)
			}

			// auth type used only for the CA
			switch c.TLS.AuthType {
			case NoClientCert:
				c.TLS.auth = tls.NoClientCert
			case RequestClientCert:
				c.TLS.auth = tls.RequestClientCert
			case RequireAnyClientCert:
				c.TLS.auth = tls.RequireAnyClientCert
			case VerifyClientCertIfGiven:
				c.TLS.auth = tls.VerifyClientCertIfGiven
			case RequireAndVerifyClientCert:
				c.TLS.auth = tls.RequireAndVerifyClientCert
			default:
				c.TLS.auth = tls.NoClientCert
			}
		}
	}

	return nil
}

func (c *config) enableTLS() bool {
	if c.TLS != nil {
		return (c.TLS.RootCA != "" && c.TLS.Key != "" && c.TLS.Cert != "") || (c.TLS.Key != "" && c.TLS.Cert != "")
	}
	return false
}
