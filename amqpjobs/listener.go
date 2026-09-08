package amqpjobs

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

func (d *Driver) listener(deliv <-chan amqp.Delivery) {
	go func() {
		for msg := range deliv {
			del := d.fromDelivery(msg)

			ctx := otel.GetTextMapPropagator().Extract(context.Background(), propagation.HeaderCarrier(del.headers))
			ctx, span := d.tracer.Tracer(tracerName).Start(ctx, "amqp_listener")

			if del.Options.AutoAck {
				// AutoAck jobs enter the queue even if acknowledgment fails.
				_ = msg.Ack(false)
			}

			if del.headers == nil {
				del.headers = make(map[string][]string, 2)
			}

			d.prop.Inject(ctx, propagation.HeaderCarrier(del.headers))
			d.pq.Insert(del)
			span.End()
		}

		d.log.Debug("delivery channel was closed, leaving the AMQP listener")
		// Pause can clear the listener flag before this goroutine exits.
		_ = d.listeners.CompareAndSwap(1, 0)
		d.log.Debug("number of listeners", "listeners", d.listeners.Load())
	}()
}
