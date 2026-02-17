package helpers

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/rpc"
	"testing"
	"time"

	"github.com/google/uuid"
	jobsProto "github.com/roadrunner-server/api/v4/build/jobs/v1"
	jobState "github.com/roadrunner-server/api/v4/plugins/v1/jobs"
	goridgeRpc "github.com/roadrunner-server/goridge/v3/pkg/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	push    string = "jobs.Push"
	pause   string = "jobs.Pause"
	destroy string = "jobs.Destroy"
	resume  string = "jobs.Resume"
	stat    string = "jobs.Stat"
)

func ResumePipes(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		var dialer net.Dialer
		conn, err := dialer.DialContext(context.Background(), "tcp", address)
		require.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		pipe := &jobsProto.Pipelines{Pipelines: make([]string, len(pipes))}

		for i := range pipes {
			pipe.GetPipelines()[i] = pipes[i]
		}

		er := &jobsProto.Empty{}
		err = client.Call(resume, pipe, er)
		require.NoError(t, err)
	}
}

func ResumePipesErr(address, errContains string, pipes ...string) func(t *testing.T) {
	return callPipelinesErr(address, resume, errContains, pipes...)
}

func PushToPipe(pipeline string, autoAck bool, address string) func(t *testing.T) {
	return func(t *testing.T) {
		var dialer net.Dialer
		conn, err := dialer.DialContext(context.Background(), "tcp", address)
		require.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		req := &jobsProto.PushRequest{Job: createDummyJob(pipeline, autoAck)}

		er := &jobsProto.Empty{}
		err = client.Call(push, req, er)
		require.NoError(t, err)
	}
}

func PushToPipeDelayed(address string, pipeline string, delay int64) func(t *testing.T) {
	return func(t *testing.T) {
		var dialer net.Dialer
		conn, err := dialer.DialContext(context.Background(), "tcp", address)
		assert.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		req := &jobsProto.PushRequest{Job: &jobsProto.Job{
			Job:     "some/php/namespace",
			Id:      uuid.NewString(),
			Payload: []byte(`{"hello":"world"}`),
			Headers: map[string]*jobsProto.HeaderValue{"test": {Value: []string{"test2"}}},
			Options: &jobsProto.Options{
				Priority: 1,
				Pipeline: pipeline,
				Delay:    delay,
			},
		}}

		er := &jobsProto.Empty{}
		err = client.Call(push, req, er)
		assert.NoError(t, err)
	}
}

func createDummyJob(pipeline string, autoAck bool) *jobsProto.Job {
	return &jobsProto.Job{
		Job:     "some/php/namespace",
		Id:      uuid.NewString(),
		Payload: []byte(`{"hello":"world"}`),
		Headers: map[string]*jobsProto.HeaderValue{"test": {Value: []string{"test2"}}},
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
		var dialer net.Dialer
		conn, err := dialer.DialContext(context.Background(), "tcp", address)
		assert.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		pipe := &jobsProto.Pipelines{Pipelines: make([]string, len(pipes))}

		for i := range pipes {
			pipe.GetPipelines()[i] = pipes[i]
		}

		er := &jobsProto.Empty{}
		err = client.Call(pause, pipe, er)
		assert.NoError(t, err)
	}
}

func PausePipelinesErr(address, errContains string, pipes ...string) func(t *testing.T) {
	return callPipelinesErr(address, pause, errContains, pipes...)
}

func callPipelinesErr(address, method, errContains string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		var dialer net.Dialer
		conn, err := dialer.DialContext(context.Background(), "tcp", address)
		require.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		pipe := &jobsProto.Pipelines{Pipelines: make([]string, len(pipes))}
		for i := range pipes {
			pipe.GetPipelines()[i] = pipes[i]
		}

		er := &jobsProto.Empty{}
		err = client.Call(method, pipe, er)
		require.Error(t, err)
		if errContains != "" {
			assert.Contains(t, err.Error(), errContains)
		}
	}
}

func DestroyPipelines(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		var dialer net.Dialer
		conn, err := dialer.DialContext(context.Background(), "tcp", address)
		assert.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		pipe := &jobsProto.Pipelines{Pipelines: make([]string, len(pipes))}

		for i := range pipes {
			pipe.GetPipelines()[i] = pipes[i]
		}

		for range 10 {
			er := &jobsProto.Empty{}
			err = client.Call(destroy, pipe, er)
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			assert.NoError(t, err)
			break
		}
	}
}

func PushToPipeErr(pipeline string) func(t *testing.T) {
	return func(t *testing.T) {
		var dialer net.Dialer
		conn, err := dialer.DialContext(context.Background(), "tcp", "127.0.0.1:6001")
		require.NoError(t, err)
		defer func() {
			_ = conn.Close()
		}()
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		req := &jobsProto.PushRequest{Job: &jobsProto.Job{
			Job:     "some/php/namespace",
			Id:      uuid.NewString(),
			Payload: []byte(`{"hello":"world"}`),
			Headers: map[string]*jobsProto.HeaderValue{"test": {Value: []string{"test2"}}},
			Options: &jobsProto.Options{
				Priority:  1,
				Pipeline:  pipeline,
				AutoAck:   true,
				Topic:     pipeline,
				Offset:    0,
				Partition: 0,
			},
		}}

		er := &jobsProto.Empty{}

		// Proxy disable and AMQP connection teardown are asynchronous.
		// Retry for a short period until push starts failing during redial.
		require.Eventually(t, func() bool {
			req.Job.Id = uuid.NewString()
			err = client.Call(push, req, er)
			return err != nil
		}, 5*time.Second, 100*time.Millisecond)
	}
}

func Stats(address string, state *jobState.State) func(t *testing.T) {
	return func(t *testing.T) {
		var dialer net.Dialer
		conn, err := dialer.DialContext(context.Background(), "tcp", address)
		require.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		st := &jobsProto.Stats{}
		er := &jobsProto.Empty{}

		err = client.Call(stat, er, st)
		require.NoError(t, err)
		require.NotNil(t, st)

		state.Queue = st.Stats[0].Queue
		state.Pipeline = st.Stats[0].Pipeline
		state.Driver = st.Stats[0].Driver
		state.Active = st.Stats[0].Active
		state.Delayed = st.Stats[0].Delayed
		state.Reserved = st.Stats[0].Reserved
		state.Ready = st.Stats[0].Ready
		state.Priority = st.Stats[0].Priority
	}
}

func EnableProxy(name string, t *testing.T) {
	buf := new(bytes.Buffer)
	buf.WriteString(`{"enabled":true}`)

	resp, err := http.Post("http://127.0.0.1:8474/proxies/"+name, "application/json", buf) //nolint:noctx
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func DisableProxy(name string, t *testing.T) {
	buf := new(bytes.Buffer)
	buf.WriteString(`{"enabled":false}`)

	resp, err := http.Post("http://127.0.0.1:8474/proxies/"+name, "application/json", buf) //nolint:noctx
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func DeleteProxy(name string, t *testing.T) {
	client := &http.Client{}

	req, err := http.NewRequest(http.MethodDelete, "http://127.0.0.1:8474/proxies/"+name, nil) //nolint:noctx
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

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
		var dialer net.Dialer
		conn, err := dialer.DialContext(context.Background(), "tcp", "127.0.0.1:6001")
		assert.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		pipe := &jobsProto.DeclareRequest{Pipeline: map[string]string{
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
			pipe.Pipeline["queue_headers"] = headers
		}

		if exchangeDeclare != "" {
			pipe.Pipeline["exchange_declare"] = exchangeDeclare
		}

		if queueDeclare != "" {
			pipe.Pipeline["queue_declare"] = queueDeclare
		}

		er := &jobsProto.Empty{}
		err = client.Call("jobs.Declare", pipe, er)
		assert.NoError(t, err)
	}
}

func Reset(t *testing.T) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(context.Background(), "tcp", "127.0.0.1:6001")
	assert.NoError(t, err)
	c := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

	var ret bool
	err = c.Call("resetter.Reset", "jobs", &ret)
	assert.NoError(t, err)
	require.True(t, ret)
}
