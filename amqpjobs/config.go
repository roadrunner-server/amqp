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
	exchangeKey  string = "exchange"
	exchangeType string = "exchange_type"
	queue        string = "queue"
	routingKey   string = "routing_key"
	// new options to control the declaration of exchange and queue, if not set - both will be declared by default
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
	contentType   string = "application/octet-stream"
)

// v2 nested exchange configuration.
type exchangeConfigV2 struct {
	Name       string `mapstructure:"name"`
	Type       string `mapstructure:"type"`
	Durable    bool   `mapstructure:"durable"`
	AutoDelete bool   `mapstructure:"auto_delete"`
	Declare    *bool  `mapstructure:"declare"`
}

// v2 nested queue configuration.
type queueConfigV2 struct {
	Name          string         `mapstructure:"name"`
	RoutingKey    string         `mapstructure:"routing_key"`
	Durable       bool           `mapstructure:"durable"`
	AutoDelete    bool           `mapstructure:"auto_delete"`
	Exclusive     bool           `mapstructure:"exclusive"`
	DeleteOnStop  bool           `mapstructure:"delete_on_stop"`
	MultipleAck   bool           `mapstructure:"multiple_ack"`
	RequeueOnFail bool           `mapstructure:"requeue_on_fail"`
	ConsumerID    string         `mapstructure:"consumer_id"`
	Headers       map[string]any `mapstructure:"headers"`
	Declare       *bool          `mapstructure:"declare"`
}

// legacy flat v1 pipeline configuration.
type v1config struct {
	Queue        string `mapstructure:"queue"`
	Exchange     string `mapstructure:"exchange"`
	ExchangeType string `mapstructure:"exchange_type"`
	RoutingKey   string `mapstructure:"routing_key"`

	Exclusive         bool `mapstructure:"exclusive"`
	Durable           bool `mapstructure:"durable"`
	DeleteQueueOnStop bool `mapstructure:"delete_queue_on_stop"`
	MultipleAck       bool `mapstructure:"multiple_ack"`
	RequeueOnFail     bool `mapstructure:"requeue_on_fail"`

	ExchangeDurable    bool `mapstructure:"exchange_durable"`
	ExchangeAutoDelete bool `mapstructure:"exchange_auto_delete"`
	QueueAutoDelete    bool `mapstructure:"queue_auto_delete"`

	QueueHeaders map[string]any `mapstructure:"queue_headers"`

	ConsumerID string `mapstructure:"consumer_id"`
}

type v2config struct {
	ExchangeConfig *exchangeConfigV2 `mapstructure:"exchange"`
	QueueConfig    *queueConfigV2    `mapstructure:"queue"`
}

// config is the canonical static YAML model.
type config struct {
	// global
	Addr string `mapstructure:"addr"`
	TLS  *TLS   `mapstructure:"tls"`

	// local/common
	Version       int   `mapstructure:"version"`
	Prefetch      int   `mapstructure:"prefetch"`
	Priority      int64 `mapstructure:"priority"`
	RedialTimeout int   `mapstructure:"redial_timeout"`

	// v1 and v2 are unmarshalled separately.
	V1Config *v1config `mapstructure:"-"`
	V2Config *v2config `mapstructure:"-"`
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
	const op = errors.Op("amqp_config_init_default")

	if c.Addr == "" {
		c.Addr = "amqp://guest:guest@127.0.0.1:5672/"
	}

	if c.Prefetch <= 0 {
		c.Prefetch = 10
	}

	if c.Priority <= 0 {
		c.Priority = 10
	}

	if c.RedialTimeout <= 0 {
		c.RedialTimeout = 60
	}

	switch c.Version {
	case 0, 1:
		if c.V1Config == nil {
			c.V1Config = &v1config{}
		}

		if err := c.V1Config.InitDefault(); err != nil {
			return err
		}
	case 2:
		if c.V2Config == nil {
			c.V2Config = &v2config{}
		}

		if err := c.V2Config.InitDefault(); err != nil {
			return err
		}
	default:
		return errors.E(op, errors.Errorf("unsupported AMQP pipeline config version: %d", c.Version))
	}

	if c.enableTLS() {
		if err := c.validateTLS(op); err != nil {
			return err
		}
	}

	// If exchange is not fanout, routing key is required.
	if c.routingKeyName() == "" && c.exchangeTypeName() != "fanout" {
		return errors.E(op, errors.Str("empty routing key, consider adding the routing key name to the AMQP configuration"))
	}

	return nil
}

func (c *v1config) InitDefault() error {
	const op = errors.Op("amqp_init_default")

	if c.ExchangeType == "" {
		//nolint:goconst
		c.ExchangeType = "direct"
	}

	if err := validateExchangeType(c.ExchangeType); err != nil {
		return errors.E(op, err)
	}

	if c.Exchange == "" {
		c.Exchange = "amqp.default"
	}

	if c.ConsumerID == "" {
		c.ConsumerID = fmt.Sprintf("roadrunner-%s", uuid.NewString())
	}

	return nil
}

func (c *v2config) InitDefault() error {
	const op = errors.Op("amqp_init_default")

	// Version 2 requires at least one entity section configured.
	if c.ExchangeConfig == nil && c.QueueConfig == nil {
		return errors.E(op, errors.Str("exchange or queue configuration is required for AMQP config version 2"))
	}

	if c.ExchangeConfig == nil {
		c.ExchangeConfig = &exchangeConfigV2{}
	}

	if c.QueueConfig == nil {
		c.QueueConfig = &queueConfigV2{}
	}

	if c.ExchangeConfig.Type == "" {
		c.ExchangeConfig.Type = "direct"
	}

	if err := validateExchangeType(c.ExchangeConfig.Type); err != nil {
		return errors.E(op, err)
	}

	if c.ExchangeConfig.Name == "" {
		c.ExchangeConfig.Name = "amqp.default"
	}

	// Keep consumer ID default format consistent with v1.
	if c.QueueConfig.ConsumerID == "" {
		c.QueueConfig.ConsumerID = fmt.Sprintf("roadrunner-%s", uuid.NewString())
	}

	return nil
}

func (c *config) queueName() string {
	if c.Version == 2 {
		return c.V2Config.QueueConfig.Name
	}
	return c.V1Config.Queue
}

func (c *config) exchangeName() string {
	if c.Version == 2 {
		return c.V2Config.ExchangeConfig.Name
	}
	return c.V1Config.Exchange
}

func (c *config) exchangeTypeName() string {
	if c.Version == 2 {
		return c.V2Config.ExchangeConfig.Type
	}
	return c.V1Config.ExchangeType
}

func (c *config) routingKeyName() string {
	if c.Version == 2 {
		return c.V2Config.QueueConfig.RoutingKey
	}
	return c.V1Config.RoutingKey
}

func (c *config) queueDurable() bool {
	if c.Version == 2 {
		return c.V2Config.QueueConfig.Durable
	}
	return c.V1Config.Durable
}

func (c *config) queueAutoDelete() bool {
	if c.Version == 2 {
		return c.V2Config.QueueConfig.AutoDelete
	}
	return c.V1Config.QueueAutoDelete
}

func (c *config) queueExclusive() bool {
	if c.Version == 2 {
		return c.V2Config.QueueConfig.Exclusive
	}
	return c.V1Config.Exclusive
}

func (c *config) queueHeadersArgs() map[string]any {
	if c.Version == 2 {
		return c.V2Config.QueueConfig.Headers
	}
	return c.V1Config.QueueHeaders
}

func (c *config) exchangeDurable() bool {
	if c.Version == 2 {
		return c.V2Config.ExchangeConfig.Durable
	}
	return c.V1Config.ExchangeDurable
}

func (c *config) exchangeAutoDelete() bool {
	if c.Version == 2 {
		return c.V2Config.ExchangeConfig.AutoDelete
	}
	return c.V1Config.ExchangeAutoDelete
}

func (c *config) exchangeDeclareEnabled() bool {
	if c.Version != 2 {
		return true
	}

	if c.V2Config.ExchangeConfig.Declare == nil {
		return true
	}

	return *c.V2Config.ExchangeConfig.Declare
}

func (c *config) queueDeclareEnabled() bool {
	if c.Version != 2 {
		return true
	}

	if c.V2Config.QueueConfig.Declare == nil {
		return true
	}

	return *c.V2Config.QueueConfig.Declare
}

func (c *config) deleteQueueOnStopEnabled() bool {
	if c.Version == 2 {
		return c.V2Config.QueueConfig.DeleteOnStop
	}
	return c.V1Config.DeleteQueueOnStop
}

func (c *config) multipleAckEnabled() bool {
	if c.Version == 2 {
		return c.V2Config.QueueConfig.MultipleAck
	}
	return c.V1Config.MultipleAck
}

func (c *config) requeueOnFailEnabled() bool {
	if c.Version == 2 {
		return c.V2Config.QueueConfig.RequeueOnFail
	}
	return c.V1Config.RequeueOnFail
}

func (c *config) redialTimeoutSeconds() int {
	return c.RedialTimeout
}

func (c *config) consumerID() string {
	if c.Version == 2 {
		return c.V2Config.QueueConfig.ConsumerID
	}
	return c.V1Config.ConsumerID
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
