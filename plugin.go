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

const (
	pluginName      string = "amqp"       // Original AMQP 0.9.1 driver
	azurePluginName string = "azure-amqp" // New Azure AMQP 1.0 driver
)

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

// AzurePlugin represents the Azure AMQP 1.0 plugin
type AzurePlugin struct {
	cfg    Configurer
	log    *zap.Logger
	tracer *sdktrace.TracerProvider
}

// Init initializes the Azure AMQP plugin
func (ap *AzurePlugin) Init(cfg Configurer, log Logger) error {
	const op = errors.Op("azure_amqp_plugin_init")

	if !cfg.Has(azurePluginName) {
		return errors.E(op, errors.Disabled)
	}

	ap.cfg = cfg
	ap.log = log.NamedLogger(azurePluginName)

	return nil
}

// Name returns the Azure plugin name
func (ap *AzurePlugin) Name() string {
	return azurePluginName
}

// Collects dependencies for the Azure plugin
func (ap *AzurePlugin) Collects() []*dep.In {
	return []*dep.In{
		dep.Fits(func(pp any) {
			ap.tracer = pp.(Tracer).Tracer()
		}, (*Tracer)(nil)),
	}
}

// DriverFromConfig constructs Azure AMQP driver from configuration
func (ap *AzurePlugin) DriverFromConfig(configKey string, pq jobs.Queue, pipeline jobs.Pipeline) (jobs.Driver, error) {
	return amqpjobs.FromAzureConfig(ap.tracer, configKey, ap.log, ap.cfg, pipeline, pq)
}

// DriverFromPipeline constructs Azure AMQP driver from pipeline
func (ap *AzurePlugin) DriverFromPipeline(pipe jobs.Pipeline, pq jobs.Queue) (jobs.Driver, error) {
	return amqpjobs.FromAzurePipeline(ap.tracer, pipe, ap.log, ap.cfg, pq)
}
