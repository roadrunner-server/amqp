module github.com/roadrunner-server/amqp/v2

go 1.17

require (
	github.com/cenkalti/backoff/v4 v4.1.2
	github.com/goccy/go-json v0.9.4
	github.com/google/uuid v1.3.0
	github.com/rabbitmq/amqp091-go v1.3.0
	github.com/roadrunner-server/api/v2 v2.8.0-rc.5
	github.com/roadrunner-server/errors v1.1.1
	github.com/roadrunner-server/sdk/v2 v2.8.0-rc.5
	go.uber.org/zap v1.21.0
)

require (
	github.com/roadrunner-server/tcplisten v1.1.1 // indirect
	go.uber.org/atomic v1.9.0 // indirect
	go.uber.org/multierr v1.7.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)
