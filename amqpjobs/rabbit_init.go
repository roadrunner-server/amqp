package amqpjobs

import (
	"github.com/roadrunner-server/errors"
)

func (d *Driver) init() error {
	const op = errors.Op("jobs_plugin_amqp_init")
	conf := d.config.Load()
	// Channel opens a unique, concurrent server channel to process the bulk of AMQP
	// messages.  Any error from methods on this receiver will render the receiver
	// invalid and a new Channel should be opened.
	channel, err := d.conn.Channel()
	if err != nil {
		return errors.E(op, err)
	}

	// declare an exchange (idempotent operation)
	err = channel.ExchangeDeclare(
		conf.Exchange,
		conf.ExchangeType,
		conf.ExchangeDurable,
		conf.ExchangeAutoDelete,
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
	conf := d.config.Load()
	channel, err := d.conn.Channel()
	if err != nil {
		return errors.E(op, err)
	}

	// verify or declare a queue
	q, err := channel.QueueDeclare(
		conf.Queue,
		conf.Durable,
		conf.QueueAutoDelete,
		conf.Exclusive,
		false,
		conf.QueueHeaders,
	)
	if err != nil {
		return errors.E(op, err)
	}

	// bind queue to the exchange
	err = channel.QueueBind(
		q.Name,
		conf.RoutingKey,
		conf.Exchange,
		false,
		nil,
	)
	if err != nil {
		return errors.E(op, err)
	}

	return channel.Close()
}
