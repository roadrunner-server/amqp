package tests

import (
	"testing"
	"time"

	"tests/helpers"

	"github.com/stretchr/testify/require"
)

// slowAddr serves the configs dialing the broker whose consumer_timeout is
// lowered, so a worker outliving it gets its delivery canceled by rabbit.
const slowAddr = "127.0.0.1:6001"

// cancelWait bounds the waits around the consumer timeout: rabbit evaluates
// the timeouts on a roughly once a minute tick, and the worker itself holds
// the job for over a minute, so records here can lag by two minutes.
const cancelWait = time.Second * 150

// TestSlowWorkerTriggersRedial covers the consumer timeout path: the worker
// outlives rabbit's consumer_timeout, the broker kills the channel mid
// processing and the driver has to redial and redeclare rather than stay deaf.
// The old test slept a flat 120 seconds.
func TestSlowWorkerTriggersRedial(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-slow.yaml", slowAddr)

	helpers.PushToPipe("test-1", false, slowAddr)(t)

	// rabbit cancels the delivery once the worker overruns consumer_timeout
	rr.WaitLogWithin(t, "delivery channel was closed, leaving the AMQP listener", 1, cancelWait)
	rr.WaitLog(t, "amqp dial was succeed. trying to redeclare queues and subscribers", 1)
	rr.WaitLog(t, "queues and subscribers was redeclared successfully", 1)
	rr.WaitLog(t, "connection was successfully restored", 1)
	rr.WaitLog(t, "redialer restarted", 1)

	// the restored pipeline has to take work again. The worker holds every
	// delivery past the consumer timeout, so an ack can race the next channel
	// kill; what must hold is that deliveries keep flowing after the redial.
	helpers.PushEventually(t, slowAddr, "test-1")
	rr.WaitLogWithin(t, "job processing was started", 2, cancelWait)

	helpers.DestroyPipelines(slowAddr, "test-1")(t)
}

// TestSlowWorkerAutoAck runs the same overrun with auto ack: the delivery is
// acknowledged at receive, so the consumer timeout has nothing unacked to kill
// and the broker must not touch the connection while the worker grinds on.
func TestSlowWorkerAutoAck(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-slow.yaml", slowAddr)

	helpers.PushToPipe("test-1", true, slowAddr)(t)

	rr.WaitLog(t, "using auto acknowledge for the job", 1)

	// the worker outlives consumer_timeout several times over; with the
	// delivery already acked, no cancel may arrive while it runs
	rr.WaitLogWithin(t, "job was processed successfully", 1, cancelWait)
	require.Zero(t, rr.CountLog("delivery channel was closed, leaving the AMQP listener"),
		"an acked delivery must not be killed by the consumer timeout")

	helpers.DestroyPipelines(slowAddr, "test-1")(t)

	// the destroy tears the channel down, which is the only close expected
	rr.RequireLogCount(t, "delivery channel was closed, leaving the AMQP listener", 1)
}
