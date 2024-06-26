package amqpjobs

import (
	"context"
	stderr "errors"
	"maps"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/roadrunner-server/errors"
	"go.uber.org/zap"
)

var _ jobs.Job = (*Item)(nil)

const (
	auto string = "deduced_by_rr"
)

type Item struct {
	// Job contains pluginName of job broker (usually PHP class).
	Job string `json:"job"`
	// Ident is unique identifier of the job, should be provided from outside
	Ident string `json:"id"`
	// Payload is string data (usually JSON) passed to Job broker.
	Payload []byte `json:"payload"`
	// Headers with key-values pairs
	headers map[string][]string
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
	Delay int `json:"delay,omitempty"`
	// AutoAck option
	AutoAck bool `json:"auto_ack"`
	// AMQP Queue
	Queue string `json:"queue,omitempty"`

	// private
	stopped *uint64
	// ack delegates an acknowledgement through the Acknowledger interface that the client or server has finished work on a delivery
	ack func(multiply bool) error

	// nack negatively acknowledge the delivery of message(s) identified by the delivery tag from either the client or server.
	// When multiple is true, nack messages up to and including delivered messages up until the delivery tag delivered on the same channel.
	// When requeue is true, request the server to deliver this message to a different Driver. If it is not possible or requeue is false, the message will be dropped or delivered to a server configured dead-letter queue.
	// This method must not be used to select or requeue messages the client wishes not to handle, rather it is to inform the server that the client is incapable of handling this message at this time
	nack func(multiply bool, requeue bool) error

	// requeueFn used as a pointer to the push function
	requeueFn func(context.Context, *Item) error

	// delayed jobs TODO(rustatian): figure out how to get stats from the DLX
	delayed     *int64
	multipleAck bool
	requeue     bool
}

// DelayDuration returns delay duration in a form of time.Duration.
func (o *Options) DelayDuration() time.Duration {
	return time.Second * time.Duration(o.Delay)
}

func (i *Item) ID() string {
	return i.Ident
}

func (i *Item) GroupID() string {
	return i.Options.Pipeline
}

func (i *Item) Priority() int64 {
	return i.Options.Priority
}

// Body packs job payload into binary payload.
func (i *Item) Body() []byte {
	return i.Payload
}

func (i *Item) Headers() map[string][]string {
	return i.headers
}

// Context packs job context (job, id) into binary payload.
// Not used in the amqp, amqp.Table used instead
func (i *Item) Context() ([]byte, error) {
	ctx, err := json.Marshal(
		struct {
			ID       string              `json:"id"`
			Job      string              `json:"job"`
			Driver   string              `json:"driver"`
			Queue    string              `json:"queue"`
			Headers  map[string][]string `json:"headers"`
			Pipeline string              `json:"pipeline"`
		}{
			ID:       i.Ident,
			Job:      i.Job,
			Driver:   pluginName,
			Headers:  i.headers,
			Queue:    i.Options.Queue,
			Pipeline: i.Options.Pipeline,
		},
	)

	if err != nil {
		return nil, err
	}

	return ctx, nil
}

func (i *Item) Ack() error {
	if atomic.LoadUint64(i.Options.stopped) == 1 {
		return errors.Str("failed to acknowledge the JOB, the pipeline is probably stopped")
	}
	if i.Options.Delay > 0 {
		atomic.AddInt64(i.Options.delayed, ^int64(0))
	}
	return i.Options.ack(i.Options.multipleAck)
}

func (i *Item) NackWithOptions(requeue bool, delay int) error {
	if atomic.LoadUint64(i.Options.stopped) == 1 {
		return errors.Str("failed to acknowledge the JOB, the pipeline is probably stopped")
	}

	if requeue {
		// if delay is set, requeue with delay via non-native requeue
		if delay > 0 {
			return i.Requeue(nil, delay)
			// if delay is not set, requeue via native requeue
		}

		return i.Options.nack(false, true)
	}

	// otherwise, nack without requeue
	return i.Options.nack(false, false)
}

func (i *Item) Nack() error {
	if atomic.LoadUint64(i.Options.stopped) == 1 {
		return errors.Str("failed to acknowledge the JOB, the pipeline is probably stopped")
	}
	if i.Options.Delay > 0 {
		atomic.AddInt64(i.Options.delayed, ^int64(0))
	}
	return i.Options.nack(false, i.Options.requeue)
}

// Requeue with the provided delay, handled by the Nack
func (i *Item) Requeue(headers map[string][]string, delay int) error {
	if atomic.LoadUint64(i.Options.stopped) == 1 {
		return errors.Str("failed to acknowledge the JOB, the pipeline is probably stopped")
	}
	if i.Options.Delay > 0 {
		atomic.AddInt64(i.Options.delayed, ^int64(0))
	}

	// overwrite the delay
	i.Options.Delay = delay
	if i.headers == nil {
		i.headers = make(map[string][]string)
	}

	if len(headers) > 0 {
		maps.Copy(i.headers, headers)
	}

	err := i.Options.requeueFn(context.Background(), i)
	if err != nil {
		errNack := i.Options.nack(false, true)
		if errNack != nil {
			return stderr.Join(err, errNack)
		}

		return err
	}

	// ack the previous message to avoid duplicates
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
func (d *Driver) fromDelivery(deliv amqp.Delivery) *Item {
	item := d.unpack(deliv)

	switch item.Options.AutoAck {
	case true:
		d.log.Debug("using auto acknowledge for the job")
		// stubs for ack/nack
		item.Options.ack = func(bool) error {
			return nil
		}

		item.Options.nack = func(bool, bool) error {
			return nil
		}
	case false:
		d.log.Debug("using driver's ack for the job")
		item.Options.ack = deliv.Ack
		item.Options.nack = deliv.Nack
	}

	item.Options.stopped = &d.stopped
	item.Options.delayed = d.delayed
	// requeue func
	item.Options.requeueFn = d.handleItem

	return item
}

func fromJob(job jobs.Message) *Item {
	return &Item{
		Job:     job.Name(),
		Ident:   job.ID(),
		Payload: job.Payload(),
		headers: job.Headers(),
		Options: &Options{
			Priority: job.Priority(),
			Pipeline: job.GroupID(),
			Delay:    int(job.Delay()),
			AutoAck:  job.AutoAck(),
		},
	}
}

// pack job metadata into headers
func pack(id string, j *Item) (amqp.Table, error) {
	h, err := json.Marshal(j.headers)
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
func (d *Driver) unpack(deliv amqp.Delivery) *Item {
	item := &Item{
		headers: convHeaders(deliv.Headers, d.log),
		Payload: deliv.Body,
		Options: &Options{
			Pipeline:    (*d.pipeline.Load()).Name(),
			Queue:       d.queue,
			requeueFn:   d.handleItem,
			multipleAck: d.multipleAck,
			requeue:     d.requeueOnFail,
		},
	}

	if _, ok := deliv.Headers[jobs.RRID].(string); !ok {
		item.Ident = uuid.NewString()
		d.log.Debug("missing header rr_id, generating new one", zap.String("assigned ID", item.Ident))
	} else {
		item.Ident = deliv.Headers[jobs.RRID].(string)
	}

	if _, ok := deliv.Headers[jobs.RRJob].(string); !ok {
		item.Job = auto
		d.log.Debug("missing header rr_job, using the standard one", zap.String("assigned ID", item.Job))
	} else {
		item.Job = deliv.Headers[jobs.RRJob].(string)
	}

	if _, ok := deliv.Headers[jobs.RRPipeline].(string); ok {
		item.Options.Pipeline = deliv.Headers[jobs.RRPipeline].(string)
	}

	if h, ok := deliv.Headers[jobs.RRHeaders].([]byte); ok {
		err := json.Unmarshal(h, &item.headers)
		if err != nil {
			d.log.Warn("failed to unmarshal headers (should be JSON), continuing execution", zap.Any("headers", item.headers), zap.Error(err))
		}
	}

	if t, ok := deliv.Headers[jobs.RRDelay]; ok {
		switch tt := t.(type) {
		case int:
			item.Options.Delay = tt
		case int16:
			item.Options.Delay = int(tt)
		case int32:
			item.Options.Delay = int(tt)
		case int64:
			item.Options.Delay = int(tt)
		default:
			d.log.Warn("unknown delay type", zap.Strings("want", []string{"int, int16, int32, int64"}), zap.Any("actual", t))
		}
	}

	if t, ok := deliv.Headers[jobs.RRPriority]; !ok {
		// set pipe's priority
		item.Options.Priority = d.priority
	} else {
		switch t.(type) {
		case int, int16, int32, int64:
			item.Options.Priority = t.(int64)
		default:
			d.log.Warn("unknown priority type", zap.Strings("want", []string{"int, int16, int32, int64"}), zap.Any("actual", t))
		}
	}

	if aa, ok := deliv.Headers[jobs.RRAutoAck]; ok {
		if val, ok2 := aa.(bool); ok2 {
			item.Options.AutoAck = val
		}
	}

	return item
}
