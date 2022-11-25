package amqpjobs

import (
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/roadrunner-server/errors"
	"go.uber.org/zap"
)

// redialer used to redial to the rabbitmq in case of the connection interrupts
func (c *Consumer) redialer() { //nolint:gocognit,gocyclo
	go func() {
		for {
			select {
			case err := <-c.notifyCloseConnCh:
				if err == nil {
					c.log.Debug("exited from redialer")
					return
				}

				// stopped
				if atomic.LoadUint32(&c.stopped) == 1 {
					c.log.Debug("redialer stopped")
					continue
				}

				select {
				case c.redialCh <- err:
					c.log.Debug("exited from redialer")
					return
				default:
					c.log.Debug("exited from redialer")
					return
				}

			case err := <-c.notifyCloseConsumeCh:
				if err == nil {
					c.log.Debug("exited from redialer")
					return
				}

				// stopped
				if atomic.LoadUint32(&c.stopped) == 1 {
					c.log.Debug("redialer stopped")
					continue
				}

				select {
				case c.redialCh <- err:
					c.log.Debug("exited from redialer")
					return
				default:
					c.log.Debug("exited from redialer")
					return
				}
			case err := <-c.notifyClosePubCh:
				if err == nil {
					c.log.Debug("exited from redialer")
					return
				}

				// stopped
				if atomic.LoadUint32(&c.stopped) == 1 {
					c.log.Debug("redialer stopped")
					continue
				}

				select {
				case c.redialCh <- err:
					c.log.Debug("exited from redialer")
					return
				default:
					c.log.Debug("exited from redialer")
					return
				}
			case err := <-c.notifyCloseStatCh:
				if err == nil {
					c.log.Debug("redialer stopped")
					return
				}

				// stopped
				if atomic.LoadUint32(&c.stopped) == 1 {
					c.log.Debug("redialer stopped")
					continue
				}

				select {
				case c.redialCh <- err:
					c.log.Debug("redialer stopped")
					return
				default:
					c.log.Debug("redialer stopped")
					return
				}

			case <-c.stopCh:
				c.log.Debug("starting stop routine")

				pch := <-c.publishChan
				stCh := <-c.stateChan

				if c.deleteQueueOnStop {
					msg, err := pch.QueueDelete(c.queue, false, false, false)
					if err != nil {
						c.log.Error("queue delete", zap.Error(err))
					}
					c.log.Debug("number of purged messages", zap.Int("count", msg))
				}

				err := pch.Close()
				if err != nil {
					c.log.Error("publish channel close", zap.Error(err))
				}
				err = stCh.Close()
				if err != nil {
					c.log.Error("state channel close", zap.Error(err))
				}

				if c.consumeChan != nil {
					err = c.consumeChan.Close()
					if err != nil {
						c.log.Error("consume channel close", zap.Error(err))
					}
				}

				if c.conn != nil {
					err = c.conn.Close()
					if err != nil {
						c.log.Error("amqp connection closed", zap.Error(err))
					}
				}

				return
			}
		}
	}()
}

func (c *Consumer) reset() {
	pch := <-c.publishChan
	stCh := <-c.stateChan

	err := pch.Close()
	if err != nil {
		c.log.Error("publish channel close", zap.Error(err))
	}
	err = stCh.Close()
	if err != nil {
		c.log.Error("state channel close", zap.Error(err))
	}

	if c.consumeChan != nil {
		err = c.consumeChan.Close()
		if err != nil {
			c.log.Error("consume channel close", zap.Error(err))
		}
	}

	if c.conn != nil {
		err = c.conn.Close()
		if err != nil {
			c.log.Error("amqp connection closed", zap.Error(err))
		}
	}
}

func (c *Consumer) redialMergeCh() {
	go func() {
		for err := range c.redialCh {
			c.mu.Lock()
			c.redial(err)
			c.mu.Unlock()
		}
	}()
}

func (c *Consumer) redial(amqpErr *amqp.Error) {
	const op = errors.Op("rabbitmq_redial")
	// trash the broken publishing channel
	c.reset()

	t := time.Now().UTC()
	pipe := c.pipeline.Load()

	c.log.Error("pipeline connection was closed, redialing", zap.Error(amqpErr), zap.String("pipeline", pipe.Name()), zap.String("driver", pipe.Driver()), zap.Time("start", t))

	expb := backoff.NewExponentialBackOff()
	// set the retry timeout (minutes)
	expb.MaxElapsedTime = c.retryTimeout
	operation := func() error {
		var err error
		c.conn, err = amqp.Dial(c.connStr)
		if err != nil {
			return errors.E(op, err)
		}

		c.log.Info("rabbitmq dial was succeed. trying to redeclare queues and subscribers")

		// re-init connection
		err = c.initRabbitMQ()
		if err != nil {
			c.log.Error("rabbitmq dial", zap.Error(err))
			return errors.E(op, err)
		}

		// redeclare consume channel
		c.consumeChan, err = c.conn.Channel()
		if err != nil {
			return errors.E(op, err)
		}

		err = c.consumeChan.Qos(c.prefetch, 0, false)
		if err != nil {
			c.log.Error("QOS", zap.Error(err))
			return errors.E(op, err)
		}

		// redeclare publish channel
		pch, err := c.conn.Channel()
		if err != nil {
			return errors.E(op, err)
		}

		sch, err := c.conn.Channel()
		if err != nil {
			return errors.E(op, err)
		}

		// start reading messages from the channel
		deliv, err := c.consumeChan.Consume(
			c.queue,
			c.consumeID,
			false,
			false,
			false,
			false,
			nil,
		)
		if err != nil {
			return errors.E(op, err)
		}

		c.notifyClosePubCh = make(chan *amqp.Error, 1)
		c.notifyCloseStatCh = make(chan *amqp.Error, 1)
		c.notifyCloseConnCh = make(chan *amqp.Error, 1)
		c.notifyCloseConsumeCh = make(chan *amqp.Error, 1)

		c.conn.NotifyClose(c.notifyCloseConnCh)
		c.consumeChan.NotifyClose(c.notifyCloseConsumeCh)
		pch.NotifyClose(c.notifyClosePubCh)
		sch.NotifyClose(c.notifyCloseStatCh)

		// put the fresh channels
		c.stateChan <- sch
		c.publishChan <- pch

		// restart listener
		atomic.StoreUint32(&c.listeners, 1)
		c.listener(deliv)

		c.log.Info("queues and subscribers was redeclared successfully")

		return nil
	}

	retryErr := backoff.Retry(operation, expb)
	if retryErr != nil {
		c.log.Error("backoff operation failed", zap.Error(retryErr))
		return
	}

	c.log.Info("connection was successfully restored", zap.String("pipeline", pipe.Name()), zap.String("driver", pipe.Driver()), zap.Time("start", t), zap.Duration("elapsed", time.Since(t)))

	// restart redialer
	c.redialer()
	c.log.Info("redialer restarted")
}
