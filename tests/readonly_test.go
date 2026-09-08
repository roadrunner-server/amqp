package tests

import (
	"testing"

	"tests/helpers"

	"github.com/stretchr/testify/require"
)

// The readonly user has read and write permissions but no configure permission. The broker's definitions file creates the exchange, queue, and binding. See https://www.rabbitmq.com/docs/3.13/access-control#authorisation.

// TestReadOnlyDeclareOff uses existing broker resources with declarations disabled.
func TestReadOnlyDeclareOff(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-readonly-declare-off.yaml", initAddr)

	rr.WaitLog(t, "pipeline was started", 1)

	helpers.PushToPipe("readonly-ok", false, initAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.DestroyPipelines(initAddr, "readonly-ok")(t)

	rr.RequireLogCount(t, "pipeline was stopped", 1)
}

// TestReadOnlyDeclareOn checks that the broker rejects declarations without configure permission.
func TestReadOnlyDeclareOn(t *testing.T) {
	err := helpers.StartExpectServeError(t, "configs/.rr-amqp-readonly-declare-on.yaml", jobsPlugins())

	require.ErrorContains(t, err, "ACCESS_REFUSED")
}
