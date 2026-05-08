// Package amqpjobs implements the AMQP driver for RoadRunner's jobs plugin.
// It provides job queue operations including message publishing, consuming, acknowledgment,
// and automatic reconnection handling for RabbitMQ and other AMQP 0-9-1 compatible brokers.
package amqpjobs

import (
	"context"
	"crypto/tls"
	"encoding/json"
	stderr "errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/roadrunner-server/api-plugins/v6/jobs"
	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/events"
	jprop "go.opentelemetry.io/contrib/propagators/jaeger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	xRoutingKey        = "x-routing-key"
	pluginName  string = "amqp"
	tracerName  string = "jobs"
)

var _ jobs.Driver = (*Driver)(nil)

type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct.
	UnmarshalKey(name string, out any) error
	// Has checks if a config section exists.
	Has(name string) bool
}

type Driver struct {
	mu       sync.RWMutex
	log      *slog.Logger
	pq       jobs.Queue
	pipeline atomic.Pointer[jobs.Pipeline]
	tracer   *sdktrace.TracerProvider
	prop     propagation.TextMapPropagator
	config   atomic.Pointer[config]

	// events
	eventBus *events.Bus
	id       string

	// amqp connection notifiers
	notifyCloseConnCh    chan *amqp.Error
	notifyClosePubCh     chan *amqp.Error
	notifyCloseConsumeCh chan *amqp.Error
	notifyCloseStatCh    chan *amqp.Error
	redialCh             chan *redialMsg

	conn        *amqp.Connection
	consumeChan *amqp.Channel
	stateChan   chan *amqp.Channel
	publishChan chan *amqp.Channel

	listeners atomic.Uint32
	delayed   atomic.Int64
	stopCh    chan struct{}
	stopped   atomic.Uint64
}

// FromConfig initializes AMQP pipeline
func FromConfig(_ context.Context, tracer *sdktrace.TracerProvider, configKey string, log *slog.Logger, cfg Configurer, pipeline jobs.Pipeline, pq jobs.Queue) (*Driver, error) {
	const op = errors.Op("new_amqp_consumer")

	if tracer == nil {
		tracer = sdktrace.NewTracerProvider()
	}

	prop := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}, jprop.Jaeger{})
	otel.SetTextMapPropagator(prop)
	// we need to get two parts of the amqp information here.
	// first part - address to connect, it is located in the global section under the amqp pluginName
	// second part - queues and other pipeline information
	// if no such key - error
	if !cfg.Has(configKey) {
		return nil, errors.E(op, errors.Errorf("no configuration by provided key: %s", configKey))
	}

	// if no global section
	if !cfg.Has(pluginName) {
		return nil, errors.E(op, errors.Str("no global amqp configuration, global configuration should contain amqp addrs"))
	}

	// PARSE CONFIGURATION START -------
	var conf config
	err := cfg.UnmarshalKey(configKey, &conf)
	if err != nil {
		return nil, errors.E(op, err)
	}

	switch conf.Version {
	case 0, 1:
		conf.V1Config = &v1config{}
		err = cfg.UnmarshalKey(configKey, conf.V1Config)
		if err != nil {
			return nil, errors.E(op, err)
		}
	case 2:
		conf.V2Config = &v2config{}
		err = cfg.UnmarshalKey(configKey, conf.V2Config)
		if err != nil {
			return nil, errors.E(op, err)
		}
	default:
		return nil, errors.E(op, errors.Errorf("unsupported AMQP pipeline config version: %d", conf.Version))
	}

	// global amqp section holds addr/tls and must be applied before defaults.
	err = cfg.UnmarshalKey(pluginName, &conf)
	if err != nil {
		return nil, errors.E(op, err)
	}

	err = conf.InitDefault()
	if err != nil {
		return nil, err
	}
	// PARSE CONFIGURATION END -------

	eventBus, id := events.NewEventBus()

	jb := &Driver{
		tracer: tracer,
		prop:   prop,
		log:    log,
		pq:     pq,
		stopCh: make(chan struct{}, 1),

		// events
		eventBus: eventBus,
		id:       id,

		publishChan: make(chan *amqp.Channel, 1),
		stateChan:   make(chan *amqp.Channel, 1),
		redialCh:    make(chan *redialMsg, 5),

		notifyCloseConsumeCh: make(chan *amqp.Error, 1),
		notifyCloseConnCh:    make(chan *amqp.Error, 1),
		notifyCloseStatCh:    make(chan *amqp.Error, 1),
		notifyClosePubCh:     make(chan *amqp.Error, 1),
	}

	jb.conn, err = dial(conf.Addr, &conf)
	if err != nil {
		return nil, errors.E(op, err)
	}

	jb.config.Store(&conf)

	err = jb.init()
	if err != nil {
		_ = jb.conn.Close()
		return nil, errors.E(op, err)
	}

	pch, err := jb.conn.Channel()
	if err != nil {
		_ = jb.conn.Close()
		return nil, errors.E(op, err)
	}

	err = pch.Confirm(false)
	if err != nil {
		_ = pch.Close()
		_ = jb.conn.Close()
		return nil, errors.E(op, fmt.Errorf("failed to turn on publisher confirms on the channel: %w", err))
	}

	stch, err := jb.conn.Channel()
	if err != nil {
		_ = pch.Close()
		_ = jb.conn.Close()
		return nil, errors.E(op, err)
	}

	jb.pipeline.Store(&pipeline)

	jb.conn.NotifyClose(jb.notifyCloseConnCh)
	pch.NotifyClose(jb.notifyClosePubCh)
	stch.NotifyClose(jb.notifyCloseStatCh)

	jb.publishChan <- pch
	jb.stateChan <- stch

	// run redialer and requeue listener for the connection
	jb.redialer()
	jb.redialMergeCh()

	return jb, nil
}

// FromPipeline initializes consumer from pipeline
func FromPipeline(_ context.Context, tracer *sdktrace.TracerProvider, pipeline jobs.Pipeline, log *slog.Logger, cfg Configurer, pq jobs.Queue) (*Driver, error) {
	const op = errors.Op("new_amqp_consumer_from_pipeline")
	if tracer == nil {
		tracer = sdktrace.NewTracerProvider()
	}

	prop := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}, jprop.Jaeger{})
	otel.SetTextMapPropagator(prop)
	// we need to get two parts of the amqp information here.
	// first part - address to connect, it is located in the global section under the amqp pluginName
	// second part - queues and other pipeline information

	// only global section
	if !cfg.Has(pluginName) {
		return nil, errors.E(op, errors.Str("no global amqp configuration, global configuration should contain amqp addrs"))
	}

	// PARSE CONFIGURATION -------
	var conf config
	err := cfg.UnmarshalKey(pluginName, &conf)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// PARSE CONFIGURATION -------

	// parse prefetch
	prf, err := strconv.Atoi(pipeline.String(prefetch, "10"))
	if err != nil {
		log.Error("prefetch parse, driver will use default (10) prefetch", "prefetch", pipeline.String(prefetch, "10"))
		prf = 10
	}
	conf.Prefetch = prf
	conf.Priority = int64(pipeline.Int(priority, 10))
	conf.RedialTimeout = pipeline.Int(redialTimeout, 0)

	// we always use v2 for the FromPipeline constructor
	conf.Version = 2
	conf.V2Config = &v2config{
		ExchangeConfig: &exchangeConfigV2{
			Name:       pipeline.String(exchangeKey, "amqp.default"),
			Type:       pipeline.String(exchangeType, "direct"),
			Durable:    pipeline.Bool(exchangeDurable, false),
			AutoDelete: pipeline.Bool(exchangeAutoDelete, false),
		},
		QueueConfig: &queueConfigV2{
			Name:          pipeline.String(queue, ""),
			RoutingKey:    pipeline.String(routingKey, ""),
			Durable:       pipeline.Bool(durable, false),
			AutoDelete:    pipeline.Bool(queueAutoDelete, false),
			Exclusive:     pipeline.Bool(exclusive, false),
			DeleteOnStop:  pipeline.Bool(deleteOnStop, false),
			MultipleAck:   pipeline.Bool(multipleAck, false),
			RequeueOnFail: pipeline.Bool(requeueOnFail, false),
			ConsumerID:    pipeline.String(consumerIDKey, ""),
		},
	}

	eventBus, id := events.NewEventBus()
	jb := &Driver{
		prop:   prop,
		tracer: tracer,
		log:    log,
		pq:     pq,
		stopCh: make(chan struct{}, 1),
		// events
		eventBus: eventBus,
		id:       id,

		publishChan: make(chan *amqp.Channel, 1),
		stateChan:   make(chan *amqp.Channel, 1),

		redialCh:             make(chan *redialMsg, 5),
		notifyCloseConsumeCh: make(chan *amqp.Error, 1),
		notifyCloseConnCh:    make(chan *amqp.Error, 1),
		notifyCloseStatCh:    make(chan *amqp.Error, 1),
		notifyClosePubCh:     make(chan *amqp.Error, 1),
	}

	v := pipeline.String(queueHeaders, "")
	if v != "" {
		var tp map[string]any
		err = json.Unmarshal([]byte(v), &tp)
		if err != nil {
			log.Warn("failed to unmarshal headers", "value", v)
			return nil, errors.E(op, fmt.Errorf("failed to unmarshal headers: %w", err))
		}

		conf.V2Config.QueueConfig.Headers = tp
	}

	if pipeline.Has(exchangeDeclare) {
		conf.V2Config.ExchangeConfig.Declare = new(pipeline.Bool(exchangeDeclare, true))
	}

	if pipeline.Has(queueDeclare) {
		conf.V2Config.QueueConfig.Declare = new(pipeline.Bool(queueDeclare, true))
	}

	err = conf.InitDefault()
	if err != nil {
		return nil, errors.E(op, err)
	}

	jb.conn, err = dial(conf.Addr, &conf)
	if err != nil {
		return nil, errors.E(op, err)
	}

	jb.config.Store(&conf)

	err = jb.init()
	if err != nil {
		_ = jb.conn.Close()
		return nil, errors.E(op, err)
	}

	pch, err := jb.conn.Channel()
	if err != nil {
		_ = jb.conn.Close()
		return nil, errors.E(op, err)
	}

	err = pch.Confirm(false)
	if err != nil {
		_ = pch.Close()
		_ = jb.conn.Close()
		return nil, errors.E(op, fmt.Errorf("failed to turn on publisher confirms on the channel: %w", err))
	}

	// channel to report amqp states
	stch, err := jb.conn.Channel()
	if err != nil {
		_ = pch.Close()
		_ = jb.conn.Close()
		return nil, errors.E(op, err)
	}

	jb.conn.NotifyClose(jb.notifyCloseConnCh)
	pch.NotifyClose(jb.notifyClosePubCh)
	stch.NotifyClose(jb.notifyCloseStatCh)

	jb.publishChan <- pch
	jb.stateChan <- stch

	// register the pipeline
	jb.pipeline.Store(&pipeline)

	// run redialer for the connection
	jb.redialer()
	jb.redialMergeCh()

	return jb, nil
}

func (d *Driver) Push(ctx context.Context, job jobs.Message) error {
	const op = errors.Op("amqp_driver_push")
	// check if the pipeline registered

	ctx, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "amqp_push")
	defer span.End()

	// load atomic value
	pipe := *d.pipeline.Load()
	if pipe.Name() != job.GroupID() {
		return errors.E(op, errors.Errorf("no such pipeline: %s, actual: %s", job.GroupID(), pipe.Name()))
	}

	err := d.handleItem(ctx, fromJob(job))
	if err != nil {
		return errors.E(op, err)
	}

	return nil
}

func (d *Driver) Run(ctx context.Context, p jobs.Pipeline) error {
	start := time.Now().UTC()
	const op = errors.Op("amqp_driver_run")

	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "amqp_run")
	defer span.End()

	pipe := *d.pipeline.Load()
	if pipe.Name() != p.Name() {
		return errors.E(op, errors.Errorf("no such pipeline registered: %s", pipe.Name()))
	}

	if d.config.Load().queueName() == "" {
		return errors.Str("empty queue name, consider adding the queue name to the AMQP configuration")
	}

	// protect connection (redial)
	d.mu.Lock()
	defer d.mu.Unlock()

	// declare/bind/check the queue
	var err error
	err = d.declareQueue()
	if err != nil {
		return err
	}

	d.consumeChan, err = d.conn.Channel()
	if err != nil {
		return errors.E(op, err)
	}

	err = d.consumeChan.Qos(d.config.Load().Prefetch, 0, false)
	if err != nil {
		return errors.E(op, err)
	}

	// start reading messages from the channel
	deliv, err := d.consumeChan.Consume(
		d.config.Load().queueName(),
		d.config.Load().consumerID(),
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return errors.E(op, err)
	}

	d.consumeChan.NotifyClose(d.notifyCloseConsumeCh)
	d.listener(deliv)
	d.listeners.Store(1)
	d.log.Debug("pipeline was started", "driver", pipe.Driver(), "pipeline", pipe.Name(), "start", start, "elapsed", time.Since(start).Milliseconds())
	return nil
}

func (d *Driver) State(ctx context.Context) (*jobs.State, error) {
	const op = errors.Op("amqp_driver_state")
	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "amqp_state")
	defer span.End()

	select {
	case stateCh := <-d.stateChan:
		defer func() {
			d.stateChan <- stateCh
		}()

		conf := d.config.Load()
		pipe := *d.pipeline.Load()

		// If queue declaration is disabled, we should not use passive checks:
		// they require queue configure permissions in RabbitMQ.
		if !conf.queueDeclareEnabled() {
			// d.conn should be protected (redial)
			d.mu.RLock()
			defer d.mu.RUnlock()

			if !d.conn.IsClosed() {
				return &jobs.State{
					Priority: uint64(pipe.Priority()), //nolint:gosec
					Pipeline: pipe.Name(),
					Driver:   pipe.Driver(),
					Queue:    conf.queueName(),
					Delayed:  d.delayed.Load(),
					Ready:    ready(d.listeners.Load()),
				}, nil
			}

			return nil, errors.Str("connection is closed, can't get the state")
		}

		// if there is no queue, check the connection instead
		if conf.queueName() == "" {
			// d.conn should be protected (redial)
			d.mu.RLock()
			defer d.mu.RUnlock()

			if !d.conn.IsClosed() {
				return &jobs.State{
					Priority: uint64(pipe.Priority()), //nolint:gosec
					Pipeline: pipe.Name(),
					Driver:   pipe.Driver(),
					Delayed:  d.delayed.Load(),
					Ready:    ready(d.listeners.Load()),
				}, nil
			}

			return nil, errors.Str("connection is closed, can't get the state")
		}

		// verify or declare a queue
		q, err := stateCh.QueueDeclarePassive(
			conf.queueName(),
			conf.queueDurable(),
			conf.queueAutoDelete(),
			conf.queueExclusive(),
			false,
			conf.queueHeadersArgs(),
		)

		if err != nil {
			return nil, errors.E(op, err)
		}

		return &jobs.State{
			Priority: uint64(pipe.Priority()), //nolint:gosec
			Pipeline: pipe.Name(),
			Driver:   pipe.Driver(),
			Queue:    q.Name,
			Active:   int64(q.Messages),
			Delayed:  d.delayed.Load(),
			Ready:    ready(d.listeners.Load()),
		}, nil

	case <-ctx.Done():
		return nil, errors.E(op, errors.TimeOut, ctx.Err())
	}
}

func (d *Driver) Pause(ctx context.Context, p string) error {
	start := time.Now().UTC()
	pipe := *d.pipeline.Load()

	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "amqp_pause")
	defer span.End()

	if pipe.Name() != p {
		return errors.Errorf("no such pipeline: %s", pipe.Name())
	}

	if d.config.Load().queueName() == "" {
		return errors.Str("empty queue name, consider adding the queue name to the AMQP configuration")
	}

	// protect connection (redial)
	d.mu.Lock()
	defer d.mu.Unlock()

	// check and clear the listener flag while holding the lock
	if !d.listeners.CompareAndSwap(1, 0) {
		return errors.Str("no active listeners, nothing to pause")
	}

	err := d.consumeChan.Cancel(d.config.Load().consumerID(), true)
	if err != nil {
		d.log.Error("cancel consume channel, forcing close", "error", err)
		errCl := d.consumeChan.Close()
		if errCl != nil {
			return stderr.Join(errCl, err)
		}
		return err
	}

	d.log.Debug("pipeline was paused",
		"driver", pipe.Driver(),
		"pipeline", pipe.Name(),
		"start", start,
		"elapsed", time.Since(start).Milliseconds(),
	)

	return nil
}

func (d *Driver) Resume(ctx context.Context, p string) error {
	start := time.Now().UTC()

	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "amqp_resume")
	defer span.End()

	pipe := *d.pipeline.Load()
	if pipe.Name() != p {
		return errors.Errorf("no such pipeline: %s", pipe.Name())
	}

	conf := d.config.Load()
	if conf.queueName() == "" {
		return errors.Str("empty queue name, consider adding the queue name to the AMQP configuration")
	}

	// protect connection (redial)
	d.mu.Lock()
	defer d.mu.Unlock()

	// no active listeners
	if d.listeners.Load() == 1 {
		return errors.Str("amqp listener is already in the active state")
	}

	var err error
	err = d.declareQueue()
	if err != nil {
		return err
	}

	d.consumeChan, err = d.conn.Channel()
	if err != nil {
		return err
	}

	err = d.consumeChan.Qos(conf.Prefetch, 0, false)
	if err != nil {
		return err
	}

	// start reading messages from the channel
	deliv, err := d.consumeChan.Consume(
		conf.queueName(),
		d.config.Load().consumerID(),
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	d.listener(deliv)
	// increase the listener counter
	d.listeners.Store(1)
	d.log.Debug("pipeline was resumed",
		"driver", pipe.Driver(),
		"pipeline", pipe.Name(),
		"start", start,
		"elapsed", time.Since(start).Milliseconds(),
		"listeners", int64(d.listeners.Load()),
	)

	return nil
}

func (d *Driver) Stop(ctx context.Context) error {
	start := time.Now().UTC()

	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "amqp_stop")
	defer span.End()

	d.eventBus.Unsubscribe(d.id)

	if !d.stopped.CompareAndSwap(0, 1) {
		return nil
	}

	select {
	case d.stopCh <- struct{}{}:
	default:
	}

	pipe := *d.pipeline.Load()

	// remove all pending JOBS associated with the pipeline
	d.pq.Remove(pipe.Name())

	d.log.Debug("pipeline was stopped", "driver", pipe.Driver(), "pipeline", pipe.Name(), "start", start, "elapsed", time.Since(start).Milliseconds())
	return nil
}

// handleItem
func (d *Driver) handleItem(ctx context.Context, msg *Item) error {
	const op = errors.Op("amqp_driver_handle_item")
	select {
	case pch := <-d.publishChan:
		// return the channel back
		defer func() {
			d.publishChan <- pch
		}()

		conf := d.config.Load()
		d.prop.Inject(ctx, propagation.HeaderCarrier(msg.headers))

		rk := d.setRoutingKey(msg.headers)

		// convert
		table, err := pack(msg.ID(), msg)
		if err != nil {
			return errors.E(op, err)
		}

		// handle timeouts
		if msg.Options.DelayDuration() > 0 {
			d.delayed.Add(1)
			// TODO declare separate method for this if condition
			// TODO dlx cache channel??
			delayMs := int64(msg.Options.DelayDuration().Seconds() * 1000)
			tmpQ := fmt.Sprintf("delayed-%d.%s.%s", delayMs, conf.exchangeName(), d.queueOrRk())
			_, err = pch.QueueDeclare(tmpQ, true, false, false, false, amqp.Table{
				dlx:           conf.exchangeName(),
				dlxRoutingKey: rk,
				dlxTTL:        delayMs,
				dlxExpires:    delayMs * 2,
			})
			if err != nil {
				d.delayed.Add(^int64(0))
				return errors.E(op, err)
			}

			err = pch.QueueBind(tmpQ, tmpQ, conf.exchangeName(), false, nil)
			if err != nil {
				d.delayed.Add(^int64(0))
				return errors.E(op, err)
			}

			// insert to the local, limited pipeline
			err = pch.PublishWithContext(ctx, conf.exchangeName(), tmpQ, false, false, amqp.Publishing{
				Headers:      table,
				ContentType:  contentType,
				Timestamp:    time.Now().UTC(),
				DeliveryMode: amqp.Persistent,
				Body:         msg.Body(),
			})

			if err != nil {
				d.delayed.Add(^int64(0))
				return errors.E(op, err)
			}

			return nil
		}

		dc, err := pch.PublishWithDeferredConfirmWithContext(ctx, conf.exchangeName(), rk, false, false, amqp.Publishing{
			Headers:      table,
			ContentType:  contentType,
			Timestamp:    time.Now().UTC(),
			DeliveryMode: amqp.Persistent,
			Body:         msg.Body(),
		})
		if err != nil {
			return errors.E(op, err)
		}

		ok, err := dc.WaitContext(ctx)
		// if error is not nil, ok would be false
		if err != nil {
			return errors.E(op, fmt.Errorf("failed to get publisher confirm: %w", err))
		}

		// publish was unsuccessful
		if !ok {
			return errors.E(op, fmt.Errorf("failed to get publisher confirm"))
		}

		return nil
	case <-ctx.Done():
		return errors.E(op, errors.TimeOut, ctx.Err())
	}
}

func dial(addr string, amqps *config) (*amqp.Connection, error) {
	// use non-tls connection
	if amqps.TLS == nil {
		conn, err := amqp.Dial(addr)
		if err != nil {
			return nil, err
		}
		return conn, nil
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	err := initTLS(amqps.TLS, tlsCfg)
	if err != nil {
		return nil, err
	}

	conn, err := amqp.DialTLS(addr, tlsCfg)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func ready(r uint32) bool {
	return r > 0
}

func (d *Driver) setRoutingKey(headers map[string][]string) string {
	if val, ok := headers[xRoutingKey]; ok {
		delete(headers, xRoutingKey)
		if len(val) == 1 && val[0] != "" {
			return val[0]
		}
	}

	return d.config.Load().routingKeyName()
}

func (d *Driver) queueOrRk() string {
	conf := d.config.Load()
	if conf.queueName() != "" {
		return conf.queueName()
	}

	return conf.routingKeyName()
}
