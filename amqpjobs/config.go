package amqpjobs

import (
	"crypto/tls"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/roadrunner-server/errors"
)

// ClientAuthType TSL auth type
type ClientAuthType string

const (
	NoClientCert               ClientAuthType = "no_client_cert"
	RequestClientCert          ClientAuthType = "request_client_cert"
	RequireAnyClientCert       ClientAuthType = "require_any_client_cert"
	VerifyClientCertIfGiven    ClientAuthType = "verify_client_cert_if_given"
	RequireAndVerifyClientCert ClientAuthType = "require_and_verify_client_cert"
)

// pipeline amqp info
const (
	exchangeKey     string = "exchange"
	exchangeType    string = "exchange_type"
	queue           string = "queue"
	routingKey      string = "routing_key"
	exchangeDeclare string = "exchange_declare"
	queueDeclare    string = "queue_declare"

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

type pipelineConfigV2 struct {
	Prefetch int               `mapstructure:"prefetch"`
	Priority int64             `mapstructure:"priority"`
	Exchange *exchangeConfigV2 `mapstructure:"exchange"`
	Queue    *queueConfigV2    `mapstructure:"queue"`

	DeleteQueueOnStop bool   `mapstructure:"delete_queue_on_stop"`
	MultipleAck       bool   `mapstructure:"multiple_ack"`
	RequeueOnFail     bool   `mapstructure:"requeue_on_fail"`
	RedialTimeout     int    `mapstructure:"redial_timeout"`
	ConsumerID        string `mapstructure:"consumer_id"`
}

type exchangeConfigV2 struct {
	Name       string `mapstructure:"name"`
	Type       string `mapstructure:"type"`
	Durable    bool   `mapstructure:"durable"`
	AutoDelete bool   `mapstructure:"auto_delete"`
	Declare    *bool  `mapstructure:"declare"`
}

type queueConfigV2 struct {
	Name       string         `mapstructure:"name"`
	RoutingKey string         `mapstructure:"routing_key"`
	Durable    bool           `mapstructure:"durable"`
	AutoDelete bool           `mapstructure:"auto_delete"`
	Exclusive  bool           `mapstructure:"exclusive"`
	Headers    map[string]any `mapstructure:"headers"`
	Declare    *bool          `mapstructure:"declare"`
}

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

	// exchange/queue declaration controls (new, defaults to true)
	ExchangeDeclare bool `mapstructure:"exchange_declare"`
	QueueDeclare    bool `mapstructure:"queue_declare"`

	// set flags are used to keep "default true" while still allowing explicit false.
	exchangeDeclareSet bool
	queueDeclareSet    bool
}

// TLS configuration
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

	if err := validateExchangeType(c.ExchangeType); err != nil {
		return errors.E(op, err)
	}

	if c.Exchange == "" {
		c.Exchange = "amqp.default"
	}

	if c.RedialTimeout <= 0 {
		c.RedialTimeout = 60
	}

	if c.Prefetch <= 0 {
		c.Prefetch = 10
	}

	if c.Priority <= 0 {
		c.Priority = 10
	}

	if c.Addr == "" {
		c.Addr = "amqp://guest:guest@127.0.0.1:5672/"
	}

	if c.ConsumerID == "" {
		c.ConsumerID = fmt.Sprintf("roadrunner-%s", uuid.NewString())
	}

	if !c.exchangeDeclareSet {
		c.ExchangeDeclare = true
	}

	if !c.queueDeclareSet {
		c.QueueDeclare = true
	}

	if c.enableTLS() {
		if err := c.validateTLS(op); err != nil {
			return err
		}
	}

	return nil
}

func (c *config) validateTLS(op errors.Op) error {
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

	return nil
}

// validateExchangeType checks that the exchange type is a valid AMQP exchange type.
func validateExchangeType(t string) error {
	switch t {
	case "direct", "fanout", "topic", "headers":
		return nil
	default:
		return errors.Errorf("invalid exchange type %q, must be one of: direct, fanout, topic, headers", t)
	}
}

func (c *config) enableTLS() bool {
	if c.TLS != nil {
		return (c.TLS.RootCA != "" && c.TLS.Key != "" && c.TLS.Cert != "") || (c.TLS.Key != "" && c.TLS.Cert != "")
	}
	return false
}

func (c *config) applyPipelineConfigV2(raw pipelineConfigV2) {
	c.Prefetch = raw.Prefetch
	c.Priority = raw.Priority
	c.DeleteQueueOnStop = raw.DeleteQueueOnStop
	c.MultipleAck = raw.MultipleAck
	c.RequeueOnFail = raw.RequeueOnFail
	c.RedialTimeout = raw.RedialTimeout
	c.ConsumerID = raw.ConsumerID

	if raw.Exchange != nil {
		c.Exchange = raw.Exchange.Name
		c.ExchangeType = raw.Exchange.Type
		c.ExchangeDurable = raw.Exchange.Durable
		c.ExchangeAutoDelete = raw.Exchange.AutoDelete

		if raw.Exchange.Declare != nil {
			c.ExchangeDeclare = *raw.Exchange.Declare
			c.exchangeDeclareSet = true
		}
	}

	if raw.Queue != nil {
		c.Queue = raw.Queue.Name
		c.RoutingKey = raw.Queue.RoutingKey
		c.Durable = raw.Queue.Durable
		c.QueueAutoDelete = raw.Queue.AutoDelete
		c.Exclusive = raw.Queue.Exclusive
		c.QueueHeaders = raw.Queue.Headers

		if raw.Queue.Declare != nil {
			c.QueueDeclare = *raw.Queue.Declare
			c.queueDeclareSet = true
		}
	}
}
