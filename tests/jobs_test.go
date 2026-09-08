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

// boot starts the test plugins with log capture and a TCP listener probe.
func boot(t *testing.T, cfgPath string, addr string, opts ...helpers.Option) (*helpers.RR, func()) {
	t.Helper()

	return helpers.Start(t, cfgPath, jobsPlugins(),
		append([]helpers.Option{
			helpers.WithObservedLogger(),
			helpers.WithTCPProbe(addr),
		}, opts...)...)
}

// pushAndDrain pushes one job per pipeline and waits for processing before pipeline removal.
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

func TestBoots(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-init.yaml", initAddr)

	rr.RequireLogCount(t, "pipeline was started", 2)

	pushAndDrain(t, rr, initAddr, "test-1", "test-2")
}

func TestHeaders(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-headers.yaml", initAddr)

	pushAndDrain(t, rr, initAddr, "test-1", "test-2")
}

// TestFanoutExchange checks two pipelines that consume from one shared queue on a fanout exchange. Each push adds one message to that queue. See https://www.rabbitmq.com/tutorials/amqp-concepts#exchange-fanout.
func TestFanoutExchange(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-fanout.yaml", initAddr)

	pushAndDrain(t, rr, initAddr, "test-fanout-1", "test-fanout-2")
}

// TestRoutingQueue uses queue names as routing keys on a shared direct exchange. A message must reach only its target queue. See https://www.rabbitmq.com/tutorials/amqp-concepts#exchange-direct.
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

// TestXRoutingKeyHeader checks that x-routing-key overrides the pipeline's routing key.
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

// TestReset checks job processing before and after a worker reset.
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

// TestPriorityQueueBacklog destroys pipelines while slow workers process jobs and other jobs wait in the jobs priority queue.
func TestPriorityQueueBacklog(t *testing.T) {
	const rounds = 100

	rr, _ := boot(t, "configs/.rr-amqp-pq.yaml", pqAddr)

	for range rounds {
		helpers.PushToPipe("test-1-pq", false, pqAddr)(t)
		helpers.PushToPipe("test-2-pq", false, pqAddr)(t)
	}

	rr.RequireLogCount(t, "job was pushed successfully", 2*rounds)

	// Pipeline destruction must overlap job processing.
	rr.WaitLog(t, "job processing was started", 2)

	helpers.DestroyPipelines(pqAddr, "test-1-pq", "test-2-pq")(t)

	rr.RequireLogCount(t, "pipeline was started", 2)
	rr.RequireLogCount(t, "pipeline was stopped", 2)
}

// TestTwentyPipelines uses more pipelines than pollers.
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

// TestDelayedJobsSurviveResume checks delayed-job processing after pipeline resume.
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

func TestDeclareWithQueueHeaders(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-headers-declare.yaml", initAddr)

	helpers.DeclarePipe(initAddr, "test-6", map[string]string{
		"exclusive":     "false",
		"durable":       "true",
		"queue_headers": `{"rr_connection":"unknown","x-queue-mode":"lazy"}`,
	})(t)
	helpers.ResumePipes(initAddr, "test-6")(t)

	helpers.PushToPipe("test-6", false, initAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.PausePipelines(initAddr, "test-6")(t)
	helpers.DestroyPipelines(initAddr, "test-6")(t)

	rr.RequireLogCount(t, "job was processed successfully", 1)
}

// TestRequeueRetriesUntilAck checks that the worker requeues the first three deliveries and acknowledges the fourth.
func TestRequeueRetriesUntilAck(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-jobs-err.yaml", initAddr)

	helpers.DeclarePipe(initAddr, "test-4", nil)(t)
	helpers.ResumePipes(initAddr, "test-4")(t)
	helpers.PushToPipe("test-4", false, initAddr)(t)

	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.PausePipelines(initAddr, "test-4")(t)
	helpers.DestroyPipelines(initAddr, "test-4")(t)

	// One initial delivery plus three retries.
	rr.RequireLogCount(t, "job processing was started", 4)
	rr.RequireLogCount(t, "job was re-queued", 3)
	rr.RequireLogCount(t, "job was pushed successfully", 1)
	rr.RequireLogCount(t, "job was processed successfully", 1)
}

// TestStatsTrackDelayed checks delayed and queued counts while a pipeline is paused and after it resumes.
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

func TestBadResponseIsReported(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-amqp-init-br.yaml", initAddr)

	helpers.PushToPipe("test-1", false, initAddr)(t)
	helpers.PushToPipe("test-2", false, initAddr)(t)

	rr.WaitLog(t, "response handler error", 2)

	helpers.DestroyPipelines(initAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "response handler error", 2)
	rr.RequireLogCount(t, "pipeline was stopped", 2)
}

// TestNoGlobalSection checks that the container starts with the AMQP plugin disabled.
func TestNoGlobalSection(t *testing.T) {
	boot(t, "configs/.rr-no-global.yaml", initAddr, helpers.WithLogLevel(slog.LevelError))
}

// TestOTELSpans checks span names for job publishing, consumption, and pipeline removal. See https://opentelemetry.io/docs/concepts/signals/traces/#spans.
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
