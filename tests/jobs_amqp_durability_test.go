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

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
	amqpDriver "github.com/roadrunner-server/amqp/v5"
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
)

func TestDurabilityAMQP(t *testing.T) {
	newClient := toxiproxy.NewClient("127.0.0.1:8474")

	_, err := newClient.CreateProxy("redial", "127.0.0.1:23679", "127.0.0.1:5672")
	require.NoError(t, err)
	defer helpers.DeleteProxy("redial", t)

	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-durability-redial.yaml",
	}

	err = cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&logger.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&amqpDriver.Plugin{},
	)
	require.NoError(t, err)

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
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
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
	}()

	time.Sleep(time.Second * 3)
	helpers.DisableProxy("redial", t)
	time.Sleep(time.Second * 3)

	go func() {
		time.Sleep(time.Second * 5)
		helpers.EnableProxy("redial", t)
	}()

	t.Run("PushPipelineWhileRedialing-1", helpers.PushToPipeErr("test-1"))
	t.Run("PushPipelineWhileRedialing-2", helpers.PushToPipeErr("test-2"))

	time.Sleep(time.Second * 15)
	t.Run("PushPipelineWhileRedialing-1", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushPipelineWhileRedialing-2", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))

	time.Sleep(time.Second * 5)
	helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2")

	stopCh <- struct{}{}
	wg.Wait()
}

func TestDurabilityAMQP_NoQueue(t *testing.T) {
	newClient := toxiproxy.NewClient("127.0.0.1:8474")

	_, err := newClient.CreateProxy("redial", "127.0.0.1:23679", "127.0.0.1:5672")
	require.NoError(t, err)
	defer helpers.DeleteProxy("redial", t)

	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-durability-no-queue.yaml",
	}

	// l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err = cont.RegisterAll(
		// l,
		&logger.Plugin{},
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&amqpDriver.Plugin{},
	)
	require.NoError(t, err)

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
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
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
	}()

	address := "127.0.0.1:6001"
	time.Sleep(time.Second * 2)
	t.Run("PushPipelineWhileRedialing-1", helpers.PushToPipe("push_pipeline", false, address))
	time.Sleep(time.Second * 2)

	helpers.DisableProxy("redial", t)

	go func() {
		time.Sleep(time.Second * 5)
		helpers.EnableProxy("redial", t)
	}()

	time.Sleep(time.Second * 15)
	t.Run("PushPipelineWhileRedialing-1", helpers.PushToPipe("push_pipeline", false, address))

	time.Sleep(time.Second * 5)
	helpers.DestroyPipelines("127.0.0.1:6001", "push_pipeline")

	stopCh <- struct{}{}
	wg.Wait()

	// assert.Equal(t, oLogger.FilterMessageSnippet("job was pushed successfully").Len(), 2)
	// assert.Equal(t, oLogger.FilterMessageSnippet("amqp connection closed").Len(), 1)
	// assert.Equal(t, oLogger.FilterMessageSnippet("pipeline connection was closed, redialing").Len(), 1)

	// assert.Equal(t, oLogger.FilterMessageSnippet("amqp dial was succeed. trying to redeclare queues and subscribers").Len(), 1)
	// assert.Equal(t, oLogger.FilterMessageSnippet("queues and subscribers was redeclared successfully").Len(), 1)
	// assert.Equal(t, oLogger.FilterMessageSnippet("connection was successfully restored").Len(), 1)
	// assert.Equal(t, oLogger.FilterMessageSnippet("redialer restarted").Len(), 1)
	// assert.Equal(t, oLogger.FilterMessageSnippet("pipeline was stopped").Len(), 1)
}
