package amqp

import (
	"context"
	"fmt"
	"os"
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
	"gopkg.in/yaml.v3"
)

const (
	readOnlyConfigKey = "jobs.pipelines.readonly.config"
	readOnlyAMQPAddr  = "amqp://readonly:readonly@127.0.0.1:5675/TEST"

	readOnlyExchange = "CARGO_OUT"
	readOnlyQueue    = "CARGO_ECN_EVENTS"
	readOnlyBadQueue = "CARGO_ECN_EVENTS_MISSING"
	readOnlyRoute    = "CARGO_ECN_EVENTS"

	readOnlyFromFileExchangeDeclareOn = "configs/.rr-amqp-readonly-v2-exchange-declare-on.yaml"
	readOnlyFromFileQueueCheckOn      = "configs/.rr-amqp-readonly-v2-queue-check-on.yaml"
	readOnlyFromFileDeclareOff        = "configs/.rr-amqp-readonly-v2-declare-off.yaml"
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

type readOnlyYAMLConfigurer struct {
	values map[string]any
}

func newReadOnlyYAMLConfigurer(path string) (*readOnlyYAMLConfigurer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	raw := make(map[string]any)
	err = yaml.Unmarshal(data, &raw)
	if err != nil {
		return nil, err
	}

	normalized, ok := normalizeYAMLValue(raw).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected yaml root type for %s", path)
	}

	return &readOnlyYAMLConfigurer{
		values: normalized,
	}, nil
}

func (m *readOnlyYAMLConfigurer) Has(name string) bool {
	_, ok := lookupConfigValue(m.values, name)
	return ok
}

func (m *readOnlyYAMLConfigurer) UnmarshalKey(name string, out any) error {
	val, ok := lookupConfigValue(m.values, name)
	if !ok {
		return nil
	}

	return mapstructure.Decode(val, out)
}

func normalizeYAMLValue(value any) any {
	switch t := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[k] = normalizeYAMLValue(v)
		}

		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			key, ok := k.(string)
			if !ok {
				key = fmt.Sprint(k)
			}
			out[key] = normalizeYAMLValue(v)
		}

		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = normalizeYAMLValue(t[i])
		}

		return out
	default:
		return value
	}
}

func lookupConfigValue(values map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")

	var cur any = values
	for _, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}

		next, ok := m[part]
		if !ok {
			return nil, false
		}

		cur = next
	}

	return cur, true
}

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

func TestReadOnlyFromConfigDeclarationsFromFileV2(t *testing.T) {
	pipe := jobs.Pipeline{
		"name":   fmt.Sprintf("readonly-static-file-%s", uuid.NewString()),
		"driver": "amqp",
	}

	t.Run("exchange declare enabled should fail for readonly user", func(t *testing.T) {
		cfg, err := newReadOnlyYAMLConfigurer(readOnlyFromFileExchangeDeclareOn)
		require.NoError(t, err)

		d, err := amqpjobs.FromConfig(
			nil,
			readOnlyConfigKey,
			zap.NewNop(),
			cfg,
			pipe,
			&readOnlyMockQueue{},
		)
		require.Error(t, err)
		require.Nil(t, d)
		assertPermissionErrorContains(t, err, "exchange")
	})

	t.Run("queue declare checks enabled should fail on state", func(t *testing.T) {
		cfg, err := newReadOnlyYAMLConfigurer(readOnlyFromFileQueueCheckOn)
		require.NoError(t, err)

		d, err := amqpjobs.FromConfig(
			nil,
			readOnlyConfigKey,
			zap.NewNop(),
			cfg,
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
		cfg, err := newReadOnlyYAMLConfigurer(readOnlyFromFileDeclareOff)
		require.NoError(t, err)

		d, err := amqpjobs.FromConfig(
			nil,
			readOnlyConfigKey,
			zap.NewNop(),
			cfg,
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
