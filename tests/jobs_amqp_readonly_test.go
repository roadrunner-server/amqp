package amqp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/google/uuid"
	amqpjobs "github.com/roadrunner-server/amqp/v5/amqpjobs"
	apiJobs "github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/roadrunner-server/jobs/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const (
	readOnlyConfigKey = "jobs.pipelines.readonly.config"
	readOnlyAMQPAddr  = "amqp://readonly:readonly@127.0.0.1:5675/TEST"

	readOnlyExchange = "CARGO_OUT"
	readOnlyQueue    = "CARGO_ECN_EVENTS"
	readOnlyBadQueue = "CARGO_ECN_EVENTS_MISSING"
	readOnlyRoute    = "CARGO_ECN_EVENTS"
)

type readOnlyMockConfigurer struct {
	has    map[string]bool
	values map[string]any
	errs   map[string]error
}

func (m *readOnlyMockConfigurer) Has(name string) bool {
	return m.has[name]
}

func (m *readOnlyMockConfigurer) UnmarshalKey(name string, out any) error {
	if err, ok := m.errs[name]; ok {
		return err
	}

	val, ok := m.values[name]
	if !ok {
		return nil
	}

	return mapstructure.Decode(val, out)
}

type readOnlyMockQueue struct{}

func (m *readOnlyMockQueue) Remove(string) []apiJobs.Job {
	return nil
}

func (m *readOnlyMockQueue) Insert(apiJobs.Job) {}

func (m *readOnlyMockQueue) ExtractMin() apiJobs.Job {
	return nil
}

func (m *readOnlyMockQueue) Len() uint64 {
	return 0
}

func TestReadOnlyFromConfigDeclarations(t *testing.T) {
	pipe := jobs.Pipeline{
		"name":   fmt.Sprintf("readonly-static-%s", uuid.NewString()),
		"driver": "amqp",
	}

	t.Run("exchange declare enabled should fail for readonly user", func(t *testing.T) {
		d, err := amqpjobs.FromConfig(
			nil,
			readOnlyConfigKey,
			zap.NewNop(),
			newReadOnlyConfigConfigurer(true, true, readOnlyQueue),
			pipe,
			&readOnlyMockQueue{},
		)
		require.Error(t, err)
		require.Nil(t, d)
		assertPermissionErrorContains(t, err, "exchange")
	})

	t.Run("queue declare checks enabled should fail on state", func(t *testing.T) {
		d, err := amqpjobs.FromConfig(
			nil,
			readOnlyConfigKey,
			zap.NewNop(),
			newReadOnlyConfigConfigurer(false, true, readOnlyBadQueue),
			pipe,
			&readOnlyMockQueue{},
		)
		require.NoError(t, err)
		require.NotNil(t, d)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err = d.State(ctx)
		require.Error(t, err)
		assertQueueCheckErrorContains(t, err)

		require.NoError(t, d.Stop(ctx))
	})

	t.Run("exchange and queue declare disabled should pass", func(t *testing.T) {
		d, err := amqpjobs.FromConfig(
			nil,
			readOnlyConfigKey,
			zap.NewNop(),
			newReadOnlyConfigConfigurer(false, false, readOnlyQueue),
			pipe,
			&readOnlyMockQueue{},
		)
		require.NoError(t, err)
		require.NotNil(t, d)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		st, err := d.State(ctx)
		require.NoError(t, err)
		require.NotNil(t, st)
		assert.Equal(t, readOnlyQueue, st.Queue)
		assert.Equal(t, pipe.Name(), st.Pipeline)
		assert.Equal(t, "amqp", st.Driver)
		assert.False(t, st.Ready)

		require.NoError(t, d.Stop(ctx))
	})
}

func TestReadOnlyFromPipelineDeclarations(t *testing.T) {
	cfg := &readOnlyMockConfigurer{
		has: map[string]bool{
			"amqp": true,
		},
		values: map[string]any{
			"amqp": map[string]any{
				"addr": readOnlyAMQPAddr,
			},
		},
	}

	t.Run("exchange declare enabled should fail for readonly user", func(t *testing.T) {
		pipeName := fmt.Sprintf("readonly-pipe-ex-%s", uuid.NewString())
		d, err := amqpjobs.FromPipeline(
			nil,
			newReadOnlyPipeline(pipeName, true, true, readOnlyQueue),
			zap.NewNop(),
			cfg,
			&readOnlyMockQueue{},
		)
		require.Error(t, err)
		require.Nil(t, d)
		assertPermissionErrorContains(t, err, "exchange")
	})

	t.Run("queue declare checks enabled should fail on state", func(t *testing.T) {
		pipeName := fmt.Sprintf("readonly-pipe-q-%s", uuid.NewString())
		d, err := amqpjobs.FromPipeline(
			nil,
			newReadOnlyPipeline(pipeName, false, true, readOnlyBadQueue),
			zap.NewNop(),
			cfg,
			&readOnlyMockQueue{},
		)
		require.NoError(t, err)
		require.NotNil(t, d)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err = d.State(ctx)
		require.Error(t, err)
		assertQueueCheckErrorContains(t, err)

		require.NoError(t, d.Stop(ctx))
	})

	t.Run("exchange and queue declare disabled should pass", func(t *testing.T) {
		pipeName := fmt.Sprintf("readonly-pipe-ok-%s", uuid.NewString())
		d, err := amqpjobs.FromPipeline(
			nil,
			newReadOnlyPipeline(pipeName, false, false, readOnlyQueue),
			zap.NewNop(),
			cfg,
			&readOnlyMockQueue{},
		)
		require.NoError(t, err)
		require.NotNil(t, d)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		st, err := d.State(ctx)
		require.NoError(t, err)
		require.NotNil(t, st)
		assert.Equal(t, readOnlyQueue, st.Queue)
		assert.Equal(t, pipeName, st.Pipeline)
		assert.Equal(t, "amqp", st.Driver)
		assert.False(t, st.Ready)

		require.NoError(t, d.Stop(ctx))
	})
}

func newReadOnlyPipeline(name string, exchangeDeclareEnabled, queueDeclareEnabled bool, queueName string) jobs.Pipeline {
	return jobs.Pipeline{
		"name":             name,
		"driver":           "amqp",
		"exchange":         readOnlyExchange,
		"exchange_type":    "fanout",
		"exchange_durable": "true",
		"queue":            queueName,
		"routing_key":      readOnlyRoute,
		"durable":          "true",
		"exchange_declare": fmt.Sprintf("%t", exchangeDeclareEnabled),
		"queue_declare":    fmt.Sprintf("%t", queueDeclareEnabled),
	}
}

func newReadOnlyConfigConfigurer(exchangeDeclareEnabled, queueDeclareEnabled bool, queueName string) *readOnlyMockConfigurer {
	return &readOnlyMockConfigurer{
		has: map[string]bool{
			readOnlyConfigKey: true,
			"amqp":            true,
		},
		values: map[string]any{
			readOnlyConfigKey: map[string]any{
				"version": 2,
				"exchange": map[string]any{
					"name":        readOnlyExchange,
					"type":        "fanout",
					"durable":     true,
					"auto_delete": false,
					"declare":     exchangeDeclareEnabled,
				},
				"queue": map[string]any{
					"name":        queueName,
					"routing_key": readOnlyRoute,
					"durable":     true,
					"auto_delete": false,
					"exclusive":   false,
					"declare":     queueDeclareEnabled,
				},
			},
			"amqp": map[string]any{
				"addr": readOnlyAMQPAddr,
			},
		},
	}
}

func assertPermissionErrorContains(t *testing.T, err error, target string) {
	t.Helper()
	require.Error(t, err)

	msg := strings.ToLower(err.Error())
	assert.Contains(t, msg, strings.ToLower(target))
	assert.True(
		t,
		strings.Contains(msg, "access_refused") ||
			strings.Contains(msg, "access refused") ||
			strings.Contains(msg, "configure access"),
		"expected permission-related error, got: %s",
		err.Error(),
	)
}

func assertQueueCheckErrorContains(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)

	msg := strings.ToLower(err.Error())
	assert.Contains(t, msg, "queue")
	assert.True(
		t,
		strings.Contains(msg, "precondition_failed") ||
			strings.Contains(msg, "inequivalent arg") ||
			strings.Contains(msg, "not_found") ||
			strings.Contains(msg, "no queue") ||
			strings.Contains(msg, "access_refused") ||
			strings.Contains(msg, "access refused"),
		"expected queue check failure, got: %s",
		err.Error(),
	)
}
