package tests

import (
	"testing"
	"time"

	"tests/helpers"

	"github.com/stretchr/testify/require"
)

const slowAddr = "127.0.0.1:6001"

// cancelWait allows for the worker delay and RabbitMQ's one-minute acknowledgement timeout checks. See https://www.rabbitmq.com/docs/3.13/consumers#acknowledgement-timeout.
const cancelWait = time.Second * 150

// TestSlowWorkerTriggersRedial checks that job delivery continues after the broker closes the delivery channel.
func TestSlowWorkerTriggersRedial(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-slow.yaml", slowAddr)

	helpers.PushToPipe("test-1", false, slowAddr)(t)

	rr.WaitLogWithin(t, "delivery channel was closed, leaving the AMQP listener", 1, cancelWait)
	rr.WaitLog(t, "amqp dial was succeed. trying to redeclare queues and subscribers", 1)
	rr.WaitLog(t, "queues and subscribers was redeclared successfully", 1)
	rr.WaitLog(t, "connection was successfully restored", 1)
	rr.WaitLog(t, "redialer restarted", 1)

	// The worker can acknowledge a delivery as the broker closes the channel. Check that delivery continues after reconnection.
	helpers.PushEventually(t, slowAddr, "test-1")
	rr.WaitLogWithin(t, "job processing was started", 2, cancelWait)

	helpers.DestroyPipelines(slowAddr, "test-1")(t)
}

// TestSlowWorkerAutoAck checks that early acknowledgement prevents a delivery timeout during worker processing. See https://www.rabbitmq.com/docs/3.13/consumers#acknowledgement-timeout.
func TestSlowWorkerAutoAck(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-slow.yaml", slowAddr)

	helpers.PushToPipe("test-1", true, slowAddr)(t)

	rr.WaitLog(t, "using auto acknowledge for the job", 1)

	// The driver acknowledges the delivery before worker processing starts.
	rr.WaitLogWithin(t, "job was processed successfully", 1, cancelWait)
	require.Zero(t, rr.CountLog("delivery channel was closed, leaving the AMQP listener"),
		"an acked delivery must not be killed by the consumer timeout")

	helpers.DestroyPipelines(slowAddr, "test-1")(t)

	// Only pipeline removal should close the delivery channel.
	rr.RequireLogCount(t, "delivery channel was closed, leaving the AMQP listener", 1)
}
