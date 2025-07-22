package amqpjobs

import (
	"context"

	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/roadrunner-server/errors"
)

// AzureAMQPPlugin implements the Azure AMQP 1.0 plugin
type AzureAMQPPlugin struct{}

// Name returns the name of the Azure AMQP plugin
func (p *AzureAMQPPlugin) Name() string {
	return "azure-amqp"
}

// DriverFromConfig creates a new Azure AMQP driver from configuration
func (p *AzureAMQPPlugin) DriverFromConfig(configKey string, pq jobs.Queue, pipeline jobs.Pipeline) (jobs.Driver, error) {
	// This method would normally be called by the framework
	// For now, it's a placeholder - the actual construction happens through FromAzureConfig
	return nil, errors.Errorf("DriverFromConfig not implemented for Azure AMQP plugin")
}

// Pause implementation placeholder for jobs.Driver interface
func (p *AzureAMQPPlugin) Pause(_ context.Context, _ string) error {
	return errors.Errorf("pause not supported for Azure AMQP plugin")
}

// Resume implementation placeholder for jobs.Driver interface  
func (p *AzureAMQPPlugin) Resume(_ context.Context, _ string) error {
	return errors.Errorf("resume not supported for Azure AMQP plugin")
}

// State implementation placeholder for jobs.Driver interface
func (p *AzureAMQPPlugin) State(_ context.Context) (*jobs.State, error) {
	return nil, errors.Errorf("state not supported for Azure AMQP plugin")
}

// Push implementation placeholder for jobs.Driver interface
func (p *AzureAMQPPlugin) Push(_ context.Context, _ jobs.Message) error {
	return errors.Errorf("push not supported for Azure AMQP plugin")
}

// Run implementation placeholder for jobs.Driver interface
func (p *AzureAMQPPlugin) Run(_ context.Context, _ jobs.Pipeline) error {
	return errors.Errorf("run not supported for Azure AMQP plugin")
}

// Stop implementation placeholder for jobs.Driver interface
func (p *AzureAMQPPlugin) Stop(_ context.Context) error {
	return errors.Errorf("stop not supported for Azure AMQP plugin")
}

// DriverFromPipeline creates a new Azure AMQP driver from pipeline
func (p *AzureAMQPPlugin) DriverFromPipeline(pipeline jobs.Pipeline, pq jobs.Queue) (jobs.Driver, error) {
	const op = errors.Op("azure_amqp_plugin_driver_from_pipeline")
	
	// This method would normally be called by the framework  
	// For now, it's a placeholder - the actual construction happens through FromAzurePipeline
	return nil, errors.E(op, errors.Errorf("DriverFromPipeline not implemented for Azure AMQP plugin"))
}

// RegisterAzureAMQPPlugin registers the Azure AMQP plugin with the job system
func RegisterAzureAMQPPlugin() jobs.Constructor {
	return &AzureAMQPPlugin{}
}
