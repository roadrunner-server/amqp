package tests

import (
	"testing"
	"time"

	"tests/helpers"

	amqp "github.com/rabbitmq/amqp091-go"
	jobsProto "github.com/roadrunner-server/api-go/v6/jobs/v1"
	apiJobs "github.com/roadrunner-server/api-plugins/v6/jobs"
	"github.com/stretchr/testify/require"
)

const (
	durabilityAddr = "127.0.0.1:6001"
	// proxyName fronts rabbitmq on 23679, which the durability configs dial.
	// Both addresses are inside the compose network; 23679 is published.
	proxyName     = "redial"
	proxyListen   = "0.0.0.0:23679"
	proxyUpstream = "rabbitmq:5672"
)

// TestRedialAfterOutage checks recovery on one broker while the other continues processing.
func TestRedialAfterOutage(t *testing.T) {
	helpers.CreateProxy(t, proxyName, proxyListen, proxyUpstream)

	rr, _ := helpers.Start(t, "configs/.rr-amqp-durability-redial.yaml", jobsPlugins(),
		helpers.WithObservedLogger(),
		helpers.WithTCPProbe(durabilityAddr),
	)

	rr.WaitLog(t, "pipeline was started", 2)

	helpers.SetProxyEnabled(t, proxyName, false)

	helpers.PushExpectError(durabilityAddr, "test-1")(t)
	client := helpers.NewJobsClient(t, durabilityAddr)
	require.NoError(t, client.Call("jobs.Push", &jobsProto.PushRequest{Job: &jobsProto.Job{
		Job:     "redial.healthy",
		Id:      "redial-healthy-broker",
		Payload: []byte("during outage"),
		Options: &jobsProto.Options{Pipeline: "test-2"},
	}}, &jobsProto.Empty{}))
	require.Eventually(t, func() bool {
		return rr.Logs.FilterMessage("job was processed successfully").FilterAttr("ID", "redial-healthy-broker").Len() == 1
	}, time.Minute, 50*time.Millisecond)

	helpers.SetProxyEnabled(t, proxyName, true)

	rr.WaitLog(t, "connection was successfully restored", 1)

	helpers.PushEventually(t, durabilityAddr, "test-1")

	// Publish directly to the original broker to check the restored consumer's connection.
	publishRaw(t, "default", "test-1", amqp.Publishing{
		Headers: amqp.Table{apiJobs.RRID: "redial-original-broker"},
		Body:    []byte("after redial"),
	})
	require.Eventually(t, func() bool {
		return rr.Logs.FilterMessage("job was processed successfully").FilterAttr("ID", "redial-original-broker").Len() == 1
	}, time.Minute, 50*time.Millisecond)

	helpers.DestroyPipelines(durabilityAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "pipeline was stopped", 2)
}

// TestRedialWithoutQueue covers a push-only pipeline with no queue: it reports
// empty state, rejects resume and pause, and survives an outage the same way.
func TestRedialWithoutQueue(t *testing.T) {
	helpers.CreateProxy(t, proxyName, proxyListen, proxyUpstream)

	rr, _ := helpers.Start(t, "configs/.rr-amqp-durability-no-queue.yaml", jobsPlugins(),
		helpers.WithObservedLogger(),
		helpers.WithTCPProbe(durabilityAddr),
	)

	state := helpers.StatsFor(t, durabilityAddr, "push_pipeline")
	require.Equal(t, "amqp", state.Driver)
	require.Empty(t, state.Queue)

	helpers.PushToPipe("push_pipeline", false, durabilityAddr)(t)
	rr.WaitLog(t, "job was pushed successfully", 1)

	// a pipeline with no queue has nothing to consume or pause
	helpers.ResumePipesErr(durabilityAddr, "empty queue name", "push_pipeline")(t)
	helpers.PausePipelinesErr(durabilityAddr, "empty queue name", "push_pipeline")(t)

	helpers.SetProxyEnabled(t, proxyName, false)
	helpers.SetProxyEnabled(t, proxyName, true)

	rr.WaitLog(t, "connection was successfully restored", 1)
	rr.WaitLog(t, "redialer restarted", 1)

	helpers.PushEventually(t, durabilityAddr, "push_pipeline")

	helpers.DestroyPipelines(durabilityAddr, "push_pipeline")(t)

	rr.RequireLogCount(t, "pipeline was stopped", 1)
	require.Zero(t, rr.CountLog("amqp connection closed"))
}
