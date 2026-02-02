// Package amqp provides a RoadRunner plugin for AMQP (Advanced Message Queuing Protocol) integration.
// It enables RoadRunner's jobs system to use RabbitMQ or other AMQP-compatible message brokers
// as a backend for asynchronous job processing, supporting features like message persistence,
// routing, and reliable delivery.
package amqp

import (
	_ "google.golang.org/genproto/protobuf/ptype" //nolint:revive,nolintlint

	"github.com/roadrunner-server/amqp/v5/amqpjobs"

	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/roadrunner-server/endure/v2/dep"
	"github.com/roadrunner-server/errors"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
)

const pluginName string = "amqp"

type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct.
	UnmarshalKey(name string, out any) error
	// Has checks if a config section exists.
	Has(name string) bool
}

type Logger interface {
	NamedLogger(name string) *zap.Logger
}

type Tracer interface {
	Tracer() *sdktrace.TracerProvider
}

type Plugin struct {
	log    *zap.Logger
	cfg    Configurer
	tracer *sdktrace.TracerProvider
}

func (p *Plugin) Init(log Logger, cfg Configurer) error {
	if !cfg.Has(pluginName) {
		return errors.E(errors.Disabled)
	}

	p.log = log.NamedLogger(pluginName)
	p.cfg = cfg
	return nil
}

func (p *Plugin) Name() string {
	return pluginName
}

func (p *Plugin) Collects() []*dep.In {
	return []*dep.In{
		dep.Fits(func(pp any) {
			p.tracer = pp.(Tracer).Tracer()
		}, (*Tracer)(nil)),
	}
}

// DriverFromConfig constructs amqp driver from the .rr.yaml configuration
func (p *Plugin) DriverFromConfig(configKey string, pq jobs.Queue, pipeline jobs.Pipeline) (jobs.Driver, error) {
	return amqpjobs.FromConfig(p.tracer, configKey, p.log, p.cfg, pipeline, pq)
}

// DriverFromPipeline constructs amqp driver from pipeline
func (p *Plugin) DriverFromPipeline(pipe jobs.Pipeline, pq jobs.Queue) (jobs.Driver, error) {
	return amqpjobs.FromPipeline(p.tracer, pipe, p.log, p.cfg, pq)
}
