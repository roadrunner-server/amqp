package amqpjobs

import (
	"crypto/tls"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitTLSUsesRootCAs(t *testing.T) {
	cfg := &TLS{
		RootCA: filepath.Join("..", "tests", "test-certs", "rootCA.pem"),
		Key:    filepath.Join("..", "tests", "test-certs", "localhost+2-client-key.pem"),
		Cert:   filepath.Join("..", "tests", "test-certs", "localhost+2-client.pem"),
		auth:   tls.RequireAndVerifyClientCert,
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	err := initTLS(cfg, tlsCfg)
	require.NoError(t, err)
	require.Len(t, tlsCfg.Certificates, 1)
	require.NotNil(t, tlsCfg.RootCAs)
	assert.Nil(t, tlsCfg.ClientCAs)
}
