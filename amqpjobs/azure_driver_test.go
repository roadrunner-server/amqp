package amqpjobs

import (
	"testing"

	"github.com/Azure/go-amqp"
	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap/zaptest"
)

// MockPipeline implements jobs.Pipeline for testing
type MockPipeline struct {
	mock.Mock
}

func (m *MockPipeline) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockPipeline) Driver() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockPipeline) Has(name string) bool {
	args := m.Called(name)
	return args.Bool(0)
}

func (m *MockPipeline) String(name string, def string) string {
	args := m.Called(name, def)
	return args.String(0)
}

func (m *MockPipeline) Bool(name string, def bool) bool {
	args := m.Called(name, def)
	return args.Bool(0)
}

func (m *MockPipeline) Int(name string, def int) int {
	args := m.Called(name, def)
	return args.Int(0)
}

func (m *MockPipeline) Priority() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

// MockQueue implements jobs.Queue for testing
type MockQueue struct {
	mock.Mock
}

func (m *MockQueue) Insert(job jobs.Job) error {
	args := m.Called(job)
	return args.Error(0)
}

func (m *MockQueue) Remove(pipeline string) error {
	args := m.Called(pipeline)
	return args.Error(0)
}

func (m *MockQueue) Len() uint64 {
	args := m.Called()
	return args.Get(0).(uint64)
}

// MockConfigurer implements Configurer for testing
type MockConfigurer struct {
	mock.Mock
}

func (m *MockConfigurer) UnmarshalKey(name string, out any) error {
	args := m.Called(name, out)
	
	// Set default values for azureConfig
	if name == "amqp" {
		if config, ok := out.(*azureConfig); ok {
			config.ConnectionString = "amqp://localhost:5672"
			config.Address = "test-queue"
			config.Prefetch = 10
			config.Priority = 5
			config.AutoAck = false
			config.RequeueOnFail = true
		}
	}
	
	return args.Error(0)
}

func (m *MockConfigurer) Has(name string) bool {
	args := m.Called(name)
	return args.Bool(0)
}

func TestAzureDriverConfiguration(t *testing.T) {
	logger := zaptest.NewLogger(t)
	tracer := sdktrace.NewTracerProvider()
	
	mockPipeline := &MockPipeline{}
	mockPipeline.On("Name").Return("test-pipeline")
	mockPipeline.On("Driver").Return("azure-amqp")
	mockPipeline.On("Priority").Return(int64(5))
	
	mockQueue := &MockQueue{}
	mockConfigurer := &MockConfigurer{}
	mockConfigurer.On("Has", "amqp").Return(true)
	mockConfigurer.On("UnmarshalKey", "amqp", mock.AnythingOfType("*amqpjobs.azureConfig")).Return(nil)
	
	// Note: This test would require a running AMQP 1.0 broker
	// For unit testing, we'd need to mock the AMQP connection
	t.Skip("Integration test - requires AMQP 1.0 broker")
	
	driver, err := NewAzureDriver(tracer, logger, mockConfigurer, mockPipeline, mockQueue)
	require.NoError(t, err)
	require.NotNil(t, driver)
	
	assert.Equal(t, "test-queue", driver.address)
	assert.Equal(t, uint32(10), driver.prefetch)
	assert.Equal(t, int64(5), driver.priority)
}

func TestJobToAMQPMessageConversion(t *testing.T) {
	logger := zaptest.NewLogger(t)
	tracer := sdktrace.NewTracerProvider()
	
	mockPipeline := &MockPipeline{}
	mockPipeline.On("Name").Return("test-pipeline")
	mockPipeline.On("Driver").Return("azure-amqp")
	mockPipeline.On("Priority").Return(int64(5))
	
	mockQueue := &MockQueue{}
	mockConfigurer := &MockConfigurer{}
	mockConfigurer.On("Has", "amqp").Return(true)
	mockConfigurer.On("UnmarshalKey", "amqp", mock.AnythingOfType("*amqpjobs.azureConfig")).Return(nil)
	
	t.Skip("Integration test - requires AMQP 1.0 broker")
	
	driver, err := NewAzureDriver(tracer, logger, mockConfigurer, mockPipeline, mockQueue)
	require.NoError(t, err)
	
	// Create a test job
	testJob := &MockJob{
		id:      "test-job-123",
		groupID: "test-pipeline",
		body:    []byte(`{"task": "test", "data": "value"}`),
		headers: map[string][]string{
			"Content-Type": {"application/json"},
			"Priority":     {"high"},
		},
		priority: 10,
	}
	
	// Convert to AMQP message
	msg, err := driver.jobToAMQPMessage(testJob)
	require.NoError(t, err)
	require.NotNil(t, msg)
	
	// Verify message structure
	assert.Equal(t, testJob.body, msg.Data[0])
	assert.Equal(t, testJob.id, msg.Properties.MessageID)
	assert.Equal(t, "application/json", msg.ApplicationProperties["Content-Type"])
	assert.Equal(t, "high", msg.ApplicationProperties["Priority"])
	assert.Equal(t, testJob.groupID, msg.ApplicationProperties["job_name"])
	assert.Equal(t, testJob.priority, msg.ApplicationProperties["priority"])
}

func TestAMQPMessageToJobConversion(t *testing.T) {
	logger := zaptest.NewLogger(t)
	tracer := sdktrace.NewTracerProvider()
	
	mockPipeline := &MockPipeline{}
	mockPipeline.On("Name").Return("test-pipeline")
	mockPipeline.On("Driver").Return("azure-amqp")
	mockPipeline.On("Priority").Return(int64(5))
	
	mockQueue := &MockQueue{}
	mockConfigurer := &MockConfigurer{}
	mockConfigurer.On("Has", "amqp").Return(true)
	mockConfigurer.On("UnmarshalKey", "amqp", mock.AnythingOfType("*amqpjobs.azureConfig")).Return(nil)
	
	t.Skip("Integration test - requires AMQP 1.0 broker")
	
	driver, err := NewAzureDriver(tracer, logger, mockConfigurer, mockPipeline, mockQueue)
	require.NoError(t, err)
	
	// Create a test AMQP message
	msg := &amqp.Message{
		Data: [][]byte{[]byte(`{"task": "test", "data": "value"}`)},
		Properties: &amqp.MessageProperties{
			MessageID: "test-message-456",
		},
		ApplicationProperties: map[string]any{
			"job_name":     "test-job",
			"priority":     int64(8),
			"Content-Type": "application/json",
		},
	}
	
	// Convert to job
	job, err := driver.amqpMessageToJob(msg)
	require.NoError(t, err)
	require.NotNil(t, job)
	
	// Verify job structure
	assert.Equal(t, "test-message-456", job.ID())
	assert.Equal(t, "test-job", job.Job)
	assert.Equal(t, []byte(`{"task": "test", "data": "value"}`), job.Body())
	assert.Equal(t, int64(8), job.Options.Priority)
	assert.Equal(t, "test-pipeline", job.Options.Pipeline)
	assert.Equal(t, []string{"application/json"}, job.headers["Content-Type"])
}

// MockJob implements jobs.Message for testing
type MockJob struct {
	id       string
	groupID  string
	body     []byte
	headers  map[string][]string
	priority int64
}

func (m *MockJob) ID() string {
	return m.id
}

func (m *MockJob) GroupID() string {
	return m.groupID
}

func (m *MockJob) Body() []byte {
	return m.body
}

func (m *MockJob) Headers() map[string][]string {
	return m.headers
}

func (m *MockJob) Priority() int64 {
	return m.priority
}

func TestAzureDriverConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      AzureDriverConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			config: AzureDriverConfig{
				ConnectionString: "amqp://localhost:5672",
				Address:         "test-queue",
				Prefetch:        10,
			},
			expectError: false,
		},
		{
			name: "missing connection string",
			config: AzureDriverConfig{
				Address:  "test-queue",
				Prefetch: 10,
			},
			expectError: true,
			errorMsg:    "connection_string is required",
		},
		{
			name: "missing address",
			config: AzureDriverConfig{
				ConnectionString: "amqp://localhost:5672",
				Prefetch:        10,
			},
			expectError: true,
			errorMsg:    "address is required",
		},
		{
			name: "zero prefetch gets default",
			config: AzureDriverConfig{
				ConnectionString: "amqp://localhost:5672",
				Address:         "test-queue",
				Prefetch:        0, // Should get default value
			},
			expectError: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
				if tt.config.Prefetch == 0 {
					// Check that default was applied
					assert.Equal(t, uint32(10), tt.config.Prefetch)
				}
			}
		})
	}
}
