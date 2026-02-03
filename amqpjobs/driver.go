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
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/events"
	jprop "go.opentelemetry.io/contrib/propagators/jaeger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
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
	log      *zap.Logger
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

	listeners uint32
	delayed   *int64
	stopCh    chan struct{}
	stopped   uint64
}

// FromConfig initializes AMQP pipeline
func FromConfig(tracer *sdktrace.TracerProvider, configKey string, log *zap.Logger, cfg Configurer, pipeline jobs.Pipeline, pq jobs.Queue) (*Driver, error) {
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

		delayed: ptrTo(int64(0)),

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
func FromPipeline(tracer *sdktrace.TracerProvider, pipeline jobs.Pipeline, log *zap.Logger, cfg Configurer, pq jobs.Queue) (*Driver, error) {
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
	err = conf.InitDefault()
	if err != nil {
		return nil, err
	}
	// PARSE CONFIGURATION -------

	// parse prefetch
	prf, err := strconv.Atoi(pipeline.String(prefetch, "10"))
	if err != nil {
		log.Error("prefetch parse, driver will use default (10) prefetch", zap.String("prefetch", pipeline.String(prefetch, "10")))
		prf = 10
	}

	eventBus, id := events.NewEventBus()
	conf.RoutingKey = pipeline.String(routingKey, "")
	conf.Queue = pipeline.String(queue, "")
	conf.ExchangeType = pipeline.String(exchangeType, "direct")
	conf.Exchange = pipeline.String(exchangeKey, "amqp.default")
	conf.Prefetch = prf
	conf.Priority = int64(pipeline.Int(priority, 10))
	conf.Durable = pipeline.Bool(durable, false)
	conf.DeleteQueueOnStop = pipeline.Bool(deleteOnStop, false)
	conf.Exclusive = pipeline.Bool(exclusive, false)
	conf.MultipleAck = pipeline.Bool(multipleAck, false)
	conf.RequeueOnFail = pipeline.Bool(requeueOnFail, false)

	// new in 2.12
	conf.ExchangeAutoDelete = pipeline.Bool(exchangeAutoDelete, false)
	conf.ExchangeDurable = pipeline.Bool(exchangeDurable, false)
	conf.QueueAutoDelete = pipeline.Bool(queueAutoDelete, false)
	conf.RedialTimeout = pipeline.Int(redialTimeout, 60)
	conf.ConsumerID = pipeline.String(consumerIDKey, fmt.Sprintf("roadrunner-%s", uuid.NewString()))

	jb := &Driver{
		prop:    prop,
		tracer:  tracer,
		log:     log,
		pq:      pq,
		stopCh:  make(chan struct{}, 1),
		delayed: ptrTo(int64(0)),

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
			log.Warn("failed to unmarshal headers", zap.String("value", v))
			return nil, err
		}

		conf.QueueHeaders = tp
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

	conf := d.config.Load()
	if conf.RoutingKey == "" && conf.ExchangeType != "fanout" {
		return errors.Str("empty routing key, consider adding the routing key name to the AMQP configuration")
	}

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

	if d.config.Load().Queue == "" {
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
		d.config.Load().Queue,
		d.config.Load().ConsumerID,
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
	// run listener
	d.listener(deliv)

	atomic.StoreUint32(&d.listeners, 1)
	d.log.Debug("pipeline was started", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", start), zap.Int64("elapsed", time.Since(start).Milliseconds()))
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

		// if there is no queue, check the connection instead
		if conf.Queue == "" {
			// d.conn should be protected (redial)
			d.mu.RLock()
			defer d.mu.RUnlock()

			if !d.conn.IsClosed() {
				return &jobs.State{
					Priority: uint64(pipe.Priority()), //nolint:gosec
					Pipeline: pipe.Name(),
					Driver:   pipe.Driver(),
					Delayed:  atomic.LoadInt64(d.delayed),
					Ready:    ready(atomic.LoadUint32(&d.listeners)),
				}, nil
			}

			return nil, errors.Str("connection is closed, can't get the state")
		}

		// verify or declare a queue
		q, err := stateCh.QueueDeclarePassive(
			conf.Queue,
			conf.Durable,
			conf.QueueAutoDelete,
			conf.Exclusive,
			false,
			conf.QueueHeaders,
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
			Delayed:  atomic.LoadInt64(d.delayed),
			Ready:    ready(atomic.LoadUint32(&d.listeners)),
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

	if d.config.Load().Queue == "" {
		return errors.Str("empty queue name, consider adding the queue name to the AMQP configuration")
	}

	// atomically check and decrement the number of listeners
	if !atomic.CompareAndSwapUint32(&d.listeners, 1, 0) {
		return errors.Str("no active listeners, nothing to pause")
	}

	// protect connection (redial)
	d.mu.Lock()
	defer d.mu.Unlock()

	err := d.consumeChan.Cancel(d.config.Load().ConsumerID, true)
	if err != nil {
		d.log.Error("cancel publish channel, forcing close", zap.Error(err))
		errCl := d.consumeChan.Close()
		if errCl != nil {
			return stderr.Join(errCl, err)
		}
		return err
	}

	d.log.Debug("pipeline was paused",
		zap.String("driver", pipe.Driver()),
		zap.String("pipeline", pipe.Name()),
		zap.Time("start", start),
		zap.Int64("elapsed", time.Since(start).Milliseconds()),
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
	if conf.Queue == "" {
		return errors.Str("empty queue name, consider adding the queue name to the AMQP configuration")
	}

	// protect connection (redial)
	d.mu.Lock()
	defer d.mu.Unlock()

	// no active listeners
	if atomic.LoadUint32(&d.listeners) == 1 {
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
		conf.Queue,
		d.config.Load().ConsumerID,
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	// run listener
	d.listener(deliv)

	// increase the number of listeners
	atomic.AddUint32(&d.listeners, 1)
	d.log.Debug("pipeline was resumed",
		zap.String("driver", pipe.Driver()),
		zap.String("pipeline", pipe.Name()),
		zap.Time("start", start),
		zap.Int64("elapsed", time.Since(start).Milliseconds()),
	)

	return nil
}

func (d *Driver) Stop(ctx context.Context) error {
	start := time.Now().UTC()

	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "amqp_stop")
	defer span.End()

	d.eventBus.Unsubscribe(d.id)

	atomic.StoreUint64(&d.stopped, 1)
	d.stopCh <- struct{}{}

	pipe := *d.pipeline.Load()

	// remove all pending JOBS associated with the pipeline
	d.pq.Remove(pipe.Name())

	d.log.Debug("pipeline was stopped", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", start), zap.Int64("elapsed", time.Since(start).Milliseconds()))
	close(d.redialCh)
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
			atomic.AddInt64(d.delayed, 1)
			// TODO declare separate method for this if condition
			// TODO dlx cache channel??
			delayMs := int64(msg.Options.DelayDuration().Seconds() * 1000)
			tmpQ := fmt.Sprintf("delayed-%d.%s.%s", delayMs, conf.Exchange, d.queueOrRk())
			_, err = pch.QueueDeclare(tmpQ, true, false, false, false, amqp.Table{
				dlx:           conf.Exchange,
				dlxRoutingKey: rk,
				dlxTTL:        delayMs,
				dlxExpires:    delayMs * 2,
			})
			if err != nil {
				atomic.AddInt64(d.delayed, ^int64(0))
				return errors.E(op, err)
			}

			err = pch.QueueBind(tmpQ, tmpQ, conf.Exchange, false, nil)
			if err != nil {
				atomic.AddInt64(d.delayed, ^int64(0))
				return errors.E(op, err)
			}

			// insert to the local, limited pipeline
			err = pch.PublishWithContext(ctx, conf.Exchange, tmpQ, false, false, amqp.Publishing{
				Headers:      table,
				ContentType:  contentType,
				Timestamp:    time.Now().UTC(),
				DeliveryMode: amqp.Persistent,
				Body:         msg.Body(),
			})

			if err != nil {
				atomic.AddInt64(d.delayed, ^int64(0))
				return errors.E(op, err)
			}

			return nil
		}

		dc, err := pch.PublishWithDeferredConfirmWithContext(ctx, conf.Exchange, rk, false, false, amqp.Publishing{
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

func ptrTo[T any](val T) *T {
	return &val
}

func (d *Driver) setRoutingKey(headers map[string][]string) string {
	if val, ok := headers[xRoutingKey]; ok {
		delete(headers, xRoutingKey)
		if len(val) == 1 && val[0] != "" {
			return val[0]
		}
	}

	return d.config.Load().RoutingKey
}

func (d *Driver) queueOrRk() string {
	conf := d.config.Load()
	if conf.Queue != "" {
		return conf.Queue
	}

	return conf.RoutingKey
}
