package amqpjobs

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/go-amqp"
	"github.com/google/uuid"
	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/events"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const (
	azureTracerName = "azure-amqp-jobs"
)

// AzureDriver implements the jobs.Driver interface using Azure's go-amqp library (AMQP 1.0)
type AzureDriver struct {
	mu       sync.RWMutex
	log      *zap.Logger
	pq       jobs.Queue
	pipeline atomic.Pointer[jobs.Pipeline]
	tracer   *sdktrace.TracerProvider
	prop     propagation.TextMapPropagator

	// Azure AMQP 1.0 components
	conn     *amqp.Conn
	session  *amqp.Session
	sender   *amqp.Sender
	receiver *amqp.Receiver

	// Configuration
	address      string // AMQP 1.0 uses addresses instead of queues/exchanges
	connString   string
	prefetch     uint32
	priority     int64
	autoAck      bool
	requeueOnFail bool

	// State management
	listeners uint32
	stopCh    chan struct{}
	stopped   uint64
	
	// Events
	eventBus *events.Bus
	id       string
}

var _ jobs.Driver = (*AzureDriver)(nil)

// NewAzureDriver creates a new Azure AMQP 1.0 driver
func NewAzureDriver(tracer *sdktrace.TracerProvider, log *zap.Logger, cfg Configurer, pipeline jobs.Pipeline, pq jobs.Queue) (*AzureDriver, error) {
	const op = errors.Op("azure_amqp_driver_new")

	// Parse configuration
	config := &azureConfig{}
	if err := cfg.UnmarshalKey("amqp", config); err != nil {
		return nil, errors.E(op, err)
	}

	eventBus, id := events.NewEventBus()

	d := &AzureDriver{
		log:           log,
		pq:            pq,
		tracer:        tracer,
		prop:          otel.GetTextMapPropagator(),
		eventBus:      eventBus,
		id:            id,
		address:       config.Address,
		connString:    config.ConnectionString,
		prefetch:      config.Prefetch,
		priority:      config.Priority,
		autoAck:       config.AutoAck,
		requeueOnFail: config.RequeueOnFail,
		stopCh:        make(chan struct{}),
	}

	// Store pipeline
	d.pipeline.Store(&pipeline)

	// Initialize connection
	if err := d.initConnection(context.Background()); err != nil {
		return nil, errors.E(op, err)
	}

	return d, nil
}

type azureConfig struct {
	ConnectionString string `mapstructure:"connection_string"`
	Address          string `mapstructure:"address"`
	Prefetch         uint32 `mapstructure:"prefetch"`
	Priority         int64  `mapstructure:"priority"`
	AutoAck          bool   `mapstructure:"auto_ack"`
	RequeueOnFail    bool   `mapstructure:"requeue_on_fail"`
}

func (d *AzureDriver) initConnection(ctx context.Context) error {
	const op = errors.Op("azure_amqp_init_connection")

	// Create connection
	conn, err := amqp.Dial(ctx, d.connString, nil)
	if err != nil {
		return errors.E(op, fmt.Errorf("failed to dial AMQP broker: %w", err))
	}
	d.conn = conn

	// Create session
	session, err := d.conn.NewSession(ctx, nil)
	if err != nil {
		return errors.E(op, fmt.Errorf("failed to create AMQP session: %w", err))
	}
	d.session = session

	// Create sender for publishing messages
	var settleModeSettled amqp.SenderSettleMode = amqp.SenderSettleModeSettled
	sender, err := d.session.NewSender(ctx, d.address, &amqp.SenderOptions{
		SettlementMode: &settleModeSettled, // Auto-settle for performance
	})
	if err != nil {
		return errors.E(op, fmt.Errorf("failed to create sender: %w", err))
	}
	d.sender = sender

	return nil
}

func (d *AzureDriver) initReceiver(ctx context.Context) error {
	const op = errors.Op("azure_amqp_init_receiver")

	if d.receiver != nil {
		return nil // Already initialized
	}

	// Create receiver for consuming messages
	receiver, err := d.session.NewReceiver(ctx, d.address, &amqp.ReceiverOptions{
		Credit: int32(d.prefetch),
	})
	if err != nil {
		return errors.E(op, fmt.Errorf("failed to create receiver: %w", err))
	}
	d.receiver = receiver

	return nil
}

// Push implements jobs.Driver.Push
func (d *AzureDriver) Push(ctx context.Context, job jobs.Message) error {
	const op = errors.Op("azure_amqp_push")

	ctx, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(azureTracerName).Start(ctx, "azure_amqp_push")
	defer span.End()

	// Convert job to AMQP message
	msg, err := d.jobToAMQPMessage(job)
	if err != nil {
		return errors.E(op, fmt.Errorf("failed to connect to Azure AMQP: %w", err))
	}

	// Send message
	if err := d.sender.Send(ctx, msg, nil); err != nil {
		return errors.E(op, fmt.Errorf("failed to send message: %w", err))
	}

	d.log.Debug("message sent successfully", zap.String("job_id", job.ID()))
	return nil
}

// Run implements jobs.Driver.Run
func (d *AzureDriver) Run(ctx context.Context, p jobs.Pipeline) error {
	const op = errors.Op("azure_amqp_run")

	// Initialize receiver
	if err := d.initReceiver(ctx); err != nil {
		return errors.E(op, err)
	}

	// Start message consumption
	atomic.StoreUint32(&d.listeners, 1)
	go d.messageConsumer(ctx)

	d.log.Info("Azure AMQP driver started", zap.String("address", d.address))
	return nil
}

// State implements jobs.Driver.State
func (d *AzureDriver) State(ctx context.Context) (*jobs.State, error) {
	const op = errors.Op("azure_amqp_state")
	
	pipe := *d.pipeline.Load()
	return &jobs.State{
		Priority: uint64(pipe.Priority()),
		Pipeline: pipe.Name(),
		Driver:   pipe.Driver(),
		Queue:    d.address,
		Ready:    atomic.LoadUint32(&d.listeners) > 0,
	}, nil
}

// Pause implements jobs.Driver.Pause
func (d *AzureDriver) Pause(ctx context.Context, p string) error {
	// AMQP 1.0 doesn't have a direct pause mechanism
	// We can stop consuming by closing the receiver
	if d.receiver != nil {
		return d.receiver.Close(ctx)
	}
	return nil
}

// Resume implements jobs.Driver.Resume
func (d *AzureDriver) Resume(ctx context.Context, p string) error {
	// Recreate receiver to resume consumption
	return d.initReceiver(ctx)
}

// Stop implements jobs.Driver.Stop
func (d *AzureDriver) Stop(ctx context.Context) error {
	const op = errors.Op("azure_amqp_stop")

	atomic.StoreUint64(&d.stopped, 1)
	close(d.stopCh)

	// Close receiver
	if d.receiver != nil {
		if err := d.receiver.Close(ctx); err != nil {
			d.log.Error("failed to close receiver", zap.Error(err))
		}
	}

	// Close sender
	if d.sender != nil {
		if err := d.sender.Close(ctx); err != nil {
			d.log.Error("failed to close sender", zap.Error(err))
		}
	}

	// Close session
	if d.session != nil {
		if err := d.session.Close(ctx); err != nil {
			d.log.Error("failed to close session", zap.Error(err))
		}
	}

	// Close connection
	if d.conn != nil {
		if err := d.conn.Close(); err != nil {
			d.log.Error("failed to close connection", zap.Error(err))
		}
	}

	d.log.Info("Azure AMQP driver stopped")
	return nil
}

// messageConsumer runs the message consumption loop
func (d *AzureDriver) messageConsumer(ctx context.Context) {
	defer atomic.StoreUint32(&d.listeners, 0)

	d.log.Debug("starting message consumer")
	
	for {
		select {
		case <-ctx.Done():
			d.log.Debug("context cancelled, stopping consumer")
			return
		case <-d.stopCh:
			d.log.Debug("stop signal received, stopping consumer")
			return
		default:
			// Receive message with timeout
			receiveCtx, cancel := context.WithTimeout(ctx, time.Second*30)
			msg, err := d.receiver.Receive(receiveCtx, nil)
			cancel()

			if err != nil {
				if ctx.Err() != nil {
					return // Context cancelled
				}
				d.log.Error("failed to receive message", zap.Error(err))
				continue
			}

			// Process message
			d.processMessage(ctx, msg)
		}
	}
}

// processMessage handles a received AMQP message
func (d *AzureDriver) processMessage(ctx context.Context, msg *amqp.Message) {
	ctx, span := d.tracer.Tracer(azureTracerName).Start(ctx, "azure_amqp_process")
	defer span.End()

	// Convert AMQP message to job
	job, err := d.amqpMessageToJob(msg)
	if err != nil {
		d.log.Error("failed to convert AMQP message to job", zap.Error(err))
		// Reject message
		if rejectErr := d.receiver.RejectMessage(ctx, msg, nil); rejectErr != nil {
			d.log.Error("failed to reject message", zap.Error(rejectErr))
		}
		return
	}

	// Extract tracing context
	if job.headers != nil {
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(job.headers))
	}

	// Auto-acknowledge if configured
	if d.autoAck {
		if err := d.receiver.AcceptMessage(ctx, msg); err != nil {
			d.log.Error("failed to accept message", zap.Error(err))
		}
	}

	// Set up acknowledgment functions
	job.Options.ack = func(multiple bool) error {
		return d.receiver.AcceptMessage(ctx, msg)
	}
	job.Options.nack = func(multiple bool, requeue bool) error {
		if requeue && d.requeueOnFail {
			return d.receiver.ReleaseMessage(ctx, msg)
		}
		return d.receiver.RejectMessage(ctx, msg, nil)
	}

	// Insert job into priority queue
	d.pq.Insert(job)
}

// jobToAMQPMessage converts a RoadRunner job to an AMQP message
func (d *AzureDriver) jobToAMQPMessage(job jobs.Message) (*amqp.Message, error) {
	msg := &amqp.Message{
		Data: [][]byte{job.Payload()},
		Properties: &amqp.MessageProperties{
			MessageID: job.ID(),
		},
		ApplicationProperties: make(map[string]any),
	}

	// Add headers as application properties
	for k, v := range job.Headers() {
		if len(v) > 0 {
			msg.ApplicationProperties[k] = v[0] // Take first value
		}
	}

	// Add job metadata
	msg.ApplicationProperties["job_name"] = job.GroupID()
	msg.ApplicationProperties["priority"] = job.Priority()

	return msg, nil
}

// amqpMessageToJob converts an AMQP message to a RoadRunner job
func (d *AzureDriver) amqpMessageToJob(msg *amqp.Message) (*Item, error) {
	var payload []byte
	if len(msg.Data) > 0 {
		payload = msg.Data[0]
	}

	// Extract job ID
	jobID := uuid.NewString()
	if msg.Properties != nil && msg.Properties.MessageID != nil {
		if id, ok := msg.Properties.MessageID.(string); ok {
			jobID = id
		}
	}

	// Extract headers from application properties
	headers := make(map[string][]string)
	for k, v := range msg.ApplicationProperties {
		if str, ok := v.(string); ok {
			headers[k] = []string{str}
		}
	}

	// Extract job name and priority
	jobName := "default"
	if name, ok := msg.ApplicationProperties["job_name"]; ok {
		if str, ok := name.(string); ok {
			jobName = str
		}
	}

	priority := d.priority
	if p, ok := msg.ApplicationProperties["priority"]; ok {
		if i64, ok := p.(int64); ok {
			priority = i64
		}
	}

	pipe := *d.pipeline.Load()
	
	return &Item{
		Job:     jobName,
		Ident:   jobID,
		Payload: payload,
		headers: headers,
		Options: &Options{
			Priority: priority,
			Pipeline: pipe.Name(),
			AutoAck:  d.autoAck,
		},
	}, nil
}
