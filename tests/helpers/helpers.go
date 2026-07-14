package helpers

import (
	"net"
	"net/http"
	"net/rpc"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	jobsProto "github.com/roadrunner-server/api-go/v6/jobs/v2"
	resetterProto "github.com/roadrunner-server/api-go/v6/resetter/v1"
	jobState "github.com/roadrunner-server/api-plugins/v6/jobs"
	goridgeRpc "github.com/roadrunner-server/goridge/v4/pkg/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

func NewJobsClient(t *testing.T, address string) *rpc.Client {
	t.Helper()
	conn, err := new(net.Dialer).DialContext(t.Context(), "tcp", address)
	require.NoError(t, err)
	client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func NewResetterClient(t *testing.T, address string) *rpc.Client {
	t.Helper()
	return NewJobsClient(t, address)
}

func ResumePipes(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		err := client.Call("jobs.Resume", &jobsProto.Pipelines{Pipelines: slices.Clone(pipes)}, &jobsProto.JobsHandlerResponse{})
		require.NoError(t, err)
	}
}

func ResumePipesErr(address, errContains string, pipes ...string) func(t *testing.T) {
	return callPipelinesErr(address, "Resume", errContains, pipes...)
}

func PushToPipe(pipeline string, autoAck bool, address string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		err := client.Call("jobs.Push", &jobsProto.PushRequest{Job: createDummyJob(pipeline, autoAck)}, &jobsProto.JobsHandlerResponse{})
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
		err := client.Call("jobs.Push", req, &jobsProto.JobsHandlerResponse{})
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
		err := client.Call("jobs.Pause", &jobsProto.Pipelines{Pipelines: slices.Clone(pipes)}, &jobsProto.JobsHandlerResponse{})
		assert.NoError(t, err)
	}
}

func PausePipelinesErr(address, errContains string, pipes ...string) func(t *testing.T) {
	return callPipelinesErr(address, "Pause", errContains, pipes...)
}

func callPipelinesErr(address, method, errContains string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		req := &jobsProto.Pipelines{Pipelines: slices.Clone(pipes)}

		var err error
		switch method {
		case "Resume":
			err = client.Call("jobs.Resume", req, &jobsProto.JobsHandlerResponse{})
		case "Pause":
			err = client.Call("jobs.Pause", req, &jobsProto.JobsHandlerResponse{})
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
			err := client.Call("jobs.Destroy", req, &jobsProto.Pipelines{})
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
			err := client.Call("jobs.Push", req, &jobsProto.JobsHandlerResponse{})
			return err != nil
		}, 5*time.Second, 100*time.Millisecond)
	}
}

func Stats(address string, state *jobState.State) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)

		resp := &jobsProto.Stats{}
		err := client.Call("jobs.GetStats", &emptypb.Empty{}, resp)
		require.NoError(t, err)
		require.NotEmpty(t, resp.GetStats())

		st := resp.GetStats()[0]
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

		err := client.Call("jobs.Declare", req, &jobsProto.JobsHandlerResponse{})
		assert.NoError(t, err)
	}
}

func Reset(t *testing.T) {
	client := NewResetterClient(t, "127.0.0.1:6001")
	resp := &resetterProto.Response{}
	err := client.Call("resetter.Reset", &resetterProto.ResetRequest{Plugin: "jobs"}, resp)
	assert.NoError(t, err)
	require.True(t, resp.GetOk())
}
