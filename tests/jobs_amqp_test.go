package amqp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"testing"
	"time"

	"tests/helpers"
	mocklogger "tests/mock"

	"github.com/goccy/go-json"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	amqpDriver "github.com/roadrunner-server/amqp/v4"
	jobsProto "github.com/roadrunner-server/api/v4/build/jobs/v1"
	jobsState "github.com/roadrunner-server/api/v4/plugins/v1/jobs"
	"github.com/roadrunner-server/config/v4"
	"github.com/roadrunner-server/endure/v2"
	goridgeRpc "github.com/roadrunner-server/goridge/v3/pkg/rpc"
	"github.com/roadrunner-server/informer/v4"
	"github.com/roadrunner-server/jobs/v4"
	"github.com/roadrunner-server/logger/v4"
	"github.com/roadrunner-server/metrics/v4"
	"github.com/roadrunner-server/otel/v4"
	"github.com/roadrunner-server/resetter/v4"
	rpcPlugin "github.com/roadrunner-server/rpc/v4"
	"github.com/roadrunner-server/server/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fanout received job queue name
func TestAMQPFanoutQueueName(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.1.3",
		Path:    "configs/.rr-amqp-fanout.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		l,
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-fanout-1", false, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-fanout-2", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-fanout-1", "test-fanout-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job was processed successfully").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPHeaders(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.12.2",
		Path:    "configs/.rr-amqp-headers.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		l,
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("PipelineDestroy", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPHeadersXRoutingKey(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-xroutingkey.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		l,
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

	jb := &jobsProto.Job{
		Job:     "some/php/namespace",
		Id:      uuid.NewString(),
		Payload: []byte(`{"hello":"world"}`),
		Headers: map[string]*jobsProto.HeaderValue{
			"test":          {Value: []string{"test2"}},
			"x-routing-key": {Value: []string{"super-routing-key"}},
		},
		Options: &jobsProto.Options{
			AutoAck:  false,
			Priority: 1,
			Pipeline: "test-1",
		},
	}

	conn, err := net.Dial("tcp", "127.0.0.1:6001")
	require.NoError(t, err)
	client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

	req := &jobsProto.PushRequest{
		Job: jb,
	}

	er := &jobsProto.Empty{}
	err = client.Call("jobs.Push", req, er)
	require.NoError(t, err)

	time.Sleep(time.Second * 3)

	t.Run("PipelineDestroy", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPDeclareHeaders(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.12.2",
		Path:    "configs/.rr-amqp-headers-declare.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
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

	headers := `{"x-queue-type": "quorum"}`

	t.Run("DeclareAMQPPipeline", helpers.DeclareAMQPPipe("test-6", "test-6", "test-6", headers, "false", "true"))
	t.Run("ConsumeAMQPPipeline", helpers.ResumePipes("127.0.0.1:6001", "test-6"))
	t.Run("PushAMQPPipeline", helpers.PushToPipe("test-6", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("PauseAMQPPipeline", helpers.PausePipelines("127.0.0.1:6001", "test-6"))
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-6"))

	stopCh <- struct{}{}
	wg.Wait()

	assert.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was resumed").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("job processing was started").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("job was processed successfully").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPInitTLS(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.2.0",
		Path:    "configs/.rr-amqp-init-tls.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		l,
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6111"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6111"))
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6111", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job was processed successfully").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPRemoveAllFromPQ(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.2.0",
		Path:    "configs/.rr-amqp-pq.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		l,
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
	for i := 0; i < 100; i++ {
		t.Run("PushToPipelineTest1PQ", helpers.PushToPipe("test-1-pq", false, "127.0.0.1:6601"))
		t.Run("PushToPipelineTest2PQ", helpers.PushToPipe("test-2-pq", false, "127.0.0.1:6601"))
	}

	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6601", "test-1-pq", "test-2-pq"))
	time.Sleep(time.Second)

	stopCh <- struct{}{}
	wg.Wait()

	assert.Equal(t, 0, oLogger.FilterMessageSnippet("job was processed successfully").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	assert.Equal(t, 200, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("job processing was started").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQP20Pipelines(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-parallel.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		l,
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
	for i := 1; i < 21; i++ {
		t.Run("PushToPipeline", helpers.PushToPipe(fmt.Sprintf("test-%d", i), false, "127.0.0.1:6001"))
	}

	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001",
		"test-1",
		"test-2",
		"test-3",
		"test-4",
		"test-5",
		"test-6",
		"test-7",
		"test-8",
		"test-9",
		"test-10",
		"test-11",
		"test-12",
		"test-13",
		"test-14",
		"test-15",
		"test-16",
		"test-17",
		"test-18",
		"test-19",
		"test-20",
	))

	stopCh <- struct{}{}
	wg.Wait()

	assert.Equal(t, 10, oLogger.FilterMessageSnippet("exited from jobs processor").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("initializing driver").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("driver ready").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("pipeline was started").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("job processing was started").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPInit(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.9.0",
		Path:    "configs/.rr-amqp-init.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		l,
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPInitV27(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Path:    "configs/.rr-amqp-init.yaml",
		Prefix:  "rr",
		Version: "2.7.0",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPRoutingQueue(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Path:    "configs/.rr-amqp-routing-queue.yaml",
		Prefix:  "rr",
		Version: "2.10.5",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
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
	// push to only 1 pipeline
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)

	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPInitV27RR27(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Path:    "configs/.rr-amqp-init-v27.yaml",
		Prefix:  "rr",
		Version: "2.7.0",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPInitV27RR27Durable(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Path:    "configs/.rr-amqp-init-v27-durable.yaml",
		Prefix:  "rr",
		Version: "2.7.0",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPReset(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.9.0",
		Path:    "configs/.rr-amqp-init.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	helpers.Reset(t)
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)

	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 4, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 4, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 4, oLogger.FilterMessageSnippet("job was processed successfully").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPDeclare(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.9.0",
		Path:    "configs/.rr-amqp-declare.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
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

	t.Run("DeclareAMQPPipeline", helpers.DeclareAMQPPipe("test-3", "test-3", "test-3", "", "true", "false"))
	t.Run("ConsumeAMQPPipeline", helpers.ResumePipes("127.0.0.1:6001", "test-3"))
	t.Run("PushAMQPPipeline", helpers.PushToPipe("test-3", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("PauseAMQPPipeline", helpers.PausePipelines("127.0.0.1:6001", "test-3"))
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-3"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was resumed").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job was processed successfully").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPDeclareDurable(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.9.0",
		Path:    "configs/.rr-amqp-declare.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
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

	t.Run("DeclareAMQPPipeline", helpers.DeclareAMQPPipe("test-8", "test-8", "test-8", "", "true", "true"))
	t.Run("ConsumeAMQPPipeline", helpers.ResumePipes("127.0.0.1:6001", "test-8"))
	t.Run("PushAMQPPipeline", helpers.PushToPipe("test-8", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("PauseAMQPPipeline", helpers.PausePipelines("127.0.0.1:6001", "test-8"))
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-8"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was resumed").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job was processed successfully").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPJobsError(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.9.0",
		Path:    "configs/.rr-amqp-jobs-err.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
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

	t.Run("DeclareAMQPPipeline", helpers.DeclareAMQPPipe("test-4", "test-4", "test-4", "", "true", "false"))
	t.Run("ConsumeAMQPPipeline", helpers.ResumePipes("127.0.0.1:6001", "test-4"))
	t.Run("PushAMQPPipeline", helpers.PushToPipe("test-4", false, "127.0.0.1:6001"))
	time.Sleep(time.Second * 25)
	t.Run("PauseAMQPPipeline", helpers.PausePipelines("127.0.0.1:6001", "test-4"))
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-4"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 1, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 4, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 4, oLogger.FilterMessageSnippet("job was processed successfully").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was paused").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was resumed").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 3, oLogger.FilterMessageSnippet("jobs protocol error").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPNoGlobalSection(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.9.0",
		Path:    "configs/.rr-no-global.yaml",
		Prefix:  "rr",
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

	_, err = cont.Serve()
	require.NoError(t, err)
	_ = cont.Stop()
}

func TestAMQPStats(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.9.0",
		Path:    "configs/.rr-amqp-declare.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
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

	t.Run("DeclareAMQPPipeline", helpers.DeclareAMQPPipe("test-5", "test-5", "test-5", "", "true", "false"))
	t.Run("ConsumeAMQPPipeline", helpers.ResumePipes("127.0.0.1:6001", "test-5"))
	t.Run("PushAMQPPipeline", helpers.PushToPipe("test-5", false, "127.0.0.1:6001"))
	time.Sleep(time.Second * 2)
	t.Run("PauseAMQPPipeline", helpers.PausePipelines("127.0.0.1:6001", "test-5"))
	time.Sleep(time.Second * 2)
	t.Run("PushAMQPPipeline", helpers.PushToPipe("test-5", false, "127.0.0.1:6001"))
	t.Run("PushPipelineDelayed", helpers.PushToPipeDelayed("127.0.0.1:6001", "test-5", 5))

	out := &jobsState.State{}
	t.Run("Stats", helpers.Stats("127.0.0.1:6001", out))

	assert.Equal(t, out.Pipeline, "test-5")
	assert.Equal(t, out.Driver, "amqp")
	assert.Equal(t, out.Queue, "test-5")

	assert.Equal(t, int64(1), out.Active)
	assert.Equal(t, int64(1), out.Delayed)
	assert.Equal(t, int64(0), out.Reserved)
	assert.Equal(t, uint64(3), out.Priority)
	assert.Equal(t, false, out.Ready)

	time.Sleep(time.Second)
	t.Run("ResumePipeline", helpers.ResumePipes("127.0.0.1:6001", "test-5"))
	time.Sleep(time.Second * 7)

	out = &jobsState.State{}
	t.Run("Stats", helpers.Stats("127.0.0.1:6001", out))

	assert.Equal(t, out.Pipeline, "test-5")
	assert.Equal(t, out.Driver, "amqp")
	assert.Equal(t, out.Queue, "test-5")

	assert.Equal(t, int64(0), out.Active)
	assert.Equal(t, int64(0), out.Delayed)
	assert.Equal(t, int64(0), out.Reserved)
	assert.Equal(t, uint64(3), out.Priority)
	assert.Equal(t, true, out.Ready)

	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-5"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 3, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 3, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 3, oLogger.FilterMessageSnippet("job was processed successfully").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was paused").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was resumed").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPBadResp(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.9.0",
		Path:    "configs/.rr-amqp-init-br.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		l,
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("response handler error").Len())
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

// redialer should be restarted
// ack timeout is 30 seconds
func TestAMQPSlow(t *testing.T) {
	cont := endure.New(slog.LevelDebug, endure.GracefulShutdownTimeout(time.Minute*5))

	cfg := &config.Plugin{
		Version: "2.9.2",
		Path:    "configs/.rr-amqp-slow.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		l,
		&resetter.Plugin{},
		&metrics.Plugin{},
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	time.Sleep(time.Second * 40)
	for i := 0; i < 10; i++ {
		t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	}
	time.Sleep(time.Second * 80)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1"))

	stopCh <- struct{}{}
	wg.Wait()

	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet(`number of listeners`).Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("consume channel close").Len(), 1)
	assert.GreaterOrEqual(
		t,
		oLogger.FilterMessageSnippet("rabbitmq dial was succeed. trying to redeclare queues and subscribers").Len(),
		1,
	)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("queues and subscribers was redeclared successfully").Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("connection was successfully restored").Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("redialer restarted").Len(), 1)
}

// Use auto-ack, jobs should not be timeouted
func TestAMQPSlowAutoAck(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.9.2",
		Path:    "configs/.rr-amqp-slow.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		l,
		&resetter.Plugin{},
		&metrics.Plugin{},
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", true, "127.0.0.1:6001"))
	time.Sleep(time.Second * 40)
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", true, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", true, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", true, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", true, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", true, "127.0.0.1:6001"))
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", true, "127.0.0.1:6001"))
	time.Sleep(time.Second * 80)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1"))

	stopCh <- struct{}{}
	wg.Wait()

	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet(`number of listeners`).Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("consume channel close").Len(), 0)
	assert.GreaterOrEqual(
		t,
		oLogger.FilterMessageSnippet("rabbitmq dial was succeed. trying to redeclare queues and subscribers").Len(),
		0,
	)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("queues and subscribers was redeclared successfully").Len(), 0)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("connection was successfully restored").Len(), 0)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("redialer restarted").Len(), 0)
}

// custom payload
func TestAMQPRawPayload(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2.10.1",
		Path:    "configs/.rr-amqp-raw.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		l,
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

	conn, err := amqp.Dial("amqp://guest:guest@127.0.0.1:5672/")
	assert.NoError(t, err)

	channel, err := conn.Channel()
	assert.NoError(t, err)

	// declare an exchange (idempotent operation)
	err = channel.ExchangeDeclare(
		"default",
		"direct",
		false,
		false,
		false,
		false,
		nil,
	)
	assert.NoError(t, err)

	// verify or declare a queue
	q, err := channel.QueueDeclare(
		"test-raw-queue",
		false,
		false,
		false,
		false,
		nil,
	)
	assert.NoError(t, err)

	// bind queue to the exchange
	err = channel.QueueBind(
		q.Name,
		"test-raw",
		"default",
		false,
		nil,
	)
	assert.NoError(t, err)

	pch, err := conn.Channel()
	assert.NoError(t, err)
	require.NotNil(t, pch)

	err = pch.PublishWithContext(context.Background(), "default", "test-raw", false, false, amqp.Publishing{
		Headers:   amqp.Table{"foo": 2.3},
		Timestamp: time.Now(),
		Body:      []byte("foooobarrrrrrrrbazzzzzzzzzzzzzzzzzzzzzzzzz"),
	})
	require.NoError(t, err)

	time.Sleep(time.Second * 10)

	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-raw"))

	stopCh <- struct{}{}
	wg.Wait()

	assert.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was started").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("job processing was started").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())
}

func TestAMQPOTEL(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.1.0",
		Path:    "configs/.rr-amqp-otel.yaml",
		Prefix:  "rr",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		&otel.Plugin{},
		l,
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
	t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6100"))
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6100", "test-1"))

	stopCh <- struct{}{}
	wg.Wait()

	resp, err := http.Get("http://127.0.0.1:9411/api/v2/spans?serviceName=rr_test_amqp") //nolint:noctx
	require.NoError(t, err)
	require.NotNil(t, resp)

	buf, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var spans []string
	err = json.Unmarshal(buf, &spans)
	assert.NoError(t, err)

	sort.Slice(spans, func(i, j int) bool {
		return spans[i] < spans[j]
	})

	expected := []string{
		"amqp_listener",
		"amqp_push",
		"amqp_stop",
		"destroy_pipeline",
		"jobs_listener",
		"push",
	}
	assert.Equal(t, expected, spans)

	assert.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was started").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("job processing was started").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the rabbit listener").Len())

	t.Cleanup(func() {
		_ = resp.Body.Close()
	})
}
