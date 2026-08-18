package tests

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"testing"

	"tests/helpers"

	amqpPlugin "github.com/roadrunner-server/amqp/v6"
	jobsProto "github.com/roadrunner-server/api-go/v6/jobs/v1"
	jobState "github.com/roadrunner-server/api-plugins/v6/jobs"
	"github.com/roadrunner-server/informer/v6"
	"github.com/roadrunner-server/jobs/v6"
	"github.com/roadrunner-server/resetter/v6"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/server/v6"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const (
	initAddr = "127.0.0.1:6001"
	pqAddr   = "127.0.0.1:6601"
	otelAddr = "127.0.0.1:6100"
	bugAddr  = "127.0.0.1:1792"
)

func jobsPlugins() []any {
	return []any{
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&amqpPlugin.Plugin{},
	}
}

// boot starts the container with the observed logger and waits for the rpc
// listener, which is the readiness signal the fixed sleeps used to stand in for.
func boot(t *testing.T, cfgPath string, addr string, opts ...helpers.Option) (*helpers.RR, func()) {
	t.Helper()

	return helpers.Start(t, cfgPath, jobsPlugins(),
		append([]helpers.Option{
			helpers.WithObservedLogger(),
			helpers.WithTCPProbe(addr),
		}, opts...)...)
}

// pushAndDrain pushes one job to each pipeline, waits for all of them to be
// processed and destroys the pipelines.
func pushAndDrain(t *testing.T, rr *helpers.RR, addr string, pipes ...string) {
	t.Helper()

	for _, p := range pipes {
		helpers.PushToPipe(p, false, addr)(t)
	}

	rr.WaitLog(t, "job was processed successfully", len(pipes))

	helpers.DestroyPipelines(addr, pipes...)(t)

	rr.RequireLogCount(t, "job was pushed successfully", len(pipes))
	rr.RequireLogCount(t, "job was processed successfully", len(pipes))
	rr.RequireLogCount(t, "pipeline was stopped", len(pipes))
	rr.RequireLogCount(t, "delivery channel was closed, leaving the AMQP listener", len(pipes))
}

// TestBoots covers the current config schema.
func TestBoots(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-init.yaml", initAddr)

	rr.RequireLogCount(t, "pipeline was started", 2)

	pushAndDrain(t, rr, initAddr, "test-1", "test-2")
}

// TestBootsV2 covers the same round trip through the version 2 config schema,
// which the driver still parses for old setups.
func TestBootsV2(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-init-v2.yaml", initAddr, helpers.WithConfigVersion("2.7"))

	rr.RequireLogCount(t, "pipeline was started", 2)

	pushAndDrain(t, rr, initAddr, "test-1", "test-2")
}

// TestHeaders covers pipelines declared with queue headers.
func TestHeaders(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-headers.yaml", initAddr)

	pushAndDrain(t, rr, initAddr, "test-1", "test-2")
}

// TestFanoutExchange covers two pipelines bound to one fanout exchange, where
// every message reaches both queues regardless of the routing key.
func TestFanoutExchange(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-fanout.yaml", initAddr)

	pushAndDrain(t, rr, initAddr, "test-fanout-1", "test-fanout-2")
}

// TestRoutingQueue covers two pipelines whose routing keys equal their queue
// names on a shared direct exchange: a push to one must not reach the other.
func TestRoutingQueue(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-routing-queue.yaml", initAddr)

	helpers.PushToPipe("test-1", false, initAddr)(t)

	rr.WaitLog(t, "job was processed successfully", 1)
	rr.NeverLog(t, "jobs protocol error")

	helpers.DestroyPipelines(initAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "job was pushed successfully", 1)
	rr.RequireLogCount(t, "job was processed successfully", 1)
	rr.RequireLogCount(t, "pipeline was stopped", 2)
}

// TestXRoutingKeyHeader covers the x-routing-key header, which overrides the
// pipeline's routing key: the job lands on the queue bound to the header's key.
func TestXRoutingKeyHeader(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-xroutingkey.yaml", initAddr)

	client := helpers.NewJobsClient(t, initAddr)
	require.NoError(t, client.Call("jobs.Push", &jobsProto.PushRequest{Job: &jobsProto.Job{
		Job:     "some/php/namespace",
		Id:      "routed-by-header",
		Payload: []byte(`{"hello":"world"}`),
		Headers: map[string]*jobsProto.HeaderValue{
			"x-routing-key": {Value: []string{"super-routing-key"}},
		},
		Options: &jobsProto.Options{Priority: 1, Pipeline: "test-1"},
	}}, &jobsProto.Empty{}))

	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.DestroyPipelines(initAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "job was pushed successfully", 1)
	rr.RequireLogCount(t, "job was processed successfully", 1)
}

// TestReset covers the resetter: the worker pool is rebuilt and the pipelines
// keep processing afterwards.
func TestReset(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-init.yaml", initAddr)

	helpers.PushToPipe("test-1", false, initAddr)(t)
	helpers.PushToPipe("test-2", false, initAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 2)

	helpers.Reset(t, initAddr)

	helpers.PushToPipe("test-1", false, initAddr)(t)
	helpers.PushToPipe("test-2", false, initAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 4)

	helpers.DestroyPipelines(initAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "job was pushed successfully", 4)
	rr.RequireLogCount(t, "job was processed successfully", 4)
}

// TestPriorityQueueBacklog pushes far more jobs than the two slow workers can
// take, so most of them sit in the priority queue until the pipelines are
// destroyed under them.
func TestPriorityQueueBacklog(t *testing.T) {
	const rounds = 100

	rr, _ := boot(t, "configs/.rr-amqp-pq.yaml", pqAddr)

	for range rounds {
		helpers.PushToPipe("test-1-pq", false, pqAddr)(t)
		helpers.PushToPipe("test-2-pq", false, pqAddr)(t)
	}

	rr.RequireLogCount(t, "job was pushed successfully", 2*rounds)

	// both workers have to be busy before the destroy, otherwise the backlog
	// would never form
	rr.WaitLog(t, "job processing was started", 2)

	helpers.DestroyPipelines(pqAddr, "test-1-pq", "test-2-pq")(t)

	rr.RequireLogCount(t, "pipeline was started", 2)
	rr.RequireLogCount(t, "pipeline was stopped", 2)
}

// TestTwentyPipelines boots twenty pipelines against ten pollers and runs one
// job through each, so more pipelines than processor goroutines is covered.
func TestTwentyPipelines(t *testing.T) {
	const pipelines = 20

	rr, _ := boot(t, "configs/.rr-amqp-parallel.yaml", initAddr)

	rr.RequireLogCount(t, "pipeline was started", pipelines)

	names := make([]string, 0, pipelines)
	for i := 1; i <= pipelines; i++ {
		names = append(names, fmt.Sprintf("test-%d", i))
	}

	for _, name := range names {
		helpers.PushToPipe(name, false, initAddr)(t)
	}

	rr.WaitLog(t, "job was processed successfully", pipelines)

	helpers.DestroyPipelines(initAddr, names...)(t)

	rr.RequireLogCount(t, "job was pushed successfully", pipelines)
	rr.RequireLogCount(t, "pipeline was stopped", pipelines)
}

// TestDelayedJobsSurviveResume covers bug 1792: a delayed job pushed to a
// paused pipeline has to be delivered exactly once after the resume, not
// duplicated and not lost.
func TestDelayedJobsSurviveResume(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-bug-1792.yaml", bugAddr)

	helpers.PushToPipeDelayed(bugAddr, "queue1", 3)(t)
	helpers.PushToPipeDelayed(bugAddr, "queue2", 3)(t)
	helpers.ResumePipes(bugAddr, "queue1", "queue2")(t)

	rr.WaitLog(t, "job was processed successfully", 2)

	helpers.DestroyPipelines(bugAddr, "queue1", "queue2")(t)

	rr.RequireLogCount(t, "job was pushed successfully", 2)
	rr.RequireLogCount(t, "job processing was started", 2)
	rr.RequireLogCount(t, "job was processed successfully", 2)
}

// TestDeclareAndConsume declares a pipeline over rpc and drives it through the
// full life cycle, including the calls that must fail: a second resume and a
// pause with no active listener.
func TestDeclareAndConsume(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-declare.yaml", initAddr)

	helpers.DeclarePipe(initAddr, "test-3", nil)(t)
	helpers.ResumePipes(initAddr, "test-3")(t)
	rr.WaitLog(t, "pipeline was resumed", 1)

	helpers.ResumePipesErr(initAddr, "already in the active state", "test-3")(t)

	helpers.PushToPipe("test-3", false, initAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.PausePipelines(initAddr, "test-3")(t)
	rr.WaitLog(t, "pipeline was paused", 1)

	helpers.PausePipelinesErr(initAddr, "no active listeners", "test-3")(t)

	helpers.DestroyPipelines(initAddr, "test-3")(t)

	rr.RequireLogCount(t, "job was processed successfully", 1)
	rr.RequireLogCount(t, "pipeline was stopped", 1)
}

// TestDeclareDurable covers a durable, non exclusive queue declared over rpc.
func TestDeclareDurable(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-declare.yaml", initAddr)

	helpers.DeclarePipe(initAddr, "test-8", map[string]string{"durable": "true"})(t)
	helpers.ResumePipes(initAddr, "test-8")(t)

	helpers.PushToPipe("test-8", false, initAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.PausePipelines(initAddr, "test-8")(t)
	helpers.DestroyPipelines(initAddr, "test-8")(t)

	rr.RequireLogCount(t, "job was processed successfully", 1)
	rr.RequireLogCount(t, "pipeline was stopped", 1)
}

// TestDeclareWithQueueHeaders covers a queue declared with amqp headers.
func TestDeclareWithQueueHeaders(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-headers-declare.yaml", initAddr)

	helpers.DeclarePipe(initAddr, "test-6", map[string]string{
		"exclusive":     "false",
		"durable":       "true",
		"queue_headers": `{"x-queue-mode":"lazy"}`,
	})(t)
	helpers.ResumePipes(initAddr, "test-6")(t)

	helpers.PushToPipe("test-6", false, initAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.PausePipelines(initAddr, "test-6")(t)
	helpers.DestroyPipelines(initAddr, "test-6")(t)

	rr.RequireLogCount(t, "job was processed successfully", 1)
}

// TestRequeueRetriesUntilAck covers the worker that requeues a job with a
// growing attempts header and only acks the fourth delivery. The old test
// slept a flat 25 seconds.
func TestRequeueRetriesUntilAck(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-jobs-err.yaml", initAddr)

	helpers.DeclarePipe(initAddr, "test-4", nil)(t)
	helpers.ResumePipes(initAddr, "test-4")(t)
	helpers.PushToPipe("test-4", false, initAddr)(t)

	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.PausePipelines(initAddr, "test-4")(t)
	helpers.DestroyPipelines(initAddr, "test-4")(t)

	// one original delivery plus the three the worker requeued
	rr.RequireLogCount(t, "job processing was started", 4)
	rr.RequireLogCount(t, "job was re-queued", 3)
	rr.RequireLogCount(t, "job was pushed successfully", 1)
	rr.RequireLogCount(t, "job was processed successfully", 1)
}

// TestStatsTrackDelayed covers the state report. A delayed job pushed to a
// paused pipeline stays counted as delayed, and resuming drains it once the
// delay lapses. The old test slept out the delay.
func TestStatsTrackDelayed(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-declare.yaml", initAddr)

	helpers.DeclarePipe(initAddr, "test-5", nil)(t)
	helpers.ResumePipes(initAddr, "test-5")(t)

	helpers.PushToPipe("test-5", false, initAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.PausePipelines(initAddr, "test-5")(t)
	rr.WaitLog(t, "pipeline was paused", 1)

	helpers.PushToPipe("test-5", false, initAddr)(t)
	helpers.PushToPipeDelayed(initAddr, "test-5", 4)(t)

	queued := helpers.WaitStats(t, initAddr, "test-5", func(s *jobState.State) bool {
		return s.Delayed == 1 && s.Active == 1
	})

	require.Equal(t, "amqp", queued.Driver)
	require.Equal(t, "test-5", queued.Queue)
	require.False(t, queued.Ready)

	helpers.ResumePipes(initAddr, "test-5")(t)

	drained := helpers.WaitStats(t, initAddr, "test-5", func(s *jobState.State) bool {
		return s.Delayed == 0 && s.Active == 0
	})

	require.True(t, drained.Ready)

	rr.WaitLog(t, "job was processed successfully", 3)

	helpers.DestroyPipelines(initAddr, "test-5")(t)

	rr.RequireLogCount(t, "job was processed successfully", 3)
}

// TestBadResponseIsReported covers a worker answering with a payload the jobs
// response handler cannot parse.
func TestBadResponseIsReported(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-init-br.yaml", initAddr)

	helpers.PushToPipe("test-1", false, initAddr)(t)
	helpers.PushToPipe("test-2", false, initAddr)(t)

	rr.WaitLog(t, "response handler error", 2)

	helpers.DestroyPipelines(initAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "response handler error", 2)
	rr.RequireLogCount(t, "pipeline was stopped", 2)
}

// TestNoGlobalSection covers a config with pipelines but no amqp section. The
// plugin disables itself and the container still serves.
func TestNoGlobalSection(t *testing.T) {
	boot(t, "configs/.rr-no-global.yaml", initAddr, helpers.WithLogLevel(slog.LevelError))
}

// TestOTELSpans checks the spans the driver emits around a push and a destroy.
func TestOTELSpans(t *testing.T) {
	tracer := newInMemoryTracer(t)

	rr, _ := boot(t, "configs/.rr-amqp-otel.yaml", otelAddr, helpers.WithPlugin(tracer))

	helpers.PushToPipe("test-1", false, otelAddr)(t)

	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.DestroyPipelines(otelAddr, "test-1")(t)

	rr.RequireLogCount(t, "pipeline was stopped", 1)

	names := make(map[string]struct{})
	for _, s := range tracer.exp.GetSpans() {
		names[s.Name] = struct{}{}
	}

	got := make([]string, 0, len(names))
	for name := range names {
		got = append(got, name)
	}
	slices.Sort(got)

	for _, want := range []string{
		"destroy_pipeline",
		"jobs_listener",
		"amqp_listener",
		"amqp_push",
		"push",
	} {
		require.Contains(t, got, want, "collected spans: %v", got)
	}
}

// inMemoryTracer stands in for the otel plugin, keeping the spans in process.
type inMemoryTracer struct {
	tp  *sdktrace.TracerProvider
	exp *tracetest.InMemoryExporter
}

func newInMemoryTracer(t *testing.T) *inMemoryTracer {
	t.Helper()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	return &inMemoryTracer{tp: tp, exp: exp}
}

func (*inMemoryTracer) Init() error                        { return nil }
func (*inMemoryTracer) Name() string                       { return "inMemoryTracer" }
func (m *inMemoryTracer) Tracer() *sdktrace.TracerProvider { return m.tp }
