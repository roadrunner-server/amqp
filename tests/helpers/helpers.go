package helpers

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	jobsProto "github.com/roadrunner-server/api-go/v6/jobs/v1"
	jobState "github.com/roadrunner-server/api-plugins/v6/jobs"
	goridgeRpc "github.com/roadrunner-server/goridge/v4/pkg/rpc"
	"github.com/stretchr/testify/require"
)

const (
	// toxiproxyAddr is the toxiproxy api used by the durability tests.
	toxiproxyAddr = "127.0.0.1:8474"
	// redialTimeout bounds PushEventually, which retries across a broker outage.
	redialTimeout = time.Second * 120
	redialTick    = time.Second
)

func NewJobsClient(t *testing.T, address string) *rpc.Client {
	t.Helper()

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", address)
	require.NoError(t, err)

	client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))
	t.Cleanup(func() { _ = client.Close() })

	return client
}

func ResumePipes(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		require.NoError(t, client.Call("jobs.Resume",
			&jobsProto.Pipelines{Pipelines: slices.Clone(pipes)},
			&jobsProto.Empty{}))
	}
}

// ResumePipesErr requires the resume call to fail with the given message.
func ResumePipesErr(address, errContains string, pipes ...string) func(t *testing.T) {
	return callPipelinesErr(address, "jobs.Resume", errContains, pipes...)
}

func PausePipelines(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		require.NoError(t, client.Call("jobs.Pause",
			&jobsProto.Pipelines{Pipelines: slices.Clone(pipes)},
			&jobsProto.Empty{}))
	}
}

// PausePipelinesErr requires the pause call to fail with the given message.
func PausePipelinesErr(address, errContains string, pipes ...string) func(t *testing.T) {
	return callPipelinesErr(address, "jobs.Pause", errContains, pipes...)
}

func callPipelinesErr(address, method, errContains string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		err := client.Call(method,
			&jobsProto.Pipelines{Pipelines: slices.Clone(pipes)},
			&jobsProto.Empty{})

		require.ErrorContains(t, err, errContains)
	}
}

func DestroyPipelines(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		require.NoError(t, client.Call("jobs.Destroy",
			&jobsProto.Pipelines{Pipelines: slices.Clone(pipes)},
			&jobsProto.Pipelines{}))
	}
}

func PushToPipe(pipeline string, autoAck bool, address string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		require.NoError(t, client.Call("jobs.Push",
			&jobsProto.PushRequest{Job: dummyJob(pipeline, autoAck, 0)},
			&jobsProto.Empty{}))
	}
}

func PushToPipeDelayed(address string, pipeline string, delay int64) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		require.NoError(t, client.Call("jobs.Push",
			&jobsProto.PushRequest{Job: dummyJob(pipeline, false, delay)},
			&jobsProto.Empty{}))
	}
}

// PushExpectError pushes to a pipeline whose broker is down and requires the
// call to fail, so an outage is not silently swallowed.
func PushExpectError(address string, pipeline string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		require.Error(t, client.Call("jobs.Push",
			&jobsProto.PushRequest{Job: dummyJob(pipeline, false, 0)},
			&jobsProto.Empty{}))
	}
}

// PushEventually keeps retrying a push until it lands. Used after a broker
// outage, where the redialer needs a while to get through again.
func PushEventually(t *testing.T, address string, pipeline string) {
	t.Helper()

	require.Eventually(t, func() bool {
		client := NewJobsClient(t, address)

		return client.Call("jobs.Push",
			&jobsProto.PushRequest{Job: dummyJob(pipeline, false, 0)},
			&jobsProto.Empty{}) == nil
	}, redialTimeout, redialTick, "the driver never recovered after the outage")
}

func dummyJob(pipeline string, autoAck bool, delay int64) *jobsProto.Job {
	return &jobsProto.Job{
		Job:     "some/php/namespace",
		Id:      uuid.NewString(),
		Payload: []byte(`{"hello":"world"}`),
		Headers: map[string]*jobsProto.HeaderValue{"test": {Value: []string{"test2"}}},
		Options: &jobsProto.Options{
			AutoAck:  autoAck,
			Priority: 1,
			Pipeline: pipeline,
			Delay:    delay,
		},
	}
}

// DeclarePipe declares a pipeline over rpc bound to its own queue and routing
// key, so a test never inherits messages from another.
func DeclarePipe(address string, name string, opts map[string]string) func(t *testing.T) {
	return func(t *testing.T) {
		pipeline := map[string]string{
			"driver":               "amqp",
			"name":                 name,
			"routing_key":          name,
			"queue":                name,
			"exchange_type":        "direct",
			"exchange":             "amqp.default",
			"prefetch":             "100",
			"delete_queue_on_stop": "true",
			"priority":             "3",
			"exclusive":            "true",
			"durable":              "false",
			"multiple_ack":         "true",
			"requeue_on_fail":      "true",
		}
		for k, v := range opts {
			pipeline[k] = v
		}

		require.NoError(t, NewJobsClient(t, address).Call("jobs.Declare",
			&jobsProto.DeclareRequest{Pipeline: pipeline},
			&jobsProto.Empty{}))
	}
}

// StatsFor returns the state the jobs plugin reports for one pipeline. Picking
// it by name keeps the assertion stable when several are registered.
func StatsFor(t *testing.T, address string, pipeline string) *jobState.State {
	t.Helper()

	resp := &jobsProto.Stats{}
	require.NoError(t, NewJobsClient(t, address).Call("jobs.Stat", &jobsProto.Empty{}, resp))

	for _, st := range resp.GetStats() {
		if st.GetPipeline() != pipeline {
			continue
		}

		return &jobState.State{
			Queue:    st.GetQueue(),
			Pipeline: st.GetPipeline(),
			Driver:   st.GetDriver(),
			Active:   st.GetActive(),
			Delayed:  st.GetDelayed(),
			Reserved: st.GetReserved(),
			Ready:    st.GetReady(),
			Priority: st.GetPriority(),
		}
	}

	require.FailNowf(t, "pipeline not reported", "no stats for %q", pipeline)

	return nil
}

// Reset restarts the jobs plugin workers through the resetter.
func Reset(t *testing.T, address string) {
	t.Helper()

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", address)
	require.NoError(t, err)

	client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))
	t.Cleanup(func() { _ = client.Close() })

	var done bool
	require.NoError(t, client.Call("resetter.Reset", "jobs", &done))
	require.True(t, done)
}

// CreateProxy fronts rabbitmq with a toxiproxy the durability tests can cut.
// Both addresses are resolved inside the compose network, not on the host.
func CreateProxy(t *testing.T, name string, listen string, upstream string) {
	t.Helper()

	// a proxy left behind by an interrupted run would make the create conflict
	deleteProxy(t, name)

	body := fmt.Sprintf(`{"name":%q,"listen":%q,"upstream":%q,"enabled":true}`, name, listen, upstream)
	post(t, "http://"+toxiproxyAddr+"/proxies", []byte(body), http.StatusCreated)
	t.Cleanup(func() { deleteProxy(t, name) })
}

// SetProxyEnabled cuts or restores the connection to rabbitmq.
func SetProxyEnabled(t *testing.T, name string, enabled bool) {
	t.Helper()

	post(t, "http://"+toxiproxyAddr+"/proxies/"+name, fmt.Appendf(nil, `{"enabled":%t}`, enabled), http.StatusOK)
}

func deleteProxy(t *testing.T, name string) {
	t.Helper()

	// runs from t.Cleanup, where the test context is already canceled
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, "http://"+toxiproxyAddr+"/proxies/"+name, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Contains(t, []int{http.StatusNoContent, http.StatusNotFound}, resp.StatusCode)
}

func post(t *testing.T, addr string, body []byte, wantStatus int) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, addr, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, wantStatus, resp.StatusCode, "POST %s", addr)
}
