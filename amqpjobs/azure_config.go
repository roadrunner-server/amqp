package amqpjobs

import (
	"fmt"

	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/roadrunner-server/errors"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
)

// FromAzureConfig initializes Azure AMQP driver from configuration
func FromAzureConfig(tracer *sdktrace.TracerProvider, configKey string, log *zap.Logger, cfg Configurer, pipeline jobs.Pipeline, pq jobs.Queue) (*AzureDriver, error) {
	const op = errors.Op("azure_amqp_from_config")

	if !cfg.Has(configKey) {
		return nil, errors.E(op, errors.Errorf("no configuration section %s found", configKey))
	}

	return NewAzureDriver(tracer, log, cfg, pipeline, pq)
}

// FromAzurePipeline initializes Azure AMQP driver from pipeline configuration
func FromAzurePipeline(tracer *sdktrace.TracerProvider, pipeline jobs.Pipeline, log *zap.Logger, cfg Configurer, pq jobs.Queue) (*AzureDriver, error) {
	const op = errors.Op("azure_amqp_from_pipeline")

	// Create a wrapper config that reads from pipeline
	pipelineConfig := &pipelineConfigWrapper{
		pipeline: pipeline,
		cfg:      cfg,
	}

	return NewAzureDriver(tracer, log, pipelineConfig, pipeline, pq)
}

// pipelineConfigWrapper wraps pipeline configuration for the driver
type pipelineConfigWrapper struct {
	pipeline jobs.Pipeline
	cfg      Configurer
}

func (p *pipelineConfigWrapper) UnmarshalKey(name string, out any) error {
	if name != "amqp" {
		return p.cfg.UnmarshalKey(name, out)
	}

	// Extract Azure AMQP specific configuration from pipeline
	config := out.(*azureConfig)
	
	// Required fields
	config.ConnectionString = p.pipeline.String("connection_string", "amqp://localhost:5672")
	config.Address = p.pipeline.String("address", "jobs")
	
	// Optional fields with defaults
	config.Prefetch = uint32(p.pipeline.Int("prefetch", 10))
	config.Priority = p.pipeline.Priority()
	config.AutoAck = p.pipeline.Bool("auto_ack", false)
	config.RequeueOnFail = p.pipeline.Bool("requeue_on_fail", true)

	return nil
}

func (p *pipelineConfigWrapper) Has(name string) bool {
	if name == "amqp" {
		return true
	}
	return p.cfg.Has(name)
}

// AzureDriverConfig represents the configuration structure for Azure AMQP driver
type AzureDriverConfig struct {
	// Connection string for the AMQP 1.0 broker
	ConnectionString string `mapstructure:"connection_string" json:"connection_string"`
	
	// Address is the AMQP 1.0 address (replaces queue/exchange concept)
	Address string `mapstructure:"address" json:"address"`
	
	// Number of messages to prefetch
	Prefetch uint32 `mapstructure:"prefetch" json:"prefetch"`
	
	// Message priority
	Priority int64 `mapstructure:"priority" json:"priority"`
	
	// Auto-acknowledge messages
	AutoAck bool `mapstructure:"auto_ack" json:"auto_ack"`
	
	// Requeue messages on failure
	RequeueOnFail bool `mapstructure:"requeue_on_fail" json:"requeue_on_fail"`
}

// Validate validates the Azure driver configuration
func (c *AzureDriverConfig) Validate() error {
	if c.ConnectionString == "" {
		return fmt.Errorf("connection_string is required")
	}
	if c.Address == "" {
		return fmt.Errorf("address is required")
	}
	if c.Prefetch == 0 {
		c.Prefetch = 10 // Default prefetch
	}
	return nil
}
