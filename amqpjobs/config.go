package amqpjobs

import (
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"io/fs"
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
	connectionKey string = "connection"
	exchangeKey   string = "exchange"
	exchangeType  string = "exchange_type"
	queue         string = "queue"
	routingKey    string = "routing_key"
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

type exchangeConfig struct {
	Name       string `mapstructure:"name"`
	Type       string `mapstructure:"type"`
	Durable    bool   `mapstructure:"durable"`
	AutoDelete bool   `mapstructure:"auto_delete"`
	Declare    *bool  `mapstructure:"declare"`
}

type queueConfig struct {
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

type connectionConfig struct {
	Addr string `mapstructure:"addr"`
	TLS  *TLS   `mapstructure:"tls"`
}

// config is the canonical static YAML model.
type config struct {
	// Resolved from the named connection.
	Addr string `mapstructure:"-"`
	TLS  *TLS   `mapstructure:"-"`

	// local/common
	Connection    string `mapstructure:"connection"`
	Prefetch      int    `mapstructure:"prefetch"`
	Priority      int64  `mapstructure:"priority"`
	RedialTimeout int    `mapstructure:"redial_timeout"`

	ExchangeConfig *exchangeConfig `mapstructure:"exchange"`
	QueueConfig    *queueConfig    `mapstructure:"queue"`
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

func (c *config) loadConnection(cfg Configurer) error {
	const op = errors.Op("amqp_load_connection")

	if c.Connection == "" {
		return errors.E(op, errors.Str("connection is required"))
	}

	key := pluginName + "." + c.Connection
	if !cfg.Has(key) {
		return errors.E(op, errors.Errorf("unknown AMQP connection %q", c.Connection))
	}

	var conn connectionConfig
	if err := cfg.UnmarshalKey(key, &conn); err != nil {
		return errors.E(op, fmt.Errorf("connection %q: %w", c.Connection, err))
	}
	if conn.Addr == "" {
		return errors.E(op, errors.Errorf("addr is required for AMQP connection %q", c.Connection))
	}

	c.Addr = conn.Addr
	c.TLS = conn.TLS
	return nil
}

func (c *config) InitDefault() error {
	const op = errors.Op("amqp_config_init_default")

	if c.Prefetch <= 0 {
		c.Prefetch = 10
	}

	if c.Priority <= 0 {
		c.Priority = 10
	}

	if c.RedialTimeout <= 0 {
		c.RedialTimeout = 60
	}

	if c.ExchangeConfig == nil && c.QueueConfig == nil {
		return errors.E(op, errors.Str("exchange or queue configuration is required"))
	}

	if c.ExchangeConfig == nil {
		c.ExchangeConfig = &exchangeConfig{}
	}

	if c.QueueConfig == nil {
		c.QueueConfig = &queueConfig{}
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

	if c.QueueConfig.ConsumerID == "" {
		c.QueueConfig.ConsumerID = "roadrunner-" + uuid.NewString()
	}

	if c.enableTLS() {
		if err := c.validateTLS(op); err != nil {
			return err
		}
	}

	if c.QueueConfig.RoutingKey == "" && c.ExchangeConfig.Type != "fanout" {
		return errors.E(op, errors.Str("empty routing key, consider adding the routing key name to the AMQP configuration"))
	}

	return nil
}

func (c *config) exchangeDeclareEnabled() bool {
	if c.ExchangeConfig.Declare == nil {
		return true
	}

	return *c.ExchangeConfig.Declare
}

func (c *config) queueDeclareEnabled() bool {
	if c.QueueConfig.Declare == nil {
		return true
	}

	return *c.QueueConfig.Declare
}

func (c *config) validateTLS(op errors.Op) error {
	if _, err := os.Stat(c.TLS.Key); err != nil {
		if stderrors.Is(err, fs.ErrNotExist) {
			return errors.E(op, errors.Errorf("key file '%s' does not exist", c.TLS.Key))
		}

		return errors.E(op, err)
	}

	if _, err := os.Stat(c.TLS.Cert); err != nil {
		if stderrors.Is(err, fs.ErrNotExist) {
			return errors.E(op, errors.Errorf("cert file '%s' does not exist", c.TLS.Cert))
		}

		return errors.E(op, err)
	}

	// RootCA is optional, but if provided - check it
	if c.TLS.RootCA != "" {
		if _, err := os.Stat(c.TLS.RootCA); err != nil {
			if stderrors.Is(err, fs.ErrNotExist) {
				return errors.E(op, errors.Errorf("root ca path provided, but root ca file '%s' does not exist", c.TLS.RootCA))
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
