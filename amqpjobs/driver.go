package amqpjobs

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/roadrunner-server/api/v4/plugins/v1/jobs"
	pq "github.com/roadrunner-server/api/v4/plugins/v1/priority_queue"
	"github.com/roadrunner-server/api/v4/plugins/v1/status"
	"github.com/roadrunner-server/errors"
	"go.uber.org/zap"
)

const (
	pluginName string = "amqp"
)

var _ jobs.Driver = (*Driver)(nil)

type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct.
	UnmarshalKey(name string, out any) error
	// Has checks if config section exists.
	Has(name string) bool
}
type Driver struct {
	mu         sync.Mutex
	log        *zap.Logger
	pq         pq.Queue
	pipeline   atomic.Pointer[jobs.Pipeline]
	consumeAll bool

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
	consumeID   string
	connStr     string

	retryTimeout      time.Duration
	prefetch          int
	priority          int64
	exchangeName      string
	queue             string
	exclusive         bool
	exchangeType      string
	routingKey        string
	multipleAck       bool
	requeueOnFail     bool
	durable           bool
	deleteQueueOnStop bool

	// new in 2.12
	exchangeDurable    bool
	exchangeAutoDelete bool
	queueAutoDelete    bool

	// new in 2.12.2
	queueHeaders map[string]any

	listeners uint32
	delayed   *int64
	stopCh    chan struct{}
	stopped   uint32
}

// FromConfig initializes rabbitmq pipeline
func FromConfig(configKey string, log *zap.Logger, cfg Configurer, pipeline jobs.Pipeline, pq pq.Queue, _ chan<- jobs.Commander) (*Driver, error) {
	const op = errors.Op("new_amqp_consumer")
	// we need to obtain two parts of the amqp information here.
	// firs part - address to connect, it is located in the global section under the amqp pluginName
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

	conf.InitDefault()
	// PARSE CONFIGURATION END -------

	jb := &Driver{
		log:        log,
		pq:         pq,
		stopCh:     make(chan struct{}, 1),
		consumeAll: conf.ConsumeAll,

		priority: conf.Priority,
		delayed:  ptrTo(int64(0)),

		publishChan: make(chan *amqp.Channel, 1),
		stateChan:   make(chan *amqp.Channel, 1),
		redialCh:    make(chan *redialMsg, 5),

		notifyCloseConsumeCh: make(chan *amqp.Error, 1),
		notifyCloseConnCh:    make(chan *amqp.Error, 1),
		notifyCloseStatCh:    make(chan *amqp.Error, 1),
		notifyClosePubCh:     make(chan *amqp.Error, 1),

		consumeID:         conf.ConsumerID,
		routingKey:        conf.RoutingKey,
		queue:             conf.Queue,
		durable:           conf.Durable,
		exchangeType:      conf.ExchangeType,
		deleteQueueOnStop: conf.DeleteQueueOnStop,
		exchangeName:      conf.Exchange,
		prefetch:          conf.Prefetch,
		exclusive:         conf.Exclusive,
		multipleAck:       conf.MultipleAck,
		requeueOnFail:     conf.RequeueOnFail,

		// 2.12
		retryTimeout:       time.Duration(conf.RedialTimeout) * time.Second,
		exchangeAutoDelete: conf.ExchangeAutoDelete,
		exchangeDurable:    conf.ExchangeDurable,
		queueAutoDelete:    conf.QueueAutoDelete,
		// 2.12.2
		queueHeaders: conf.QueueHeaders,
	}

	jb.conn, err = amqp.Dial(conf.Addr)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// save address
	jb.connStr = conf.Addr
	err = jb.initRabbitMQ()
	if err != nil {
		return nil, errors.E(op, err)
	}

	pch, err := jb.conn.Channel()
	if err != nil {
		return nil, errors.E(op, err)
	}

	stch, err := jb.conn.Channel()
	if err != nil {
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
func FromPipeline(pipeline jobs.Pipeline, log *zap.Logger, cfg Configurer, pq pq.Queue, _ chan<- jobs.Commander) (*Driver, error) {
	const op = errors.Op("new_amqp_consumer_from_pipeline")
	// we need to obtain two parts of the amqp information here.
	// firs part - address to connect, it is located in the global section under the amqp pluginName
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
	conf.InitDefault()
	// PARSE CONFIGURATION -------

	// parse prefetch
	prf, err := strconv.Atoi(pipeline.String(prefetch, "10"))
	if err != nil {
		log.Error("prefetch parse, driver will use default (10) prefetch", zap.String("prefetch", pipeline.String(prefetch, "10")))
	}

	jb := &Driver{
		log:     log,
		pq:      pq,
		stopCh:  make(chan struct{}, 1),
		delayed: ptrTo(int64(0)),

		publishChan: make(chan *amqp.Channel, 1),
		stateChan:   make(chan *amqp.Channel, 1),

		redialCh:             make(chan *redialMsg, 5),
		notifyCloseConsumeCh: make(chan *amqp.Error, 1),
		notifyCloseConnCh:    make(chan *amqp.Error, 1),
		notifyCloseStatCh:    make(chan *amqp.Error, 1),
		notifyClosePubCh:     make(chan *amqp.Error, 1),

		consumeAll:        pipeline.Bool(consumeAll, false),
		routingKey:        pipeline.String(routingKey, ""),
		queue:             pipeline.String(queue, "default"),
		exchangeType:      pipeline.String(exchangeType, "direct"),
		exchangeName:      pipeline.String(exchangeKey, "amqp.default"),
		prefetch:          prf,
		priority:          int64(pipeline.Int(priority, 10)),
		durable:           pipeline.Bool(durable, false),
		deleteQueueOnStop: pipeline.Bool(deleteOnStop, false),
		exclusive:         pipeline.Bool(exclusive, false),
		multipleAck:       pipeline.Bool(multipleAsk, false),
		requeueOnFail:     pipeline.Bool(requeueOnFail, false),

		// new in 2.12
		retryTimeout:       time.Duration(pipeline.Int(redialTimeout, 60)) * time.Second,
		exchangeAutoDelete: pipeline.Bool(exchangeAutoDelete, false),
		exchangeDurable:    pipeline.Bool(exchangeDurable, false),
		queueAutoDelete:    pipeline.Bool(queueAutoDelete, false),

		// 2.12.2
		queueHeaders: nil,

		// new in 2023.1.0
		consumeID: pipeline.String(consumerIDKey, fmt.Sprintf("roadrunner-%s", uuid.NewString())),
	}

	v := pipeline.String(queueHeaders, "")
	if v != "" {
		var tp map[string]any
		err = json.Unmarshal([]byte(v), &tp)
		if err != nil {
			log.Warn("failed to unmarshal headers", zap.String("value", v))
			return nil, err
		}

		jb.queueHeaders = tp
	}

	jb.conn, err = amqp.Dial(conf.Addr)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// save address
	jb.connStr = conf.Addr

	err = jb.initRabbitMQ()
	if err != nil {
		return nil, errors.E(op, err)
	}

	pch, err := jb.conn.Channel()
	if err != nil {
		return nil, errors.E(op, err)
	}

	// channel to report amqp states
	stch, err := jb.conn.Channel()
	if err != nil {
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

func (d *Driver) Push(ctx context.Context, job jobs.Job) error {
	const op = errors.Op("rabbitmq_push")
	// check if the pipeline registered

	// load atomic value
	pipe := *d.pipeline.Load()
	if pipe.Name() != job.Pipeline() {
		return errors.E(op, errors.Errorf("no such pipeline: %s, actual: %s", job.Pipeline(), pipe.Name()))
	}

	err := d.handleItem(ctx, fromJob(job))
	if err != nil {
		return errors.E(op, err)
	}

	return nil
}

func (d *Driver) Run(_ context.Context, p jobs.Pipeline) error {
	start := time.Now()
	const op = errors.Op("rabbit_run")

	pipe := *d.pipeline.Load()
	if pipe.Name() != p.Name() {
		return errors.E(op, errors.Errorf("no such pipeline registered: %s", pipe.Name()))
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

	err = d.consumeChan.Qos(d.prefetch, 0, false)
	if err != nil {
		return errors.E(op, err)
	}

	// start reading messages from the channel
	deliv, err := d.consumeChan.Consume(
		d.queue,
		d.consumeID,
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
	d.log.Debug("pipeline was started", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", start), zap.Duration("elapsed", time.Since(start)))
	return nil
}

func (d *Driver) State(ctx context.Context) (*jobs.State, error) {
	const op = errors.Op("amqp_driver_state")
	select {
	case stateCh := <-d.stateChan:
		defer func() {
			d.stateChan <- stateCh
		}()

		// verify or declare a queue
		q, err := stateCh.QueueDeclarePassive(
			d.queue,
			d.durable,
			d.queueAutoDelete,
			d.exclusive,
			false,
			d.queueHeaders,
		)

		if err != nil {
			return nil, errors.E(op, err)
		}

		pipe := *d.pipeline.Load()

		return &jobs.State{
			Priority: uint64(pipe.Priority()),
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

func (d *Driver) Pause(_ context.Context, p string) error {
	start := time.Now()
	pipe := *d.pipeline.Load()
	if pipe.Name() != p {
		return errors.Errorf("no such pipeline: %s", pipe.Name())
	}

	l := atomic.LoadUint32(&d.listeners)
	// no active listeners
	if l == 0 {
		return errors.Str("no active listeners, nothing to pause")
	}

	atomic.AddUint32(&d.listeners, ^uint32(0))

	// protect connection (redial)
	d.mu.Lock()
	defer d.mu.Unlock()

	err := d.consumeChan.Cancel(d.consumeID, true)
	if err != nil {
		d.log.Error("cancel publish channel, forcing close", zap.Error(err))
		errCl := d.consumeChan.Close()
		if errCl != nil {
			return errCl
		}
		return err
	}

	d.log.Debug("pipeline was paused", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", start), zap.Duration("elapsed", time.Since(start)))

	return nil
}

func (d *Driver) Resume(_ context.Context, p string) error {
	start := time.Now()
	pipe := *d.pipeline.Load()
	if pipe.Name() != p {
		return errors.Errorf("no such pipeline: %s", pipe.Name())
	}

	// protect connection (redial)
	d.mu.Lock()
	defer d.mu.Unlock()

	l := atomic.LoadUint32(&d.listeners)
	// no active listeners
	if l == 1 {
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

	err = d.consumeChan.Qos(d.prefetch, 0, false)
	if err != nil {
		return err
	}

	// start reading messages from the channel
	deliv, err := d.consumeChan.Consume(
		d.queue,
		d.consumeID,
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

	// increase number of listeners
	atomic.AddUint32(&d.listeners, 1)
	d.log.Debug("pipeline was resumed", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", start), zap.Duration("elapsed", time.Since(start)))

	return nil
}

func (d *Driver) Stop(context.Context) error {
	start := time.Now()
	atomic.StoreUint32(&d.stopped, 1)
	d.stopCh <- struct{}{}

	pipe := *d.pipeline.Load()
	d.log.Debug("pipeline was stopped", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", start), zap.Duration("elapsed", time.Since(start)))
	close(d.redialCh)
	return nil
}

func (d *Driver) Status() (*status.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	ch, err := d.conn.Channel()
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = ch.Close()
	}()

	// verify or declare a queue
	_, err = ch.QueueDeclarePassive(
		d.queue,
		d.durable,
		d.queueAutoDelete,
		d.exclusive,
		false,
		d.queueHeaders,
	)
	if err != nil {
		d.log.Error("queue inspect", zap.Error(err))
		return &status.Status{
			Code: 500,
		}, nil
	}

	return &status.Status{
		Code: 200,
	}, nil
}

// handleItem
func (d *Driver) handleItem(ctx context.Context, msg *Item) error {
	const op = errors.Op("rabbitmq_handle_item")
	select {
	case pch := <-d.publishChan:
		// return the channel back
		defer func() {
			d.publishChan <- pch
		}()

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
			tmpQ := fmt.Sprintf("delayed-%d.%s.%s", delayMs, d.exchangeName, d.queue)
			_, err = pch.QueueDeclare(tmpQ, true, false, false, false, amqp.Table{
				dlx:           d.exchangeName,
				dlxRoutingKey: d.routingKey,
				dlxTTL:        delayMs,
				dlxExpires:    delayMs * 2,
			})
			if err != nil {
				atomic.AddInt64(d.delayed, ^int64(0))
				return errors.E(op, err)
			}

			err = pch.QueueBind(tmpQ, tmpQ, d.exchangeName, false, nil)
			if err != nil {
				atomic.AddInt64(d.delayed, ^int64(0))
				return errors.E(op, err)
			}

			// insert to the local, limited pipeline
			err = pch.PublishWithContext(ctx, d.exchangeName, tmpQ, false, false, amqp.Publishing{
				Headers:      table,
				ContentType:  contentType,
				Timestamp:    time.Now(),
				DeliveryMode: amqp.Persistent,
				Body:         msg.Body(),
			})

			if err != nil {
				atomic.AddInt64(d.delayed, ^int64(0))
				return errors.E(op, err)
			}

			return nil
		}

		err = pch.PublishWithContext(ctx, d.exchangeName, d.routingKey, false, false, amqp.Publishing{
			Headers:      table,
			ContentType:  contentType,
			Timestamp:    time.Now(),
			DeliveryMode: amqp.Persistent,
			Body:         msg.Body(),
		})

		if err != nil {
			return errors.E(op, err)
		}

		return nil
	case <-ctx.Done():
		return errors.E(op, errors.TimeOut, ctx.Err())
	}
}

func ready(r uint32) bool {
	return r > 0
}

func ptrTo[T any](val T) *T {
	return &val
}
