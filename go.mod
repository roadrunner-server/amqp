module github.com/roadrunner-server/amqp/v5

go 1.25

toolchain go1.25.1

require (
	github.com/cenkalti/backoff/v4 v4.3.0
	github.com/google/uuid v1.6.0
	github.com/rabbitmq/amqp091-go v1.10.0
	github.com/roadrunner-server/api/v4 v4.22.1
	github.com/roadrunner-server/endure/v2 v2.6.2
	github.com/roadrunner-server/errors v1.4.1
	github.com/roadrunner-server/events v1.0.1
	github.com/stretchr/testify v1.11.1
	go.opentelemetry.io/contrib/propagators/jaeger v1.38.0
	go.opentelemetry.io/otel v1.38.0
	go.opentelemetry.io/otel/sdk v1.38.0
	go.opentelemetry.io/otel/trace v1.38.0
	go.uber.org/zap v1.27.0
	golang.org/x/sys v0.36.0
	google.golang.org/genproto v0.0.0-20250908214217-97024824d090
)

exclude (
	github.com/spf13/viper v1.18.0
	github.com/spf13/viper v1.18.1
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/metric v1.38.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	google.golang.org/protobuf v1.36.9 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
