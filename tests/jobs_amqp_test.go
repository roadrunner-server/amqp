package amqp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "google.golang.org/genproto/protobuf/ptype" //nolint:revive,nolintlint

	"tests/helpers"
	mocklogger "tests/mock"

	"github.com/go-viper/mapstructure/v2"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	amqpDriver "github.com/roadrunner-server/amqp/v5"
	amqpjobs "github.com/roadrunner-server/amqp/v5/amqpjobs"
	jobsProto "github.com/roadrunner-server/api/v4/build/jobs/v1"
	jobsState "github.com/roadrunner-server/api/v4/plugins/v1/jobs"
	apiJobs "github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/roadrunner-server/config/v5"
	"github.com/roadrunner-server/endure/v2"
	goridgeRpc "github.com/roadrunner-server/goridge/v3/pkg/rpc"
	"github.com/roadrunner-server/informer/v5"
	"github.com/roadrunner-server/jobs/v5"
	"github.com/roadrunner-server/logger/v5"
	"github.com/roadrunner-server/metrics/v5"
	"github.com/roadrunner-server/otel/v5"
	"github.com/roadrunner-server/resetter/v5"
	rpcPlugin "github.com/roadrunner-server/rpc/v5"
	"github.com/roadrunner-server/server/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockConfigurer struct {
	has    map[string]bool
	values map[string]any
	errs   map[string]error
}

type mockQueue struct{}

func (m *mockConfigurer) Has(name string) bool {
	return m.has[name]
}

func (m *mockConfigurer) UnmarshalKey(name string, out any) error {
	if err, ok := m.errs[name]; ok {
		return err
	}

	val, ok := m.values[name]
	if !ok {
		return nil
	}

	return mapstructure.Decode(val, out)
}

func (m *mockQueue) Remove(string) []apiJobs.Job {
	return nil
}

func (m *mockQueue) Insert(apiJobs.Job) {}

func (m *mockQueue) ExtractMin() apiJobs.Job {
	return nil
}

func (m *mockQueue) Len() uint64 {
	return 0
}

func declareExistingReadOnlyTopology(t *testing.T, exchangeName, queueName, routingKeyName string) {
	t.Helper()

	conn, err := amqp.Dial("amqp://guest:guest@127.0.0.1:5672/")
	require.NoError(t, err)
	defer func() {
		_ = conn.Close()
	}()

	channel, err := conn.Channel()
	require.NoError(t, err)
	defer func() {
		_ = channel.Close()
	}()

	// Pre-create fanout topology to emulate "read-only/no configure permissions" mode.
	err = channel.ExchangeDeclare(
		exchangeName,
		"fanout",
		true,
		false,
		false,
		false,
		nil,
	)
	require.NoError(t, err)

	q, err := channel.QueueDeclare(
		queueName,
		true,
		false,
		false,
		false,
		nil,
	)
	require.NoError(t, err)

	err = channel.QueueBind(
		q.Name,
		routingKeyName,
		exchangeName,
		false,
		nil,
	)
	require.NoError(t, err)
}

func TestFromConfigValidationErrors(t *testing.T) {
	const configKey = "jobs.pipelines.test.config"
	pipe := jobs.Pipeline{
		"name":   "test",
		"driver": "amqp",
	}

	missingKey := fmt.Sprintf("/tmp/rr-amqp-missing-%s.key", uuid.NewString())
	missingCert := fmt.Sprintf("/tmp/rr-amqp-missing-%s.pem", uuid.NewString())

	tests := []struct {
		name        string
		cfg         *mockConfigurer
		errContains string
	}{
		{
			name: "missing pipeline configuration key",
			cfg: &mockConfigurer{
				has: map[string]bool{
					"amqp": true,
				},
			},
			errContains: "no configuration by provided key",
		},
		{
			name: "missing global amqp section",
			cfg: &mockConfigurer{
				has: map[string]bool{
					configKey: true,
				},
			},
			errContains: "no global amqp configuration",
		},
		{
			name: "invalid exchange type",
			cfg: &mockConfigurer{
				has: map[string]bool{
					configKey: true,
					"amqp":    true,
				},
				values: map[string]any{
					configKey: map[string]any{
						"exchange_type": "invalid",
					},
					"amqp": map[string]any{},
				},
			},
			errContains: "invalid exchange type",
		},
		{
			name: "version 2 validates nested exchange type",
			cfg: &mockConfigurer{
				has: map[string]bool{
					configKey: true,
					"amqp":    true,
				},
				values: map[string]any{
					configKey: map[string]any{
						"version": 2,
						"exchange": map[string]any{
							"name": "nested-exchange",
							"type": "invalid",
						},
					},
					"amqp": map[string]any{},
				},
			},
			errContains: "invalid exchange type",
		},
		{
			name: "nested exchange config with version 1 uses legacy parser",
			cfg: &mockConfigurer{
				has: map[string]bool{
					configKey: true,
					"amqp":    true,
				},
				values: map[string]any{
					configKey: map[string]any{
						"version": 1,
						"exchange": map[string]any{
							"name": "nested-exchange",
							"type": "direct",
						},
					},
					"amqp": map[string]any{},
				},
			},
			errContains: "expected type 'string'",
		},
		{
			name: "missing tls key cert files",
			cfg: &mockConfigurer{
				has: map[string]bool{
					configKey: true,
					"amqp":    true,
				},
				values: map[string]any{
					configKey: map[string]any{},
					"amqp": map[string]any{
						"tls": map[string]any{
							"key":  missingKey,
							"cert": missingCert,
						},
					},
				},
			},
			errContains: "does not exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := amqpjobs.FromConfig(nil, configKey, zap.NewNop(), tt.cfg, pipe, apiJobs.Queue(nil))
			require.Error(t, err)
			require.Nil(t, d)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestFromConfigVersion2IgnoresLegacyFlatKeys(t *testing.T) {
	const configKey = "jobs.pipelines.v2_ignore.config"

	pipelineName := fmt.Sprintf("v2-ignore-%s", uuid.NewString())
	exchangeName := fmt.Sprintf("rr-v2-ex-%s", uuid.NewString())
	queueName := fmt.Sprintf("rr-v2-q-%s", uuid.NewString())
	routingKeyName := fmt.Sprintf("rr-v2-rk-%s", uuid.NewString())

	pipe := jobs.Pipeline{
		"name":   pipelineName,
		"driver": "amqp",
	}

	cfg := &mockConfigurer{
		has: map[string]bool{
			configKey: true,
			"amqp":    true,
		},
		values: map[string]any{
			configKey: map[string]any{
				"version": 2,
				// Legacy v1 keys should be ignored by the v2 parser path.
				"exchange_type":     "invalid",
				"durable":           true,
				"queue_auto_delete": true,
				"exchange": map[string]any{
					"name":    exchangeName,
					"declare": false,
				},
				"queue": map[string]any{
					"name":        queueName,
					"routing_key": routingKeyName,
					"declare":     false,
				},
			},
			"amqp": map[string]any{
				"addr": "amqp://guest:guest@127.0.0.1:5672/",
			},
		},
	}

	d, err := amqpjobs.FromConfig(nil, configKey, zap.NewNop(), cfg, pipe, &mockQueue{})
	require.NoError(t, err)
	require.NotNil(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st, err := d.State(ctx)
	require.NoError(t, err)
	require.NotNil(t, st)
	assert.Equal(t, queueName, st.Queue)
	assert.Equal(t, pipelineName, st.Pipeline)
	assert.Equal(t, "amqp", st.Driver)
	assert.False(t, st.Ready)

	require.NoError(t, d.Stop(ctx))
}

func TestFromPipelineValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *mockConfigurer
		pipeline    jobs.Pipeline
		errContains string
	}{
		{
			name: "missing global amqp section",
			cfg: &mockConfigurer{
				has: map[string]bool{},
			},
			pipeline: jobs.Pipeline{
				"name":   "test-pipeline",
				"driver": "amqp",
			},
			errContains: "no global amqp configuration",
		},
		{
			name: "malformed queue headers json",
			cfg: &mockConfigurer{
				has: map[string]bool{
					"amqp": true,
				},
				values: map[string]any{
					"amqp": map[string]any{},
				},
			},
			pipeline: jobs.Pipeline{
				"name":   "test-pipeline",
				"driver": "amqp",
				"config": map[string]any{
					"queue_headers": "{invalid-json",
				},
			},
			errContains: "invalid character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := amqpjobs.FromPipeline(nil, tt.pipeline, zap.NewNop(), tt.cfg, apiJobs.Queue(nil))
			require.Error(t, err)
			require.Nil(t, d)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestFromPipelineReadOnlyDeclarations(t *testing.T) {
	exchangeName := fmt.Sprintf("rr-ro-pipe-ex-%s", uuid.NewString())
	queueName := fmt.Sprintf("rr-ro-pipe-q-%s", uuid.NewString())
	routingKeyName := fmt.Sprintf("rr-ro-pipe-rk-%s", uuid.NewString())
	pipelineName := fmt.Sprintf("readonly-pipe-%s", uuid.NewString())

	declareExistingReadOnlyTopology(t, exchangeName, queueName, routingKeyName)

	cfg := &mockConfigurer{
		has: map[string]bool{
			"amqp": true,
		},
		values: map[string]any{
			"amqp": map[string]any{
				"addr": "amqp://guest:guest@127.0.0.1:5672/",
			},
		},
	}

	buildPipeline := func(exchangeDeclareValue, queueDeclareValue string) jobs.Pipeline {
		return jobs.Pipeline{
			"name":             pipelineName,
			"driver":           "amqp",
			"exchange":         exchangeName,
			"exchange_type":    "direct",
			"queue":            queueName,
			"routing_key":      routingKeyName,
			"exchange_declare": exchangeDeclareValue,
			"queue_declare":    queueDeclareValue,
		}
	}

	t.Run("declare enabled should fail on topology mismatch", func(t *testing.T) {
		d, err := amqpjobs.FromPipeline(nil, buildPipeline("true", "true"), zap.NewNop(), cfg, &mockQueue{})
		require.Error(t, err)
		require.Nil(t, d)
		assert.Contains(t, err.Error(), "inequivalent arg 'type'")
	})

	t.Run("declare disabled should skip declaration and allow state", func(t *testing.T) {
		d, err := amqpjobs.FromPipeline(nil, buildPipeline("false", "false"), zap.NewNop(), cfg, &mockQueue{})
		require.NoError(t, err)
		require.NotNil(t, d)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		st, err := d.State(ctx)
		require.NoError(t, err)
		require.NotNil(t, st)
		assert.Equal(t, queueName, st.Queue)
		assert.Equal(t, pipelineName, st.Pipeline)
		assert.Equal(t, "amqp", st.Driver)
		assert.False(t, st.Ready)

		require.NoError(t, d.Stop(ctx))
	})
}

func TestFromConfigReadOnlyDeclarations(t *testing.T) {
	const configKey = "jobs.pipelines.readonly.config"

	exchangeName := fmt.Sprintf("rr-ro-ex-%s", uuid.NewString())
	queueName := fmt.Sprintf("rr-ro-q-%s", uuid.NewString())
	routingKeyName := fmt.Sprintf("rr-ro-rk-%s", uuid.NewString())
	pipelineName := fmt.Sprintf("readonly-%s", uuid.NewString())

	declareExistingReadOnlyTopology(t, exchangeName, queueName, routingKeyName)

	pipe := jobs.Pipeline{
		"name":   pipelineName,
		"driver": "amqp",
	}

	buildConfigurer := func(declare bool) *mockConfigurer {
		return &mockConfigurer{
			has: map[string]bool{
				configKey: true,
				"amqp":    true,
			},
			values: map[string]any{
				configKey: map[string]any{
					"version": 2,
					// legacy keys should be ignored in favor of nested entity config
					"exchange_type": "fanout",
					"exchange": map[string]any{
						"name":    exchangeName,
						"type":    "direct",
						"declare": declare,
					},
					"queue": map[string]any{
						"name":        queueName,
						"routing_key": routingKeyName,
						"declare":     declare,
					},
				},
				"amqp": map[string]any{
					"addr": "amqp://guest:guest@127.0.0.1:5672/",
				},
			},
		}
	}

	t.Run("declare enabled should fail on topology mismatch", func(t *testing.T) {
		d, err := amqpjobs.FromConfig(nil, configKey, zap.NewNop(), buildConfigurer(true), pipe, &mockQueue{})
		require.Error(t, err)
		require.Nil(t, d)
		assert.Contains(t, err.Error(), "inequivalent arg 'type'")
	})

	t.Run("declare disabled should skip declaration and allow state", func(t *testing.T) {
		d, err := amqpjobs.FromConfig(nil, configKey, zap.NewNop(), buildConfigurer(false), pipe, &mockQueue{})
		require.NoError(t, err)
		require.NotNil(t, d)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		st, err := d.State(ctx)
		require.NoError(t, err)
		require.NotNil(t, st)
		assert.Equal(t, queueName, st.Queue)
		assert.Equal(t, pipelineName, st.Pipeline)
		assert.Equal(t, "amqp", st.Driver)
		assert.False(t, st.Ready)

		require.NoError(t, d.Stop(ctx))
	})
}

// fanout received job queue name
func TestAMQPFanoutQueueName(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.1.3",
		Path:    "configs/.rr-amqp-fanout.yaml",
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
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPHeaders(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.1.0",
		Path:    "configs/.rr-amqp-headers.yaml",
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
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPHeadersXRoutingKey(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-xroutingkey.yaml",
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

	var d net.Dialer
	conn, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:6001")
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
	require.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPDeclareHeaders(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2024.1.0",
		Path:    "configs/.rr-amqp-headers-declare.yaml",
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
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPInitTLS(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.2.0",
		Path:    "configs/.rr-amqp-init-tls.yaml",
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
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPRemoveAllFromPQ(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.2.0",
		Path:    "configs/.rr-amqp-pq.yaml",
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
	for range 100 {
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
	assert.Equal(t, 4, oLogger.FilterMessageSnippet("job processing was started").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQP20Pipelines(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-parallel.yaml",
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

	assert.Equal(t, 10, oLogger.FilterMessageSnippet("exited from jobs pipeline processor").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("initializing driver").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("driver ready").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("pipeline was started").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("job processing was started").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPBug1792(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.7",
		Path:    "configs/.rr-amqp-bug-1792.yaml",
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
	t.Run("PushPipelineDelayed", helpers.PushToPipeDelayed("127.0.0.1:1792", "queue1", 5))
	t.Run("PushPipelineDelayed", helpers.PushToPipeDelayed("127.0.0.1:1792", "queue2", 5))
	t.Run("ResumePipelines", helpers.ResumePipes("127.0.0.1:1792", "queue1", "queue2"))
	time.Sleep(time.Second * 10)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:1792", "queue1", "queue2"))

	stopCh <- struct{}{}
	wg.Wait()

	assert.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("job processing was started").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPInit(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.7",
		Path:    "configs/.rr-amqp-init.yaml",
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
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPRoutingQueue(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Path:    "configs/.rr-amqp-routing-queue.yaml",
		Version: "2024.1.0",
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
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPReset(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-init.yaml",
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
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPDeclare(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-declare.yaml",
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
	t.Run(
		"ConsumeAMQPPipelineAlreadyActiveErr",
		helpers.ResumePipesErr("127.0.0.1:6001", "already in the active state", "test-3"),
	)
	t.Run("PushAMQPPipeline", helpers.PushToPipe("test-3", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("PauseAMQPPipeline", helpers.PausePipelines("127.0.0.1:6001", "test-3"))
	t.Run(
		"PauseAMQPPipelineNoListenersErr",
		helpers.PausePipelinesErr("127.0.0.1:6001", "no active listeners", "test-3"),
	)
	time.Sleep(time.Second)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-3"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was resumed").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job processing was started").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("job was processed successfully").Len())
	require.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPDeclareDurable(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-declare.yaml",
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
	require.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPJobsError(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-jobs-err.yaml",
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
	require.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPNoGlobalSection(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-no-global.yaml",
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
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-declare.yaml",
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
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

func TestAMQPBadResp(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-init-br.yaml",
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
	require.Equal(t, 2, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
}

// redialer should be restarted
// ack timeout is 30 seconds
func TestAMQPSlow(t *testing.T) {
	cont := endure.New(slog.LevelDebug, endure.GracefulShutdownTimeout(time.Minute*5))

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-amqp-slow.yaml",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		l,
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
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
	for range 10 {
		t.Run("PushToPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	}
	time.Sleep(time.Second * 80)
	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1"))

	stopCh <- struct{}{}
	wg.Wait()

	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet(`number of listeners`).Len(), 1)
	assert.Equal(t, oLogger.FilterMessageSnippet("consume channel close").Len(), 0)
	assert.GreaterOrEqual(
		t,
		oLogger.FilterMessageSnippet("amqp dial was succeed. trying to redeclare queues and subscribers").Len(),
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
		Version: "2024.1.0",
		Path:    "configs/.rr-amqp-slow.yaml",
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

	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet(`number of listeners`).Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("consume channel close").Len(), 0)
	assert.GreaterOrEqual(
		t,
		oLogger.FilterMessageSnippet("amqp dial was succeed. trying to redeclare queues and subscribers").Len(),
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
		Version: "2024.1.0",
		Path:    "configs/.rr-amqp-raw.yaml",
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

	err = pch.PublishWithContext(context.Background(), "default", "test-raw", false, false, amqp.Publishing{
		Headers: amqp.Table{
			apiJobs.RRHeaders:  []byte(`{"broken-json"`),
			apiJobs.RRDelay:    "wrong-delay-type",
			apiJobs.RRPriority: true,
		},
		Timestamp: time.Now(),
		Body:      []byte("payload with malformed rr metadata"),
	})
	require.NoError(t, err)

	time.Sleep(time.Second * 10)

	t.Run("DestroyAMQPPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-raw"))

	stopCh <- struct{}{}
	wg.Wait()

	assert.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was started").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("job processing was started").Len())
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("missing header rr_id, generating new one").Len(), 2)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("missing header rr_job, using the standard one").Len(), 2)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("failed to unmarshal headers (should be JSON), continuing execution").Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("unknown delay type").Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("unknown priority type").Len(), 1)
}

func TestAMQPOTEL(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.1.0",
		Path:    "configs/.rr-amqp-otel.yaml",
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
	slices.Sort(spans)
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
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("delivery channel was closed, leaving the AMQP listener").Len())

	t.Cleanup(func() {
		_ = resp.Body.Close()
	})
}
