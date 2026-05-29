package amqpjobs

import (
	"crypto/tls"
	"crypto/x509"
	"os"

	"github.com/roadrunner-server/errors"
)

func initTLS(config *TLS, tlsCfg *tls.Config) error {
	const op = errors.Op("amqp_init_tls")

	tlsCfg.ClientAuth = config.auth
	cert, err := tls.LoadX509KeyPair(config.Cert, config.Key)
	if err != nil {
		return err
	}

	certPool, err := x509.SystemCertPool()
	if err != nil {
		return err
	}
	if certPool == nil {
		certPool = x509.NewCertPool()
	}

	if config.RootCA != "" {
		rca, err := os.ReadFile(config.RootCA)
		if err != nil {
			return err
		}

		if ok := certPool.AppendCertsFromPEM(rca); !ok {
			return errors.E(op, errors.Str("could not append Certs from PEM"))
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
		tlsCfg.RootCAs = certPool

		return nil
	}

	tlsCfg.Certificates = []tls.Certificate{cert}

	return nil
}
