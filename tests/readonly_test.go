package tests

import (
	"testing"

	"tests/helpers"

	"github.com/stretchr/testify/require"
)

// The readonly broker from its own compose file grants the readonly user no
// configure permissions, so the driver cannot declare anything on it. The
// queue it consumes is provisioned by the broker's definitions file.

// TestReadOnlyDeclareOff covers consuming from a pre-provisioned queue without
// declaring: the pipeline has to come up and process on read permissions alone.
func TestReadOnlyDeclareOff(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-readonly-declare-off.yaml", initAddr)

	rr.WaitLog(t, "pipeline was started", 1)

	helpers.PushToPipe("readonly-ok", false, initAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.DestroyPipelines(initAddr, "readonly-ok")(t)

	rr.RequireLogCount(t, "pipeline was stopped", 1)
}

// TestReadOnlyDeclareOn covers the opposite: with declare enabled the driver
// tries to create the queue, and the broker has to refuse the boot outright.
func TestReadOnlyDeclareOn(t *testing.T) {
	err := helpers.StartExpectServeError(t, "configs/.rr-amqp-readonly-declare-on.yaml", jobsPlugins())

	require.ErrorContains(t, err, "ACCESS_REFUSED")
}
