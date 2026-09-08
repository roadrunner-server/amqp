package helpers

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	mocklogger "tests/mock"

	jobState "github.com/roadrunner-server/api-plugins/v6/jobs"
	"github.com/roadrunner-server/config/v6"
	"github.com/roadrunner-server/endure/v2"
	"github.com/roadrunner-server/logger/v6"
	"github.com/stretchr/testify/require"
)

const (
	defaultConfigVersion = "v2024.2.0"
	probeTimeout         = time.Second * 30
	probeTick            = time.Millisecond * 20
	probeDial            = time.Second
	logTimeout           = time.Second * 60
	logTick              = time.Millisecond * 50
	// The timeout must exceed the longest job delay in these tests.
	statsTimeout    = time.Second * 60
	statsTick       = time.Millisecond * 100
	negativeWindow  = time.Second * 3
	shutdownTimeout = time.Second * 60
)

type bootCfg struct {
	logLevel slog.Level
	logger   loggerKind
	probe    func(ctx context.Context) bool
	extra    []any
}

type loggerKind int

const (
	realLogger loggerKind = iota
	observedLogger
)

// Option customizes the container built by Start.
type Option func(*bootCfg)

// WithLogLevel sets the container log level. The default is slog.LevelDebug.
func WithLogLevel(l slog.Level) Option {
	return func(b *bootCfg) { b.logLevel = l }
}

// WithObservedLogger captures log records in memory and exposes them through RR.Logs.
func WithObservedLogger() Option {
	return func(b *bootCfg) { b.logger = observedLogger }
}

// WithPlugin registers an additional plugin.
func WithPlugin(p any) Option {
	return func(b *bootCfg) { b.extra = append(b.extra, p) }
}

// WithTCPProbe makes Start wait until addr accepts a TCP connection. The probe checks only the listener.
func WithTCPProbe(addr string) Option {
	return func(b *bootCfg) {
		b.probe = func(ctx context.Context) bool {
			d := net.Dialer{Timeout: probeDial}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return false
			}

			_ = conn.Close()
			return true
		}
	}
}

// RR is a running container.
type RR struct {
	// Logs is nil unless WithObservedLogger is set.
	Logs *mocklogger.ObservedLogs
}

// CountLog returns the number of captured records that contain snippet.
func (rr *RR) CountLog(snippet string) int {
	return rr.Logs.FilterMessageSnippet(snippet).Len()
}

// WaitLog waits for at least want records that contain snippet. Job processing can complete after the RPC call returns.
func (rr *RR) WaitLog(t *testing.T, snippet string, want int) {
	t.Helper()

	rr.WaitLogWithin(t, snippet, want, logTimeout)
}

// WaitLogWithin waits for at least want matching records within timeout.
func (rr *RR) WaitLogWithin(t *testing.T, snippet string, want int, timeout time.Duration) {
	t.Helper()

	require.Eventually(t, func() bool {
		return rr.CountLog(snippet) >= want
	}, timeout, logTick, "expected at least %d records matching %q, saw %d",
		want, snippet, rr.CountLog(snippet))
}

// RequireLogCount waits for want matching records, then checks the exact count.
func (rr *RR) RequireLogCount(t *testing.T, snippet string, want int) {
	t.Helper()

	rr.WaitLog(t, snippet, want)
	require.Equal(t, want, rr.CountLog(snippet), "records matching %q", snippet)
}

// NeverLog checks that no record contains snippet during negativeWindow.
func (rr *RR) NeverLog(t *testing.T, snippet string) {
	t.Helper()

	require.Never(t, func() bool {
		return rr.CountLog(snippet) > 0
	}, negativeWindow, logTick, "unexpected record matching %q", snippet)
}

// Start initializes and starts the plugins, then waits for the optional probe. It reports asynchronous container errors with t.Errorf and stops the container. These errors mark the test as failed without aborting it.
//
// The returned stop function runs at most once. Start also registers it for test cleanup. Tests can call it before checking shutdown logs.
func Start(t *testing.T, cfgPath string, plugins []any, opts ...Option) (*RR, func()) {
	t.Helper()

	cont, rr, bc := newContainer(t, cfgPath, plugins, opts)
	require.NoError(t, cont.Init())

	// A closing container can still accept connections. Wait for its listener to close before starting this container.
	// Tests that expect Serve to fail use separate ports; see StartExpectServeError.
	if bc.probe != nil {
		require.Eventually(t, func() bool {
			return !bc.probe(context.Background())
		}, probeTimeout, probeTick, "the rpc port is still taken by a previous container")
	}

	ch, err := cont.Serve()
	require.NoError(t, err)

	stopCont := sync.OnceValue(cont.Stop)
	done := make(chan struct{})
	wg := &sync.WaitGroup{}

	wg.Go(func() {
		for {
			select {
			case res := <-ch:
				if res == nil {
					return
				}
				t.Errorf("plugin %s reported an error: %v", res.VertexID, res.Error)
				if errS := stopCont(); errS != nil {
					t.Errorf("container stop: %v", errS)
				}
			case <-done:
				if errS := stopCont(); errS != nil {
					t.Errorf("container stop: %v", errS)
				}
				return
			}
		}
	})

	// The error-reporting goroutine must finish before the test ends.
	stop := sync.OnceFunc(func() {
		close(done)
		wg.Wait()
	})
	t.Cleanup(stop)

	if bc.probe != nil {
		require.Eventually(t, func() bool { return bc.probe(t.Context()) }, probeTimeout, probeTick, "rpc listener did not become ready")
	}

	return rr, stop
}

// StartExpectServeError requires Init to succeed and Serve to fail. It returns the Serve error.
//
// The container remains running. Callers must use a separate listener port.
func StartExpectServeError(t *testing.T, cfgPath string, plugins []any, opts ...Option) error {
	t.Helper()

	cont, _, _ := newContainer(t, cfgPath, plugins, opts)
	require.NoError(t, cont.Init())

	_, err := cont.Serve()
	require.Error(t, err)

	return err
}

// newContainer registers the configuration, logger, and caller's plugins. The caller must initialize the container.
func newContainer(t *testing.T, cfgPath string, plugins []any, opts []Option) (*endure.Endure, *RR, *bootCfg) {
	t.Helper()

	bc := &bootCfg{logLevel: slog.LevelDebug}
	for _, o := range opts {
		o(bc)
	}

	rr := &RR{}
	all := make([]any, 0, 2+len(plugins)+len(bc.extra))
	all = append(all, &config.Plugin{Version: defaultConfigVersion, Path: cfgPath})

	switch bc.logger {
	case realLogger:
		all = append(all, &logger.Plugin{})
	case observedLogger:
		l, obs := mocklogger.SlogTestLogger(slog.LevelDebug)
		rr.Logs = obs
		all = append(all, l)
	}

	all = append(all, bc.extra...)

	cont := endure.New(bc.logLevel, endure.GracefulShutdownTimeout(shutdownTimeout))
	require.NoError(t, cont.RegisterAll(append(all, plugins...)...))

	return cont, rr, bc
}

// WaitStats waits until the pipeline state satisfies want, then returns that state.
func WaitStats(t *testing.T, address string, pipeline string, want func(*jobState.State) bool) *jobState.State {
	t.Helper()

	var state *jobState.State
	require.Eventually(t, func() bool {
		state = StatsFor(t, address, pipeline)
		return want(state)
	}, statsTimeout, statsTick, "pipeline never reached the expected state, last: %+v", state)

	return state
}
