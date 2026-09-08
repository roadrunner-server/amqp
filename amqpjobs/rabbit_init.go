package amqpjobs

import (
	"github.com/roadrunner-server/errors"
)

func (d *Driver) init() error {
	const op = errors.Op("jobs_plugin_amqp_init")
	conf := d.config.Load()
	channel, err := d.conn.Channel()
	if err != nil {
		return errors.E(op, err)
	}
	defer func() {
		_ = channel.Close()
	}()

	if !conf.exchangeDeclareEnabled() {
		return nil
	}

	err = channel.ExchangeDeclare(
		conf.ExchangeConfig.Name,
		conf.ExchangeConfig.Type,
		conf.ExchangeConfig.Durable,
		conf.ExchangeConfig.AutoDelete,
		false,
		false,
		nil,
	)
	if err != nil {
		return errors.E(op, err)
	}

	return nil
}

func (d *Driver) declareQueue() error {
	const op = errors.Op("jobs_plugin_rmq_queue_declare")
	conf := d.config.Load()
	channel, err := d.conn.Channel()
	if err != nil {
		return errors.E(op, err)
	}
	defer func() {
		_ = channel.Close()
	}()

	if !conf.queueDeclareEnabled() {
		return nil
	}

	q, err := channel.QueueDeclare(
		conf.QueueConfig.Name,
		conf.QueueConfig.Durable,
		conf.QueueConfig.AutoDelete,
		conf.QueueConfig.Exclusive,
		false,
		conf.QueueConfig.Headers,
	)
	if err != nil {
		return errors.E(op, err)
	}

	err = channel.QueueBind(
		q.Name,
		conf.QueueConfig.RoutingKey,
		conf.ExchangeConfig.Name,
		false,
		nil,
	)
	if err != nil {
		return errors.E(op, err)
	}

	return nil
}
