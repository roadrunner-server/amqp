package tests

import (
	"testing"

	"tests/helpers"

	jobState "github.com/roadrunner-server/api-plugins/v6/jobs"
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

// TestRedialAfterOutage cuts the connection to rabbitmq underneath running
// pipelines and checks the driver redials once it comes back. The old test made
// the same calls behind 31 seconds of sleeps.
func TestRedialAfterOutage(t *testing.T) {
	helpers.CreateProxy(t, proxyName, proxyListen, proxyUpstream)

	rr, _ := helpers.Start(t, "configs/.rr-amqp-durability-redial.yaml", jobsPlugins(),
		helpers.WithObservedLogger(),
		helpers.WithTCPProbe(durabilityAddr),
	)

	rr.WaitLog(t, "pipeline was started", 2)

	helpers.SetProxyEnabled(t, proxyName, false)

	// with the broker gone, pushes have to fail rather than pretend
	helpers.PushExpectError(durabilityAddr, "test-1")(t)
	helpers.PushExpectError(durabilityAddr, "test-2")(t)

	helpers.SetProxyEnabled(t, proxyName, true)

	// the redialer has to restore both pipelines
	rr.WaitLog(t, "connection was successfully restored", 1)

	helpers.PushEventually(t, durabilityAddr, "test-1")
	helpers.PushEventually(t, durabilityAddr, "test-2")

	rr.WaitLog(t, "job was processed successfully", 2)

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

var _ = jobState.State{}
