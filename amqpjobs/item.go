package amqpjobs

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/sdk/v3/plugins/jobs"
	"github.com/roadrunner-server/sdk/v3/utils"
	"go.uber.org/zap"
)

var _ jobs.Acknowledger = (*Item)(nil)

const (
	auto string = "deduced_by_rr"
)

type Item struct {
	// Job contains pluginName of job broker (usually PHP class).
	Job string `json:"job"`

	// Ident is unique identifier of the job, should be provided from outside
	Ident string `json:"id"`

	// Payload is string data (usually JSON) passed to Job broker.
	Payload string `json:"payload"`

	// Headers with key-values pairs
	Headers map[string][]string `json:"headers"`

	// Options contains set of PipelineOptions specific to job execution. Can be empty.
	Options *Options `json:"options,omitempty"`
}

// Options carry information about how to handle given job.
type Options struct {
	// Priority is job priority, default - 10
	// pointer to distinguish 0 as a priority and nil as priority not set
	Priority int64 `json:"priority"`

	// Pipeline manually specified pipeline.
	Pipeline string `json:"pipeline,omitempty"`

	// Delay defines time duration to delay execution for. Defaults to none.
	Delay int64 `json:"delay,omitempty"`

	// AutoAck option
	AutoAck bool `json:"auto_ack"`

	// private
	// ack delegates an acknowledgement through the Acknowledger interface that the client or server has finished work on a delivery
	ack func(multiply bool) error

	// nack negatively acknowledge the delivery of message(s) identified by the delivery tag from either the client or server.
	// When multiple is true, nack messages up to and including delivered messages up until the delivery tag delivered on the same channel.
	// When requeue is true, request the server to deliver this message to a different Consumer. If it is not possible or requeue is false, the message will be dropped or delivered to a server configured dead-letter queue.
	// This method must not be used to select or requeue messages the client wishes not to handle, rather it is to inform the server that the client is incapable of handling this message at this time
	nack func(multiply bool, requeue bool) error

	// requeueFn used as a pointer to the push function
	requeueFn func(context.Context, *Item) error

	// delayed jobs TODO(rustatian): figure out how to get stats from the DLX
	delayed     *int64
	multipleAsk bool
	requeue     bool
}

// DelayDuration returns delay duration in a form of time.Duration.
func (o *Options) DelayDuration() time.Duration {
	return time.Second * time.Duration(o.Delay)
}

func (i *Item) ID() string {
	return i.Ident
}

func (i *Item) Priority() int64 {
	return i.Options.Priority
}

// Body packs job payload into binary payload.
func (i *Item) Body() []byte {
	return utils.AsBytes(i.Payload)
}

// Context packs job context (job, id) into binary payload.
// Not used in the amqp, amqp.Table used instead
func (i *Item) Context() ([]byte, error) {
	ctx, err := json.Marshal(
		struct {
			ID       string              `json:"id"`
			Job      string              `json:"job"`
			Headers  map[string][]string `json:"headers"`
			Pipeline string              `json:"pipeline"`
		}{ID: i.Ident, Job: i.Job, Headers: i.Headers, Pipeline: i.Options.Pipeline},
	)

	if err != nil {
		return nil, err
	}

	return ctx, nil
}

func (i *Item) Ack() error {
	if i.Options.Delay > 0 {
		atomic.AddInt64(i.Options.delayed, ^int64(0))
	}
	return i.Options.ack(i.Options.multipleAsk)
}

func (i *Item) Nack() error {
	if i.Options.Delay > 0 {
		atomic.AddInt64(i.Options.delayed, ^int64(0))
	}
	return i.Options.nack(false, i.Options.requeue)
}

// Requeue with the provided delay, handled by the Nack
func (i *Item) Requeue(headers map[string][]string, delay int64) error {
	if i.Options.Delay > 0 {
		atomic.AddInt64(i.Options.delayed, ^int64(0))
	}
	// overwrite the delay
	i.Options.Delay = delay
	i.Headers = headers

	err := i.Options.requeueFn(context.Background(), i)
	if err != nil {
		errNack := i.Options.nack(false, true)
		if errNack != nil {
			return fmt.Errorf("requeue error: %w\nack error: %v", err, errNack)
		}

		return err
	}

	// ack the job
	err = i.Options.ack(false)
	if err != nil {
		return err
	}

	return nil
}

func (i *Item) Respond(_ []byte, _ string) error {
	return nil
}

// fromDelivery converts amqp.Delivery into an Item which will be pushed to the PQ
func (c *Consumer) fromDelivery(d amqp.Delivery) (*Item, error) {
	const op = errors.Op("from_delivery_convert")
	item, err := c.unpack(d)
	if err != nil {
		// can't decode the delivery
		if errors.Is(errors.Decode, err) && c.consumeAll {
			id := uuid.NewString()
			c.log.Debug("get raw payload", zap.String("assigned ID", id))

			if isJSONEncoded(d.Body) != nil {
				d.Body, err = json.Marshal(d.Body)
				if err != nil {
					return nil, err
				}
			}

			return &Item{
				Job:     auto,
				Ident:   id,
				Payload: utils.AsString(d.Body),
				Headers: convHeaders(d.Headers),
				Options: &Options{
					Priority:    10,
					Delay:       0,
					Pipeline:    auto,
					ack:         d.Ack,
					nack:        d.Nack,
					requeueFn:   c.handleItem,
					delayed:     c.delayed,
					AutoAck:     false,
					multipleAsk: false,
					requeue:     false,
				},
			}, nil
		}

		return nil, errors.E(op, err)
	}

	i := &Item{
		Job:     item.Job,
		Ident:   item.Ident,
		Payload: item.Payload,
		Headers: item.Headers,
		Options: item.Options,
	}

	switch item.Options.AutoAck {
	case true:
		c.log.Debug("using auto acknowledge for the job")
		// stubs for ack/nack
		item.Options.ack = func(_ bool) error {
			return nil
		}

		item.Options.nack = func(_ bool, _ bool) error {
			return nil
		}
	case false:
		c.log.Debug("using driver's ack for the job")
		item.Options.ack = d.Ack
		item.Options.nack = d.Nack
	}

	item.Options.delayed = c.delayed
	// requeue func
	item.Options.requeueFn = c.handleItem
	return i, nil
}

func fromJob(job *jobs.Job) *Item {
	return &Item{
		Job:     job.Job,
		Ident:   job.Ident,
		Payload: job.Payload,
		Headers: job.Headers,
		Options: &Options{
			Priority: job.Options.Priority,
			Pipeline: job.Options.Pipeline,
			Delay:    job.Options.Delay,
			AutoAck:  job.Options.AutoAck,
		},
	}
}

// pack job metadata into headers
func pack(id string, j *Item) (amqp.Table, error) {
	h, err := json.Marshal(j.Headers)
	if err != nil {
		return nil, err
	}
	return amqp.Table{
		jobs.RRID:       id,
		jobs.RRJob:      j.Job,
		jobs.RRPipeline: j.Options.Pipeline,
		jobs.RRHeaders:  h,
		jobs.RRDelay:    j.Options.Delay,
		jobs.RRPriority: j.Options.Priority,
		jobs.RRAutoAck:  j.Options.AutoAck,
	}, nil
}

// unpack restores jobs.Options
func (c *Consumer) unpack(d amqp.Delivery) (*Item, error) {
	item := &Item{
		Payload: utils.AsString(d.Body),
		Options: &Options{
			multipleAsk: c.multipleAck,
			requeue:     c.requeueOnFail,
			requeueFn:   c.handleItem,
		},
	}

	if _, ok := d.Headers[jobs.RRID].(string); !ok {
		return nil, errors.E(errors.Errorf("missing header `%s`", jobs.RRID), errors.Decode)
	}

	item.Ident = d.Headers[jobs.RRID].(string)

	if _, ok := d.Headers[jobs.RRJob].(string); !ok {
		return nil, errors.E(errors.Errorf("missing header `%s`", jobs.RRJob), errors.Decode)
	}

	item.Job = d.Headers[jobs.RRJob].(string)

	if _, ok := d.Headers[jobs.RRPipeline].(string); ok {
		item.Options.Pipeline = d.Headers[jobs.RRPipeline].(string)
	}

	if h, ok := d.Headers[jobs.RRHeaders].([]byte); ok {
		err := json.Unmarshal(h, &item.Headers)
		if err != nil {
			return nil, errors.E(err, errors.Decode)
		}
	}

	if t, ok := d.Headers[jobs.RRDelay]; ok {
		switch t.(type) {
		case int, int16, int32, int64:
			item.Options.Delay = t.(int64)
		default:
			c.log.Warn("unknown delay type", zap.Strings("want", []string{"int, int16, int32, int64"}), zap.Any("actual", t))
		}
	}

	if t, ok := d.Headers[jobs.RRPriority]; !ok {
		// set pipe's priority
		item.Options.Priority = c.priority
	} else {
		switch t.(type) {
		case int, int16, int32, int64:
			item.Options.Priority = t.(int64)
		default:
			c.log.Warn("unknown priority type", zap.Strings("want", []string{"int, int16, int32, int64"}), zap.Any("actual", t))
		}
	}

	if aa, ok := d.Headers[jobs.RRAutoAck]; ok {
		if val, ok := aa.(bool); ok {
			item.Options.AutoAck = val
		}
	}

	return item, nil
}

func isJSONEncoded(data []byte) error {
	var a any
	return json.Unmarshal(data, &a)
}
