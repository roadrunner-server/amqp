package amqpjobs

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/roadrunner-server/api-plugins/v6/jobs"
	"github.com/stretchr/testify/require"
)

// testPipeline is a minimal jobs.Pipeline implementation for unit tests.
// Only Name() is exercised by unpack/fromDelivery; the rest return zero values.
type testPipeline struct{ name string }

func (p *testPipeline) With(string, any)                    {}
func (p *testPipeline) Name() string                        { return p.name }
func (p *testPipeline) Driver() string                      { return pluginName }
func (p *testPipeline) Has(string) bool                     { return false }
func (p *testPipeline) String(_ string, d string) string    { return d }
func (p *testPipeline) Int(_ string, d int) int             { return d }
func (p *testPipeline) Bool(_ string, d bool) bool          { return d }
func (p *testPipeline) Map(string, map[string]string) error { return nil }
func (p *testPipeline) Priority() int64                     { return 0 }
func (p *testPipeline) Get(string) any                      { return nil }

// fakeAcker records ack/nack calls without a live AMQP channel.
type fakeAcker struct {
	acked, nacked bool
}

func (f *fakeAcker) Ack(_ uint64, _ bool) error     { f.acked = true; return nil }
func (f *fakeAcker) Nack(_ uint64, _, _ bool) error { f.nacked = true; return nil }
func (f *fakeAcker) Reject(_ uint64, _ bool) error  { return nil }

// testDriver builds a Driver with only the fields fromDelivery/unpack read:
// a logger, a config and a pipeline. No AMQP connection is required.
func testDriver(t *testing.T) *Driver {
	t.Helper()
	d := &Driver{log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	cfg := &config{Version: 1, V1Config: &v1config{RoutingKey: "rk"}}
	require.NoError(t, cfg.InitDefault())
	d.config.Store(cfg)

	var p jobs.Pipeline = &testPipeline{name: "test-pipe"}
	d.pipeline.Store(&p)
	return d
}

func newCounters(stopped uint64, delayed int64) (*atomic.Uint64, *atomic.Int64) {
	s := &atomic.Uint64{}
	s.Store(stopped)
	d := &atomic.Int64{}
	d.Store(delayed)
	return s, d
}

func TestUnpackAllHeaders(t *testing.T) {
	d := testDriver(t)

	src := &Item{
		Job:     "myjob",
		headers: map[string][]string{"x": {"y"}},
		Options: &Options{Pipeline: "custom-pipe", Delay: 5, Priority: 7, AutoAck: true},
	}
	tbl, err := pack("id-123", src)
	require.NoError(t, err)

	item := d.unpack(amqp.Delivery{Headers: tbl, Body: []byte("payload")})

	require.Equal(t, "id-123", item.Ident)
	require.Equal(t, "myjob", item.Job)
	require.Equal(t, "custom-pipe", item.Options.Pipeline)
	require.Equal(t, 5, item.Options.Delay)
	require.Equal(t, int64(7), item.Options.Priority)
	require.True(t, item.Options.AutoAck)
	require.Equal(t, []string{"y"}, item.headers["x"])
	require.Equal(t, []byte("payload"), item.Payload)
}

func TestUnpackMissingHeaders(t *testing.T) {
	d := testDriver(t)

	item := d.unpack(amqp.Delivery{Headers: amqp.Table{}})

	require.NotEmpty(t, item.Ident)                      // generated UUID
	require.Equal(t, auto, item.Job)                     // "deduced_by_rr"
	require.Equal(t, "test-pipe", item.Options.Pipeline) // pipeline.Name()
	require.Equal(t, int64(10), item.Options.Priority)   // falls back to the pipeline default
}

func TestUnpackDelayTypeVariants(t *testing.T) {
	d := testDriver(t)

	cases := []struct {
		name string
		val  any
		want int
	}{
		{"int", 3, 3},
		{"int16", int16(4), 4},
		{"int32", int32(5), 5},
		{"int64", int64(6), 6},
		{"wrong type is ignored", "nope", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := d.unpack(amqp.Delivery{Headers: amqp.Table{jobs.RRDelay: tc.val}})
			require.Equal(t, tc.want, item.Options.Delay)
		})
	}
}

func TestUnpackPriorityTypeVariants(t *testing.T) {
	d := testDriver(t)

	cases := []struct {
		name string
		val  any
		want int64
	}{
		{"int", 3, 3},
		{"int16", int16(4), 4},
		{"int32", int32(5), 5},
		{"int64", int64(6), 6},
		{"wrong type stays zero (present but unparseable)", "nope", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := d.unpack(amqp.Delivery{Headers: amqp.Table{jobs.RRPriority: tc.val}})
			require.Equal(t, tc.want, item.Options.Priority)
		})
	}
}

func TestFromDeliveryAutoAck(t *testing.T) {
	d := testDriver(t)

	tbl, err := pack("id-1", &Item{Options: &Options{AutoAck: true}})
	require.NoError(t, err)

	item := d.fromDelivery(amqp.Delivery{Headers: tbl})

	require.True(t, item.Options.AutoAck)
	// AutoAck wires no-op stubs, so neither needs a live Acknowledger.
	require.NoError(t, item.Options.ack(false))
	require.NoError(t, item.Options.nack(false, false))
}

func TestFromDeliveryManualAck(t *testing.T) {
	d := testDriver(t)
	acker := &fakeAcker{}

	tbl, err := pack("id-2", &Item{Options: &Options{AutoAck: false}})
	require.NoError(t, err)

	item := d.fromDelivery(amqp.Delivery{Headers: tbl, Acknowledger: acker})

	require.False(t, item.Options.AutoAck)
	require.NoError(t, item.Options.ack(false))
	require.True(t, acker.acked, "manual ack should delegate to the delivery's Acknowledger")
	require.NoError(t, item.Options.nack(false, false))
	require.True(t, acker.nacked)
}

func TestItemAckDecrementsDelayed(t *testing.T) {
	stopped, delayed := newCounters(0, 3)
	acked := false
	i := &Item{Options: &Options{
		Delay:   10,
		stopped: stopped,
		delayed: delayed,
		ack:     func(bool) error { acked = true; return nil },
	}}

	require.NoError(t, i.Ack())
	require.True(t, acked)
	require.Equal(t, int64(2), delayed.Load(), "a delayed job should decrement the in-flight counter on ack")
}

func TestItemNackDecrementsDelayed(t *testing.T) {
	stopped, delayed := newCounters(0, 3)
	nacked := false
	i := &Item{Options: &Options{
		Delay:   10,
		stopped: stopped,
		delayed: delayed,
		nack:    func(bool, bool) error { nacked = true; return nil },
	}}

	require.NoError(t, i.Nack())
	require.True(t, nacked)
	require.Equal(t, int64(2), delayed.Load())
}

func TestItemRequeueDecrementsDelayed(t *testing.T) {
	stopped, delayed := newCounters(0, 3)
	requeued, acked := false, false
	i := &Item{Options: &Options{
		Delay:     10,
		stopped:   stopped,
		delayed:   delayed,
		ack:       func(bool) error { acked = true; return nil },
		requeueFn: func(context.Context, *Item) error { requeued = true; return nil },
	}}

	require.NoError(t, i.Requeue(nil, 5))
	require.True(t, requeued)
	require.True(t, acked, "requeue acks the previous message to avoid duplicates")
	require.Equal(t, int64(2), delayed.Load())
	require.Equal(t, 5, i.Options.Delay, "requeue overwrites the delay")
}

func TestItemStoppedReturnsError(t *testing.T) {
	stopped, delayed := newCounters(1, 0)
	i := &Item{Options: &Options{stopped: stopped, delayed: delayed}}

	require.ErrorContains(t, i.Ack(), "pipeline is probably stopped")
	require.ErrorContains(t, i.Nack(), "pipeline is probably stopped")
	require.ErrorContains(t, i.Requeue(nil, 0), "pipeline is probably stopped")
	require.ErrorContains(t, i.NackWithOptions(true, 0), "pipeline is probably stopped")
}

func TestItemNackWithOptions(t *testing.T) {
	t.Run("requeue with delay routes through Requeue", func(t *testing.T) {
		stopped, delayed := newCounters(0, 0)
		requeued, acked := false, false
		i := &Item{Options: &Options{
			stopped:   stopped,
			delayed:   delayed,
			ack:       func(bool) error { acked = true; return nil },
			requeueFn: func(context.Context, *Item) error { requeued = true; return nil },
		}}
		require.NoError(t, i.NackWithOptions(true, 5))
		require.True(t, requeued)
		require.True(t, acked)
	})

	t.Run("requeue without delay uses native nack", func(t *testing.T) {
		stopped, delayed := newCounters(0, 0)
		var gotMultiple, gotRequeue, called bool
		i := &Item{Options: &Options{
			stopped: stopped,
			delayed: delayed,
			nack: func(multiple, requeue bool) error {
				called, gotMultiple, gotRequeue = true, multiple, requeue
				return nil
			},
		}}
		require.NoError(t, i.NackWithOptions(true, 0))
		require.True(t, called)
		require.False(t, gotMultiple)
		require.True(t, gotRequeue)
	})

	t.Run("no requeue", func(t *testing.T) {
		stopped, delayed := newCounters(0, 0)
		var gotRequeue, called bool
		i := &Item{Options: &Options{
			stopped: stopped,
			delayed: delayed,
			nack: func(_, requeue bool) error {
				called, gotRequeue = true, requeue
				return nil
			},
		}}
		require.NoError(t, i.NackWithOptions(false, 0))
		require.True(t, called)
		require.False(t, gotRequeue)
	})
}
