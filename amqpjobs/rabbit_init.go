package amqpjobs

import (
	"github.com/roadrunner-server/errors"
)

func (d *Driver) initRabbitMQ() error {
	const op = errors.Op("jobs_plugin_rmq_init")
	// Channel opens a unique, concurrent server channel to process the bulk of AMQP
	// messages.  Any error from methods on this receiver will render the receiver
	// invalid and a new Channel should be opened.
	channel, err := d.conn.Channel()
	if err != nil {
		return errors.E(op, err)
	}

	// declare an exchange (idempotent operation)
	err = channel.ExchangeDeclare(
		d.exchangeName,
		d.exchangeType,
		d.exchangeDurable,
		d.exchangeAutoDelete,
		false,
		false,
		nil,
	)
	if err != nil {
		return errors.E(op, err)
	}

	return channel.Close()
}

func (d *Driver) declareQueue() error {
	const op = errors.Op("jobs_plugin_rmq_queue_declare")
	channel, err := d.conn.Channel()
	if err != nil {
		return errors.E(op, err)
	}

	// verify or declare a queue
	q, err := channel.QueueDeclare(
		d.queue,
		d.durable,
		d.queueAutoDelete,
		d.exclusive,
		false,
		d.queueHeaders,
	)
	if err != nil {
		return errors.E(op, err)
	}

	// bind queue to the exchange
	err = channel.QueueBind(
		q.Name,
		d.routingKey,
		d.exchangeName,
		false,
		nil,
	)
	if err != nil {
		return errors.E(op, err)
	}

	return channel.Close()
}
