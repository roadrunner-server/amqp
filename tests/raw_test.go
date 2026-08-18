package tests

import (
	"testing"
	"time"

	"tests/helpers"

	amqp "github.com/rabbitmq/amqp091-go"
	apiJobs "github.com/roadrunner-server/api-plugins/v6/jobs"
	"github.com/stretchr/testify/require"
)

// publishRaw puts a message on the exchange without going through RoadRunner.
func publishRaw(t *testing.T, exchange, routingKey string, pub amqp.Publishing) {
	t.Helper()

	conn, err := amqp.Dial("amqp://guest:guest@127.0.0.1:5672/")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ch, err := conn.Channel()
	require.NoError(t, err)

	require.NoError(t, ch.PublishWithContext(t.Context(), exchange, routingKey, false, false, pub))
}

// TestRawPayload covers messages published by something other than RoadRunner:
// one with no RR headers at all, one with values RoadRunner would never write.
// The listener has to synthesize what is missing and keep going.
func TestRawPayload(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-raw.yaml", initAddr)

	rr.WaitLog(t, "pipeline was started", 1)

	publishRaw(t, "default", "test-raw", amqp.Publishing{
		Headers:   amqp.Table{"foo": 2.3},
		Timestamp: time.Now(),
		Body:      []byte("foooobarrrrrrrrbazzzzzzzzzzzzzzzzzzzzzzzzz"),
	})
	publishRaw(t, "default", "test-raw", amqp.Publishing{
		Headers: amqp.Table{
			apiJobs.RRHeaders:  []byte(`{"broken-json"`),
			apiJobs.RRDelay:    "wrong-delay-type",
			apiJobs.RRPriority: true,
		},
		Timestamp: time.Now(),
		Body:      []byte("foooobarrrrrrrrbazzzzzzzzzzzzzzzzzzzzzzzzz"),
	})

	rr.WaitLog(t, "job was processed successfully", 2)

	helpers.DestroyPipelines(initAddr, "test-raw")(t)

	rr.RequireLogCount(t, "job processing was started", 2)
	rr.RequireLogCount(t, "missing header rr_id, generating new one", 2)
	rr.RequireLogCount(t, "missing header rr_job, using the standard one", 2)
	rr.RequireLogCount(t, "failed to unmarshal headers (should be JSON), continuing execution", 1)
	rr.RequireLogCount(t, "unknown delay type", 1)
	rr.RequireLogCount(t, "unknown priority type", 1)
}
