package amqpjobs

import (
	"crypto/tls"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roadrunner-server/errors"
	"github.com/stretchr/testify/require"
)

func TestConfigInitDefaultV1(t *testing.T) {
	// direct exchange requires a routing key
	c := &config{
		Version:  1,
		V1Config: &v1config{RoutingKey: "test-rk"},
	}
	require.NoError(t, c.InitDefault())

	// global defaults
	require.Equal(t, "amqp://guest:guest@127.0.0.1:5672/", c.Addr)
	require.Equal(t, 10, c.Prefetch)
	require.Equal(t, int64(10), c.Priority)
	require.Equal(t, 60, c.RedialTimeout)

	// v1 defaults
	require.Equal(t, "direct", c.V1Config.ExchangeType)
	require.Equal(t, "amqp.default", c.V1Config.Exchange)
	require.True(t, strings.HasPrefix(c.V1Config.ConsumerID, "roadrunner-"),
		"consumer id should default to a roadrunner-prefixed UUID, got %q", c.V1Config.ConsumerID)
}

func TestConfigInitDefaultV1Fanout(t *testing.T) {
	// fanout does not require a routing key
	c := &config{
		Version:  1,
		V1Config: &v1config{ExchangeType: "fanout"},
	}
	require.NoError(t, c.InitDefault())
	require.Equal(t, "fanout", c.V1Config.ExchangeType)
}

func TestConfigInitDefaultV2(t *testing.T) {
	c := &config{
		Version: 2,
		V2Config: &v2config{
			QueueConfig: &queueConfigV2{RoutingKey: "test-rk"},
		},
	}
	require.NoError(t, c.InitDefault())

	require.Equal(t, "direct", c.V2Config.ExchangeConfig.Type)
	require.Equal(t, "amqp.default", c.V2Config.ExchangeConfig.Name)
	require.True(t, strings.HasPrefix(c.V2Config.QueueConfig.ConsumerID, "roadrunner-"),
		"consumer id should default to a roadrunner-prefixed UUID, got %q", c.V2Config.QueueConfig.ConsumerID)
	// consumerID() must read the v2 path
	require.Equal(t, c.V2Config.QueueConfig.ConsumerID, c.consumerID())
}

func TestConfigInitDefaultErrors(t *testing.T) {
	t.Run("unsupported version", func(t *testing.T) {
		err := (&config{Version: 99}).InitDefault()
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported AMQP pipeline config version")
	})

	t.Run("missing routing key for non-fanout exchange", func(t *testing.T) {
		err := (&config{Version: 1, V1Config: &v1config{ExchangeType: "direct"}}).InitDefault()
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty routing key")
	})

	t.Run("v2 without exchange or queue", func(t *testing.T) {
		err := (&config{Version: 2}).InitDefault()
		require.Error(t, err)
		require.Contains(t, err.Error(), "exchange or queue configuration is required")
	})
}

func TestConfigValidateTLS(t *testing.T) {
	certDir := filepath.Join("..", "tests", "test-certs")
	realKey := filepath.Join(certDir, "localhost+2-client-key.pem")
	realCert := filepath.Join(certDir, "localhost+2-client.pem")
	realCA := filepath.Join(certDir, "rootCA.pem")
	op := errors.Op("test_validate_tls")

	t.Run("missing key file", func(t *testing.T) {
		err := (&config{TLS: &TLS{Key: "/does/not/exist/key.pem", Cert: realCert}}).validateTLS(op)
		require.Error(t, err)
		require.Contains(t, err.Error(), "key file")
		require.Contains(t, err.Error(), "does not exist")
	})

	t.Run("missing cert file", func(t *testing.T) {
		err := (&config{TLS: &TLS{Key: realKey, Cert: "/does/not/exist/cert.pem"}}).validateTLS(op)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cert file")
		require.Contains(t, err.Error(), "does not exist")
	})

	t.Run("missing root ca file", func(t *testing.T) {
		err := (&config{TLS: &TLS{Key: realKey, Cert: realCert, RootCA: "/does/not/exist/ca.pem"}}).validateTLS(op)
		require.Error(t, err)
		require.Contains(t, err.Error(), "root ca")
		require.Contains(t, err.Error(), "does not exist")
	})

	t.Run("valid files map the auth type", func(t *testing.T) {
		c := &config{TLS: &TLS{
			Key:      realKey,
			Cert:     realCert,
			RootCA:   realCA,
			AuthType: RequireAndVerifyClientCert,
		}}
		require.NoError(t, c.validateTLS(op))
		require.Equal(t, tls.RequireAndVerifyClientCert, c.TLS.auth)
	})
}

func TestConfigConsumerID(t *testing.T) {
	t.Run("v1", func(t *testing.T) {
		c := &config{Version: 1, V1Config: &v1config{ConsumerID: "consumer-v1"}}
		require.Equal(t, "consumer-v1", c.consumerID())
	})

	t.Run("v2", func(t *testing.T) {
		c := &config{Version: 2, V2Config: &v2config{QueueConfig: &queueConfigV2{ConsumerID: "consumer-v2"}}}
		require.Equal(t, "consumer-v2", c.consumerID())
	})
}
