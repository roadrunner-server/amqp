package tests

import (
	"testing"
)

const tlsAddr = "127.0.0.1:6111"

// TestTLS checks mutual TLS with a configured client certificate, key, and root CA. See https://www.rabbitmq.com/docs/3.13/ssl#peer-verification.
func TestTLS(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-init-tls.yaml", tlsAddr)

	rr.RequireLogCount(t, "pipeline was started", 2)

	pushAndDrain(t, rr, tlsAddr, "test-1", "test-2")
}
