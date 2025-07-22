package amqpjobs

import (
	"context"
	"sync/atomic"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.uber.org/zap"
)

func (d *Driver) listener(deliv <-chan amqp.Delivery) {
	go func() {
		for msg := range deliv {
			del := d.fromDelivery(msg)

			ctx := otel.GetTextMapPropagator().Extract(context.Background(), propagation.HeaderCarrier(del.headers))
			ctx, span := d.tracer.Tracer(tracerName).Start(ctx, "amqp_listener")

			if del.Options.AutoAck {
				// we don't care about error here, since the job is not important
				_ = msg.Ack(false)
			}

			if del.headers == nil {
				del.headers = make(map[string][]string, 2)
			}

			d.prop.Inject(ctx, propagation.HeaderCarrier(del.headers))
			// insert job into the main priority queue
			d.pq.Insert(del)
			span.End()
		}

		d.log.Debug("delivery channel was closed, leaving the AMQP listener")
		// reduce the number of listeners
		if atomic.LoadUint32(&d.listeners) == 0 {
			d.log.Debug("number of listeners", zap.Uint32("listeners", atomic.LoadUint32(&d.listeners)))
			return
		}

		atomic.AddUint32(&d.listeners, ^uint32(0))
		d.log.Debug("number of listeners", zap.Uint32("listeners", atomic.LoadUint32(&d.listeners)))
	}()
}
