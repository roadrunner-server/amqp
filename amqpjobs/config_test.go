package amqpjobs

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roadrunner-server/errors"
	"github.com/stretchr/testify/require"
)

func TestConfigInitDefault(t *testing.T) {
	c := &config{
		QueueConfig: &queueConfig{RoutingKey: "test-rk"},
	}
	require.NoError(t, c.InitDefault())

	require.Equal(t, 10, c.Prefetch)
	require.Equal(t, int64(10), c.Priority)
	require.Equal(t, 60, c.RedialTimeout)

	require.Equal(t, "direct", c.ExchangeConfig.Type)
	require.Equal(t, "amqp.default", c.ExchangeConfig.Name)
	require.True(t, strings.HasPrefix(c.QueueConfig.ConsumerID, "roadrunner-"),
		"expected a consumer ID with the roadrunner- prefix, got %q", c.QueueConfig.ConsumerID)
}

func TestConfigInitDefaultFanout(t *testing.T) {
	c := &config{
		ExchangeConfig: &exchangeConfig{Type: "fanout"},
	}
	require.NoError(t, c.InitDefault())
	require.Equal(t, "fanout", c.ExchangeConfig.Type)
	require.NotNil(t, c.QueueConfig)
}

func TestConfigInitDefaultErrors(t *testing.T) {
	t.Run("missing routing key for non-fanout exchange", func(t *testing.T) {
		err := (&config{ExchangeConfig: &exchangeConfig{Type: "direct"}}).InitDefault()
		require.ErrorContains(t, err, "empty routing key")
	})

	t.Run("missing exchange and queue", func(t *testing.T) {
		err := (&config{}).InitDefault()
		require.ErrorContains(t, err, "exchange or queue configuration is required")
	})

	t.Run("invalid exchange type", func(t *testing.T) {
		err := (&config{
			ExchangeConfig: &exchangeConfig{Type: "invalid"},
			QueueConfig:    &queueConfig{RoutingKey: "test-rk"},
		}).InitDefault()
		require.ErrorContains(t, err, `invalid exchange type "invalid"`)
	})
}

func TestConfigValidateTLS(t *testing.T) {
	// validateTLS only checks the files exist; their content is never parsed
	certDir := t.TempDir()
	realKey := filepath.Join(certDir, "client-key.pem")
	realCert := filepath.Join(certDir, "client.pem")
	realCA := filepath.Join(certDir, "rootCA.pem")
	for _, f := range []string{realKey, realCert, realCA} {
		require.NoError(t, os.WriteFile(f, []byte("pem"), 0o600))
	}
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
