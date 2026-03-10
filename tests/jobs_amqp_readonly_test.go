package amqp

import (
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"tests/helpers"
	mocklogger "tests/mock"

	amqpDriver "github.com/roadrunner-server/amqp/v6"
	"github.com/roadrunner-server/config/v5"
	"github.com/roadrunner-server/endure/v2"
	"github.com/roadrunner-server/informer/v5"
	"github.com/roadrunner-server/jobs/v5"
	"github.com/roadrunner-server/logger/v5"
	"github.com/roadrunner-server/resetter/v5"
	rpcPlugin "github.com/roadrunner-server/rpc/v5"
	"github.com/roadrunner-server/server/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestAMQPReadOnlyDeclareOff verifies that a readonly AMQP user (configure="^$")
// can successfully consume from pre-created topology when declare flags are off.
func TestAMQPReadOnlyDeclareOff(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.1.0",
		Path:    "configs/.rr-amqp-readonly-declare-off.yaml",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		l,
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&amqpDriver.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}

	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	time.Sleep(time.Second * 3)
	t.Run("PushToReadOnlyPipeline", helpers.PushToPipe("readonly-ok", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("DestroyReadOnlyPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "readonly-ok"))

	stopCh <- struct{}{}
	wg.Wait()

	require.GreaterOrEqual(t, oLogger.FilterMessageSnippet("pipeline was started").Len(), 1)
	require.GreaterOrEqual(t, oLogger.FilterMessageSnippet("job was pushed successfully").Len(), 1)
	require.GreaterOrEqual(t, oLogger.FilterMessageSnippet("job was processed successfully").Len(), 1)
	require.GreaterOrEqual(t, oLogger.FilterMessageSnippet("pipeline was stopped").Len(), 1)
}

// TestAMQPReadOnlyDeclareOn verifies that a readonly AMQP user (configure="^$")
// gets ACCESS_REFUSED when exchange/queue declare is enabled.
func TestAMQPReadOnlyDeclareOn(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.1.0",
		Path:    "configs/.rr-amqp-readonly-declare-on.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&logger.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&amqpDriver.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	stopCh := make(chan struct{}, 1)

	wg.Go(func() {
		for {
			select {
			case e := <-ch:
				assert.Contains(t, e.Error.Error(), "ACCESS_REFUSED")
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	})

	// give the container enough time to fail
	time.Sleep(time.Second * 10)

	stopCh <- struct{}{}
	wg.Wait()
}
