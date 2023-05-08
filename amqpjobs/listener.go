package amqpjobs

import (
	"context"
	"sync/atomic"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.uber.org/zap"
)

func (d *Driver) listener(deliv <-chan amqp.Delivery) {
	go func() {
		for msg := range deliv {
			del, err := d.fromDelivery(msg)

			ctx := otel.GetTextMapPropagator().Extract(context.Background(), propagation.HeaderCarrier(del.Headers))
			ctx, span := d.tracer.Tracer(tracerName).Start(ctx, "amqp_listener")

			if err != nil {
				span.SetAttributes(attribute.KeyValue{
					Key:   "error",
					Value: attribute.StringValue(err.Error()),
				})

				d.log.Error("delivery convert", zap.Error(err))
				/*
					Acknowledge failed job to prevent endless loo;
				*/
				err = msg.Ack(false)
				if err != nil {
					d.log.Error("nack failed", zap.Error(err))
				}

				if d != nil {
					del.Headers = nil
					del.Options = nil
				}
				continue
			}

			if del.Options.AutoAck {
				// we don't care about error here, since the job is not important
				_ = msg.Ack(false)
			}

			if del.Headers == nil {
				del.Headers = make(map[string][]string, 2)
			}

			d.prop.Inject(ctx, propagation.HeaderCarrier(del.Headers))
			// insert job into the main priority queue
			d.pq.Insert(del)
			span.End()
		}

		d.log.Debug("delivery channel was closed, leaving the rabbit listener")
		// reduce number of listeners
		if atomic.LoadUint32(&d.listeners) == 0 {
			d.log.Debug("number of listeners", zap.Uint32("listeners", atomic.LoadUint32(&d.listeners)))
			return
		}

		atomic.AddUint32(&d.listeners, ^uint32(0))
		d.log.Debug("number of listeners", zap.Uint32("listeners", atomic.LoadUint32(&d.listeners)))
	}()
}
