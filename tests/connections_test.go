package tests

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"tests/helpers"

	amqp "github.com/rabbitmq/amqp091-go"
	apiJobs "github.com/roadrunner-server/api-plugins/v6/jobs"
	"github.com/stretchr/testify/require"
)

// TestNamedConnectionsPHPQueueHeaders forwards a task between two brokers through the PHP client.
func TestNamedConnectionsPHPQueueHeaders(t *testing.T) {
	const (
		rpcAddr    = "127.0.0.1:6002"
		exchange   = "amqp-connections-exchange"
		queue      = "amqp-connections-queue"
		routingKey = "amqp-connections-route"
		payload    = `{"message":"connections-round-trip"}`
	)

	// Both brokers use the same names to expose connection selection errors.
	var channels [2]*amqp.Channel
	for i, addr := range []string{
		"amqp://guest:guest@127.0.0.1:5673/",
		"amqp://guest:guest@127.0.0.1:5672/",
	} {
		conn, err := amqp.DialConfig(addr, amqp.Config{Dial: amqp.DefaultDial(5 * time.Second)})
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })

		ch, err := conn.Channel()
		require.NoError(t, err)
		channels[i] = ch

		t.Cleanup(func() {
			_, err := ch.QueueDelete(queue, false, false, false)
			require.NoError(t, err)
			require.NoError(t, ch.ExchangeDelete(exchange, false, false))
		})

		_, err = ch.QueueDelete(queue, false, false, false)
		require.NoError(t, err)
		require.NoError(t, ch.ExchangeDelete(exchange, false, false))
	}
	source, destination := channels[0], channels[1]

	rr, _ := boot(t, "configs/.rr-amqp-connections.yaml", rpcAddr)

	require.NoError(t, source.PublishWithContext(t.Context(), exchange, routingKey, false, false, amqp.Publishing{
		Headers: amqp.Table{
			apiJobs.RRID:       "connections-source-task",
			apiJobs.RRJob:      "connections.source",
			apiJobs.RRPipeline: "connections-source",
			apiJobs.RRHeaders:  []byte(`{"test":["connections-round-trip"]}`),
		},
		Body: []byte(payload),
	}))

	rr.WaitLog(t, "job was processed successfully", 1)

	var message amqp.Delivery
	var received bool
	var getErr error
	require.Eventually(t, func() bool {
		message, received, getErr = destination.Get(queue, true)
		return getErr != nil || received
	}, 30*time.Second, 50*time.Millisecond, "brokerB did not receive the forwarded task")
	require.NoError(t, getErr)
	require.True(t, received)
	require.Equal(t, payload, string(message.Body))
	require.Equal(t, exchange, message.Exchange)
	require.Equal(t, routingKey, message.RoutingKey)
	require.Equal(t, "connections.forward", message.Headers[apiJobs.RRJob])
	require.Equal(t, "connections-destination", message.Headers[apiJobs.RRPipeline])

	encodedHeaders, ok := message.Headers[apiJobs.RRHeaders].([]byte)
	require.True(t, ok, "the forwarded task must have encoded job headers")
	var headers map[string][]string
	require.NoError(t, json.Unmarshal(encodedHeaders, &headers))
	require.Equal(t, []string{"connections-round-trip"}, headers["test"])

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"http://127.0.0.1:15672/api/queues/%2F/"+queue, nil)
	require.NoError(t, err)
	req.SetBasicAuth("guest", "guest")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var declared struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&declared))
	require.Equal(t, queue, declared.Name)
	require.Equal(t, "lazy", declared.Arguments["x-queue-mode"])
	require.NotContains(t, declared.Arguments, "rr_connection")

	helpers.DestroyPipelines(rpcAddr, "connections-source", "connections-destination")(t)
}
