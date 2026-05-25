package helpers

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	jobsProto "github.com/roadrunner-server/api-go/v6/jobs/v2"
	"github.com/roadrunner-server/api-go/v6/jobs/v2/jobsV2connect"
	resetterProto "github.com/roadrunner-server/api-go/v6/resetter/v1"
	"github.com/roadrunner-server/api-go/v6/resetter/v1/resetterV1connect"
	jobState "github.com/roadrunner-server/api-plugins/v6/jobs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/types/known/emptypb"
)

func newHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	httpc := &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return new(net.Dialer).DialContext(ctx, network, addr)
		},
	}}
	t.Cleanup(httpc.CloseIdleConnections)
	return httpc
}

func NewJobsClient(t *testing.T, address string) jobsV2connect.JobsServiceClient {
	t.Helper()
	return jobsV2connect.NewJobsServiceClient(newHTTPClient(t), "http://"+address)
}

func NewResetterClient(t *testing.T, address string) resetterV1connect.ResetterServiceClient {
	t.Helper()
	return resetterV1connect.NewResetterServiceClient(newHTTPClient(t), "http://"+address)
}

func ResumePipes(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		_, err := client.Resume(t.Context(), connect.NewRequest(&jobsProto.Pipelines{Pipelines: slices.Clone(pipes)}))
		require.NoError(t, err)
	}
}

func ResumePipesErr(address, errContains string, pipes ...string) func(t *testing.T) {
	return callPipelinesErr(address, "Resume", errContains, pipes...)
}

func PushToPipe(pipeline string, autoAck bool, address string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		_, err := client.Push(t.Context(), connect.NewRequest(&jobsProto.PushRequest{Job: createDummyJob(pipeline, autoAck)}))
		require.NoError(t, err)
	}
}

func PushToPipeDelayed(address string, pipeline string, delay int64) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		req := &jobsProto.PushRequest{Job: &jobsProto.Job{
			Job:     "some/php/namespace",
			Id:      uuid.NewString(),
			Payload: []byte(`{"hello":"world"}`),
			Headers: map[string]*jobsProto.JobHeaderValue{"test": {Values: []string{"test2"}}},
			Options: &jobsProto.Options{
				Priority: 1,
				Pipeline: pipeline,
				Delay:    delay,
			},
		}}
		_, err := client.Push(t.Context(), connect.NewRequest(req))
		assert.NoError(t, err)
	}
}

func createDummyJob(pipeline string, autoAck bool) *jobsProto.Job {
	return &jobsProto.Job{
		Job:     "some/php/namespace",
		Id:      uuid.NewString(),
		Payload: []byte(`{"hello":"world"}`),
		Headers: map[string]*jobsProto.JobHeaderValue{"test": {Values: []string{"test2"}}},
		Options: &jobsProto.Options{
			AutoAck:  autoAck,
			Priority: 1,
			Pipeline: pipeline,
			Topic:    pipeline,
		},
	}
}

func PausePipelines(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		_, err := client.Pause(t.Context(), connect.NewRequest(&jobsProto.Pipelines{Pipelines: slices.Clone(pipes)}))
		assert.NoError(t, err)
	}
}

func PausePipelinesErr(address, errContains string, pipes ...string) func(t *testing.T) {
	return callPipelinesErr(address, "Pause", errContains, pipes...)
}

func callPipelinesErr(address, method, errContains string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		req := connect.NewRequest(&jobsProto.Pipelines{Pipelines: slices.Clone(pipes)})

		var err error
		switch method {
		case "Resume":
			_, err = client.Resume(t.Context(), req)
		case "Pause":
			_, err = client.Pause(t.Context(), req)
		default:
			t.Fatalf("callPipelinesErr: unsupported method %q", method)
		}
		require.Error(t, err)
		if errContains != "" {
			assert.Contains(t, err.Error(), errContains)
		}
	}
}

func DestroyPipelines(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		req := &jobsProto.Pipelines{Pipelines: slices.Clone(pipes)}

		// Retry the destroy 10× with 1s gaps; if all attempts fail, return
		// without asserting. Some negative tests intentionally destroy
		// non-existent pipelines and rely on this silent-after-retry pattern.
		for range 10 {
			_, err := client.Destroy(t.Context(), connect.NewRequest(req))
			if err == nil {
				return
			}
			time.Sleep(time.Second)
		}
	}
}

func PushToPipeErr(pipeline string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, "127.0.0.1:6001")
		req := &jobsProto.PushRequest{Job: &jobsProto.Job{
			Job:     "some/php/namespace",
			Id:      uuid.NewString(),
			Payload: []byte(`{"hello":"world"}`),
			Headers: map[string]*jobsProto.JobHeaderValue{"test": {Values: []string{"test2"}}},
			Options: &jobsProto.Options{
				Priority:  1,
				Pipeline:  pipeline,
				AutoAck:   true,
				Topic:     pipeline,
				Offset:    0,
				Partition: 0,
			},
		}}

		// Proxy disable and AMQP connection teardown are asynchronous.
		// Retry for a short period until push starts failing during redial.
		require.Eventually(t, func() bool {
			req.Job.Id = uuid.NewString()
			_, err := client.Push(t.Context(), connect.NewRequest(req))
			return err != nil
		}, 5*time.Second, 100*time.Millisecond)
	}
}

func Stats(address string, state *jobState.State) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)

		resp, err := client.GetStats(t.Context(), connect.NewRequest(&emptypb.Empty{}))
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotEmpty(t, resp.Msg.GetStats())

		st := resp.Msg.GetStats()[0]
		state.Queue = st.GetQueue()
		state.Pipeline = st.GetPipeline()
		state.Driver = st.GetDriver()
		state.Active = st.GetActive()
		state.Delayed = st.GetDelayed()
		state.Reserved = st.GetReserved()
		state.Ready = st.GetReady()
		state.Priority = st.GetPriority()
	}
}

func setProxy(name string, enabled bool, t *testing.T) {
	t.Helper()
	body := strings.NewReader(`{"enabled":` + boolStr(enabled) + `}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8474/proxies/"+name, body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func EnableProxy(name string, t *testing.T) {
	setProxy(name, true, t)
}

func DisableProxy(name string, t *testing.T) {
	setProxy(name, false, t)
}

func DeleteProxy(name string, t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, "http://127.0.0.1:8474/proxies/"+name, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, 204, resp.StatusCode)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func DeclareAMQPPipe(queue, routingKey, name, headers, exclusive, durable string) func(t *testing.T) {
	return DeclareAMQPPipeWithDeclare(queue, routingKey, name, headers, exclusive, durable, "", "")
}

func DeclareAMQPPipeWithDeclare(queue, routingKey, name, headers, exclusive, durable, exchangeDeclare, queueDeclare string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, "127.0.0.1:6001")
		req := &jobsProto.DeclareRequest{Pipeline: map[string]string{
			"driver":               "amqp",
			"name":                 name,
			"routing_key":          routingKey,
			"queue":                queue,
			"exchange_type":        "direct",
			"exchange":             "amqp.default",
			"prefetch":             "100",
			"delete_queue_on_stop": "true",
			"priority":             "3",
			"exclusive":            exclusive,
			"durable":              durable,
			"multiple_ack":         "true",
			"requeue_on_fail":      "true",
		}}

		if headers != "" {
			req.Pipeline["queue_headers"] = headers
		}

		if exchangeDeclare != "" {
			req.Pipeline["exchange_declare"] = exchangeDeclare
		}

		if queueDeclare != "" {
			req.Pipeline["queue_declare"] = queueDeclare
		}

		_, err := client.Declare(t.Context(), connect.NewRequest(req))
		assert.NoError(t, err)
	}
}

func Reset(t *testing.T) {
	client := NewResetterClient(t, "127.0.0.1:6001")
	resp, err := client.Reset(t.Context(), connect.NewRequest(&resetterProto.ResetRequest{Plugin: "jobs"}))
	assert.NoError(t, err)
	require.True(t, resp.Msg.GetOk())
}
