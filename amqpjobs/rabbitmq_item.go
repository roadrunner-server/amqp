package amqpjobs

import (
	"encoding/json"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"go.uber.org/zap"
)

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
