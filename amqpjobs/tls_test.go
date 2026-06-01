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

func TestInitTLSNoRootCA(t *testing.T) {
	cfg := &TLS{
		Key:  filepath.Join("..", "tests", "test-certs", "localhost+2-client-key.pem"),
		Cert: filepath.Join("..", "tests", "test-certs", "localhost+2-client.pem"),
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}

	require.NoError(t, initTLS(cfg, tlsCfg))
	require.Len(t, tlsCfg.Certificates, 1)
}

func TestInitTLSBadKeyPair(t *testing.T) {
	cfg := &TLS{
		Key:  "/does/not/exist/key.pem",
		Cert: "/does/not/exist/cert.pem",
	}
	require.Error(t, initTLS(cfg, &tls.Config{MinVersion: tls.VersionTLS12}))
}
