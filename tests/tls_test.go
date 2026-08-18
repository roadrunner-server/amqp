package tests

import (
	"testing"
)

const tlsAddr = "127.0.0.1:6111"

// TestTLS covers mutual TLS against the broker's SSL listener, with the client
// key pair and the root CA configured explicitly.
func TestTLS(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-init-tls.yaml", tlsAddr)

	rr.RequireLogCount(t, "pipeline was started", 2)

	pushAndDrain(t, rr, tlsAddr, "test-1", "test-2")
}
