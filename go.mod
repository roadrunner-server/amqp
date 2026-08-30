module github.com/roadrunner-server/amqp/v6

go 1.27

toolchain go1.27.0

require (
	github.com/cenkalti/backoff/v4 v4.3.0
	github.com/google/uuid v1.6.0
	github.com/rabbitmq/amqp091-go v1.14.0
	github.com/roadrunner-server/api-plugins/v6 v6.0.0-beta.2
	github.com/roadrunner-server/endure/v2 v2.6.2
	github.com/roadrunner-server/errors v1.5.0
	github.com/roadrunner-server/events v1.0.1
	github.com/stretchr/testify v1.12.1
	go.opentelemetry.io/contrib/propagators/jaeger v1.46.0
	go.opentelemetry.io/otel v1.46.0
	go.opentelemetry.io/otel/sdk v1.46.0
	go.opentelemetry.io/otel/trace v1.46.0
	google.golang.org/genproto v0.0.0-20260825221802-da73d73af1c5
)

exclude (
	github.com/spf13/viper v1.18.0
	github.com/spf13/viper v1.18.1
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/metric v1.46.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
