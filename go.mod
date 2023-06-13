module github.com/roadrunner-server/amqp/v4

go 1.20

require (
	github.com/cenkalti/backoff/v4 v4.2.1
	github.com/goccy/go-json v0.10.2
	github.com/google/uuid v1.3.0
	github.com/rabbitmq/amqp091-go v1.8.1
	github.com/roadrunner-server/api/v4 v4.3.2
	github.com/roadrunner-server/endure/v2 v2.2.1
	github.com/roadrunner-server/errors v1.2.0
	github.com/stretchr/testify v1.8.4
	go.opentelemetry.io/contrib/propagators/jaeger v1.17.0
	go.opentelemetry.io/otel v1.16.0
	go.opentelemetry.io/otel/sdk v1.16.0
	go.opentelemetry.io/otel/trace v1.16.0
	go.uber.org/zap v1.24.0
	golang.org/x/sys v0.9.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-logr/logr v1.2.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	go.opentelemetry.io/otel/metric v1.16.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
