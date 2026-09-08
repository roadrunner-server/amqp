package amqpjobs

import (
	"context"
	"encoding/json"
	stderr "errors"
	"maps"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/roadrunner-server/api-plugins/v6/jobs"
	"github.com/roadrunner-server/errors"
)

var _ jobs.Job = (*Item)(nil)

const (
	auto string = "deduced_by_rr"
)

type Item struct {
	// Job identifies the job handler.
	Job   string `json:"job"`
	Ident string `json:"id"`
	// Payload contains application-defined job data.
	Payload []byte `json:"payload"`
	headers map[string][]string
	// Options supplies job execution settings and must be non-nil.
	Options *Options `json:"options,omitzero"`
}

// Options defines job execution settings.
type Options struct {
	// Priority controls scheduling in the RoadRunner queue.
	Priority int64 `json:"priority"`
	// Pipeline names the RoadRunner pipeline.
	Pipeline string `json:"pipeline,omitzero"`
	// Delay specifies the job delay in seconds.
	Delay int `json:"delay,omitzero"`
	// AutoAck enables acknowledgment before queue insertion. See https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Delivery.Ack.
	AutoAck bool `json:"auto_ack"`
	// Queue names the source queue.
	Queue string `json:"queue,omitzero"`

	stopped *atomic.Uint64
	ack     func(multiply bool) error

	nack func(multiply bool, requeue bool) error

	// requeueFn republishes the job.
	requeueFn func(context.Context, *Item) error

	// delayed points to the driver's local delayed-job counter.
	delayed     *atomic.Int64
	multipleAck bool
	requeue     bool
}

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

func (i *Item) Body() []byte {
	return i.Payload
}

func (i *Item) Headers() map[string][]string {
	return i.headers
}

// Context encodes job metadata as JSON.
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
	if i.Options.stopped.Load() == 1 {
		return errors.Str("failed to acknowledge the JOB, the pipeline is probably stopped")
	}
	if i.Options.Delay > 0 {
		i.Options.delayed.Add(-1)
	}
	return i.Options.ack(i.Options.multipleAck)
}

func (i *Item) NackWithOptions(requeue bool, delay int) error {
	if i.Options.stopped.Load() == 1 {
		return errors.Str("failed to acknowledge the JOB, the pipeline is probably stopped")
	}

	if requeue {
		// Republish the job to apply the delay.
		if delay > 0 {
			return i.Requeue(nil, delay)
		}

		return i.Options.nack(false, true)
	}

	return i.Options.nack(false, false)
}

func (i *Item) Nack() error {
	if i.Options.stopped.Load() == 1 {
		return errors.Str("failed to acknowledge the JOB, the pipeline is probably stopped")
	}
	if i.Options.Delay > 0 {
		i.Options.delayed.Add(-1)
	}
	return i.Options.nack(false, i.Options.requeue)
}

// Requeue republishes the job with the specified delay in seconds.
func (i *Item) Requeue(headers map[string][]string, delay int) error {
	if i.Options.stopped.Load() == 1 {
		return errors.Str("failed to acknowledge the JOB, the pipeline is probably stopped")
	}
	if i.Options.Delay > 0 {
		i.Options.delayed.Add(-1)
	}

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

	// Acknowledge the original delivery to prevent its redelivery. See https://pkg.go.dev/github.com/rabbitmq/amqp091-go#Delivery.Ack.
	err = i.Options.ack(false)
	if err != nil {
		return err
	}

	return nil
}

func (i *Item) Respond(_ []byte, _ string) error {
	return nil
}

// fromDelivery creates an Item with delivery callbacks and shared driver state.
func (d *Driver) fromDelivery(deliv amqp.Delivery) *Item {
	item := d.unpack(deliv)

	if item.Options.AutoAck {
		d.log.Debug("using auto acknowledge for the job")
		// The listener acknowledges AutoAck deliveries before queue insertion.
		item.Options.ack = func(bool) error {
			return nil
		}

		item.Options.nack = func(bool, bool) error {
			return nil
		}
	} else {
		d.log.Debug("using driver's ack for the job")
		item.Options.ack = deliv.Ack
		item.Options.nack = deliv.Nack
	}

	item.Options.stopped = &d.stopped
	item.Options.delayed = &d.delayed
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

// pack encodes job metadata in AMQP headers.
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

// unpack decodes a delivery and applies pipeline defaults.
func (d *Driver) unpack(deliv amqp.Delivery) *Item {
	conf := d.config.Load()
	item := &Item{
		headers: convHeaders(deliv.Headers, d.log),
		Payload: deliv.Body,
		Options: &Options{
			Pipeline:    (*d.pipeline.Load()).Name(),
			Queue:       conf.QueueConfig.Name,
			requeueFn:   d.handleItem,
			multipleAck: conf.QueueConfig.MultipleAck,
			requeue:     conf.QueueConfig.RequeueOnFail,
		},
	}

	if v, ok := deliv.Headers[jobs.RRID].(string); !ok {
		item.Ident = uuid.NewString()
		d.log.Debug("missing header rr_id, generating new one", "assigned ID", item.Ident)
	} else {
		item.Ident = v
	}

	if v, ok := deliv.Headers[jobs.RRJob].(string); !ok {
		item.Job = auto
		d.log.Debug("missing header rr_job, using the standard one", "assigned ID", item.Job)
	} else {
		item.Job = v
	}

	if v, ok := deliv.Headers[jobs.RRPipeline].(string); ok {
		item.Options.Pipeline = v
	}

	if h, ok := deliv.Headers[jobs.RRHeaders].([]byte); ok {
		err := json.Unmarshal(h, &item.headers)
		if err != nil {
			d.log.Warn("failed to unmarshal headers (should be JSON), continuing execution", "headers", item.headers, "error", err)
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
			d.log.Warn("unknown delay type", "want", []string{"int, int16, int32, int64"}, "actual", t)
		}
	}

	if t, ok := deliv.Headers[jobs.RRPriority]; !ok {
		item.Options.Priority = d.config.Load().Priority
	} else {
		switch tt := t.(type) {
		case int:
			item.Options.Priority = int64(tt)
		case int16:
			item.Options.Priority = int64(tt)
		case int32:
			item.Options.Priority = int64(tt)
		case int64:
			item.Options.Priority = tt
		default:
			d.log.Warn("unknown priority type", "want", []string{"int, int16, int32, int64"}, "actual", t)
		}
	}

	if aa, ok := deliv.Headers[jobs.RRAutoAck]; ok {
		if val, ok2 := aa.(bool); ok2 {
			item.Options.AutoAck = val
		}
	}

	return item
}
