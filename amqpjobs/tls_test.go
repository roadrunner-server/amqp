package amqpjobs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTestCerts generates a throwaway self signed certificate usable both as
// the client pair and as a root CA, so the tests do not depend on files
// generated outside the module.
func writeTestCerts(t *testing.T) (keyFile, certFile, caFile string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "amqp-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	keyFile = filepath.Join(dir, "key.pem")
	certFile = filepath.Join(dir, "cert.pem")
	caFile = filepath.Join(dir, "ca.pem")

	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(caFile, certPEM, 0o600))

	return keyFile, certFile, caFile
}

func TestInitTLSUsesRootCAs(t *testing.T) {
	keyFile, certFile, caFile := writeTestCerts(t)

	cfg := &TLS{
		RootCA: caFile,
		Key:    keyFile,
		Cert:   certFile,
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
	keyFile, certFile, _ := writeTestCerts(t)

	cfg := &TLS{
		Key:  keyFile,
		Cert: certFile,
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
