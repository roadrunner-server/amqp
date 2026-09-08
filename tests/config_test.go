package tests

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"testing"

	"github.com/roadrunner-server/amqp/v6/amqpjobs"
	"github.com/roadrunner-server/config/v6"
	"github.com/roadrunner-server/jobs/v6"
	"github.com/stretchr/testify/require"
)

func TestConnectionConfigErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		addr    string
		static  map[string]any
		runtime map[string]string
		want    string
	}{
		{
			name: "missing selector",
			addr: "amqp://127.0.0.1:1/",
			want: "connection is required",
		},
		{
			name: "unknown selector takes precedence over queue headers",
			addr: "amqp://127.0.0.1:1/",
			static: map[string]any{
				"connection": "unknown",
				"queue":      map[string]any{"headers": map[string]any{"rr_connection": "rabbitmq"}},
			},
			runtime: map[string]string{
				"connection":    "unknown",
				"queue_headers": `{"rr_connection":"rabbitmq"}`,
			},
			want: `unknown AMQP connection "unknown"`,
		},
		{
			name: "empty selector takes precedence over queue headers",
			addr: "amqp://127.0.0.1:1/",
			static: map[string]any{
				"connection": "",
				"queue":      map[string]any{"headers": map[string]any{"rr_connection": "rabbitmq"}},
			},
			runtime: map[string]string{
				"connection":    "",
				"queue_headers": `{"rr_connection":"rabbitmq"}`,
			},
			want: "connection is required",
		},
		{
			name: "missing address cannot use pipeline or global address",
			static: map[string]any{
				"connection": "rabbitmq",
				"addr":       "amqp://127.0.0.1:1/",
			},
			runtime: map[string]string{
				"connection": "rabbitmq",
				"addr":       "amqp://127.0.0.1:1/",
			},
			want: `addr is required for AMQP connection "rabbitmq"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			static := jobs.Pipeline{
				"name":     "connection-test",
				"driver":   "amqp",
				"exchange": map[string]any{"type": "fanout"},
				"queue":    map[string]any{"name": "connection-test"},
			}
			maps.Copy(static, tc.static)

			pipeline := jobs.Pipeline{
				"name":          "connection-test",
				"driver":        "amqp",
				"exchange_type": "fanout",
				"queue":         "connection-test",
			}
			for key, value := range tc.runtime {
				pipeline[key] = value
			}

			data, err := json.Marshal(map[string]any{
				"version": "3",
				"amqp": map[string]any{
					"addr":     "amqp://127.0.0.1:1/",
					"rabbitmq": map[string]any{"addr": tc.addr},
				},
				"pipeline": static,
			})
			require.NoError(t, err)
			cfg := &config.Plugin{Type: "yaml", ReadInCfg: data}
			require.NoError(t, cfg.Init())

			for _, source := range []string{"config", "pipeline"} {
				t.Run(source, func(t *testing.T) {
					var driver *amqpjobs.Driver
					var err error
					if source == "config" {
						driver, err = amqpjobs.FromConfig(t.Context(), nil, "pipeline", slog.Default(), cfg, static, nil)
					} else {
						driver, err = amqpjobs.FromPipeline(t.Context(), nil, pipeline, slog.Default(), cfg, nil)
					}
					if driver != nil {
						t.Cleanup(func() { _ = driver.Stop(context.Background()) })
					}
					require.ErrorContains(t, err, tc.want)
					require.Nil(t, driver)
				})
			}
		})
	}
}

func TestConfigRejectsScalarEntities(t *testing.T) {
	pipeline := jobs.Pipeline{
		"name":          "scalar-test",
		"driver":        "amqp",
		"connection":    "rabbitmq",
		"exchange":      "test-exchange",
		"exchange_type": "fanout",
		"queue":         "test-queue",
	}
	data, err := json.Marshal(map[string]any{
		"version": "3",
		"amqp": map[string]any{
			"rabbitmq": map[string]any{"addr": "amqp://127.0.0.1:1/"},
		},
		"pipeline": pipeline,
	})
	require.NoError(t, err)
	cfg := &config.Plugin{Type: "yaml", ReadInCfg: data}
	require.NoError(t, cfg.Init())

	driver, err := amqpjobs.FromConfig(t.Context(), nil, "pipeline", slog.Default(), cfg, pipeline, nil)
	if driver != nil {
		t.Cleanup(func() { _ = driver.Stop(context.Background()) })
	}
	require.ErrorContains(t, err, `'exchange' expected a map or struct, got "string"`)
	require.ErrorContains(t, err, `'queue' expected a map or struct, got "string"`)
	require.Nil(t, driver)
}
