package amqpjobs

import (
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/roadrunner-server/errors"
	"go.uber.org/zap"
)

const (
	ConnCloseType    string = "connection"
	ConsumeCloseType string = "consume"
	PublishCloseType string = "publish"
	StatCloseType    string = "stat"
)

type redialMsg struct {
	t   string
	err *amqp.Error
}

// redialer used to redial to the rabbitmq in case of the connection interrupts
func (d *Driver) redialer() { //nolint:gocognit,gocyclo
	go func() {
		for {
			select {
			case err := <-d.notifyCloseConnCh:
				if err == nil {
					d.log.Debug("exited from redialer")
					return
				}

				// stopped
				if atomic.LoadUint64(&d.stopped) == 1 {
					d.log.Debug("redialer stopped")
					continue
				}

				select {
				case d.redialCh <- &redialMsg{
					t:   ConnCloseType,
					err: err,
				}:
					d.log.Debug("exited from redialer")
					return
				default:
					d.log.Debug("exited from redialer")
					return
				}

			case err := <-d.notifyCloseConsumeCh:
				if err == nil {
					d.log.Debug("exited from redialer")
					return
				}

				// stopped
				if atomic.LoadUint64(&d.stopped) == 1 {
					d.log.Debug("redialer stopped")
					continue
				}

				select {
				case d.redialCh <- &redialMsg{
					t:   ConsumeCloseType,
					err: err,
				}:
					d.log.Debug("exited from redialer")
					return
				default:
					d.log.Debug("exited from redialer")
					return
				}

			case err := <-d.notifyClosePubCh:
				if err == nil {
					d.log.Debug("exited from redialer")
					return
				}

				// stopped
				if atomic.LoadUint64(&d.stopped) == 1 {
					d.log.Debug("redialer stopped")
					continue
				}

				select {
				case d.redialCh <- &redialMsg{
					t:   PublishCloseType,
					err: err,
				}:
					d.log.Debug("exited from redialer")
					return
				default:
					d.log.Debug("exited from redialer")
					return
				}

			case err := <-d.notifyCloseStatCh:
				if err == nil {
					d.log.Debug("redialer stopped")
					return
				}

				// stopped
				if atomic.LoadUint64(&d.stopped) == 1 {
					d.log.Debug("redialer stopped")
					continue
				}

				select {
				case d.redialCh <- &redialMsg{
					t:   StatCloseType,
					err: err,
				}:
					d.log.Debug("redialer stopped")
					return
				default:
					d.log.Debug("redialer stopped")
					return
				}

			case <-d.stopCh:
				d.log.Debug("starting stop routine")

				pch := <-d.publishChan
				stCh := <-d.stateChan

				// cancel new deliviries
				err := pch.Cancel(d.consumeID, false)
				if err != nil {
					d.log.Error("consumer cancel", zap.Error(err), zap.String("consumerID", d.consumeID))
				}

				// wait for the listener to stop
				for atomic.CompareAndSwapUint32(&d.listeners, 1, 0) {
					time.Sleep(time.Millisecond)
				}

				// remove the items associated with that pipeline from the priority_queue
				_ = d.pq.Remove((*d.pipeline.Load()).Name())

				if d.deleteQueueOnStop {
					var n int
					n, err = pch.QueueDelete(d.queue, false, false, false)
					if err != nil {
						d.log.Error("queue delete", zap.Error(err))
					}
					d.log.Debug("number of purged messages", zap.Int("count", n))
				}

				err = pch.Close()
				if err != nil {
					d.log.Error("publish channel close", zap.Error(err))
				}
				err = stCh.Close()
				if err != nil {
					d.log.Error("state channel close", zap.Error(err))
				}

				if d.consumeChan != nil {
					err = d.consumeChan.Close()
					if err != nil {
						d.log.Error("consume channel close", zap.Error(err))
					}
				}

				if d.conn != nil {
					err = d.conn.Close()
					if err != nil {
						d.log.Error("amqp connection closed", zap.Error(err))
					}
				}

				return
			}
		}
	}()
}

func (d *Driver) reset() {
	pch := <-d.publishChan
	stCh := <-d.stateChan

	err := pch.Close()
	if err != nil {
		d.log.Error("publish channel close", zap.Error(err))
	}
	err = stCh.Close()
	if err != nil {
		d.log.Error("state channel close", zap.Error(err))
	}

	if d.consumeChan != nil {
		err = d.consumeChan.Close()
		if err != nil {
			d.log.Error("consume channel close", zap.Error(err))
		}
	}

	if d.conn != nil {
		err = d.conn.Close()
		if err != nil {
			d.log.Error("amqp connection closed", zap.Error(err))
		}
	}
}

func (d *Driver) redialMergeCh() {
	go func() {
		for rm := range d.redialCh {
			d.mu.Lock()
			d.redial(rm)
			d.mu.Unlock()
		}
	}()
}

func (d *Driver) redial(rm *redialMsg) {
	const op = errors.Op("rabbitmq_redial")
	// trash the broken publishing channel
	d.reset()

	t := time.Now().UTC()
	pipe := *d.pipeline.Load()

	d.log.Error("pipeline connection was closed, redialing", zap.Error(rm.err), zap.String("pipeline", pipe.Name()), zap.String("driver", pipe.Driver()), zap.Time("start", t))

	expb := backoff.NewExponentialBackOff()
	// set the retry timeout (minutes)
	expb.MaxElapsedTime = d.retryTimeout
	operation := func() error {
		var err error
		d.conn, err = amqp.Dial(d.connStr)
		if err != nil {
			return errors.E(op, err)
		}

		d.log.Info("rabbitmq dial was succeed. trying to redeclare queues and subscribers")

		// re-init connection
		err = d.initRabbitMQ()
		if err != nil {
			d.log.Error("rabbitmq dial", zap.Error(err))
			return errors.E(op, err)
		}

		// redeclare publish channel
		pch, err := d.conn.Channel()
		if err != nil {
			return errors.E(op, err)
		}

		sch, err := d.conn.Channel()
		if err != nil {
			return errors.E(op, err)
		}

		d.notifyClosePubCh = make(chan *amqp.Error, 1)
		d.notifyCloseStatCh = make(chan *amqp.Error, 1)
		d.notifyCloseConnCh = make(chan *amqp.Error, 1)

		d.conn.NotifyClose(d.notifyCloseConnCh)
		pch.NotifyClose(d.notifyClosePubCh)
		sch.NotifyClose(d.notifyCloseStatCh)

		// put the fresh channels
		d.stateChan <- sch
		d.publishChan <- pch

		// we should restore the listener only when we previously had an active listener
		// OR if we get a Consume Closed type of the error
		if atomic.LoadUint32(&d.listeners) == 1 || rm.t == ConsumeCloseType {
			// redeclare consume channel
			d.consumeChan, err = d.conn.Channel()
			if err != nil {
				return errors.E(op, err)
			}

			err = d.consumeChan.Qos(d.prefetch, 0, false)
			if err != nil {
				d.log.Error("QOS", zap.Error(err))
				return errors.E(op, err)
			}

			// start reading messages from the channel
			deliv, err := d.consumeChan.Consume(
				d.queue,
				d.consumeID,
				false,
				false,
				false,
				false,
				nil,
			)
			if err != nil {
				return errors.E(op, err)
			}
			d.notifyCloseConsumeCh = make(chan *amqp.Error, 1)
			d.consumeChan.NotifyClose(d.notifyCloseConsumeCh)
			// restart listener
			err = d.declareQueue()
			if err != nil {
				return err
			}

			atomic.StoreUint32(&d.listeners, 1)
			d.listener(deliv)
			d.log.Info("consumer restored successfully")
		}

		d.log.Info("queues and subscribers was redeclared successfully")

		return nil
	}

	retryErr := backoff.Retry(operation, expb)
	if retryErr != nil {
		d.log.Error("backoff operation failed", zap.Error(retryErr))
		return
	}

	d.log.Info("connection was successfully restored", zap.String("pipeline", pipe.Name()), zap.String("driver", pipe.Driver()), zap.Time("start", t), zap.Int("elapsed", time.Since(t).Milliseconds()))

	// restart redialer
	d.redialer()
	d.log.Info("redialer restarted")
}
