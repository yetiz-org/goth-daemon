package kkdaemon

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lifecycleTestTimeout = 2 * time.Second

type lifecycleTrackingDaemon struct {
	DefaultDaemon
	startCount int32
	stopCount  int32
}

func (d *lifecycleTrackingDaemon) Start() {
	atomic.AddInt32(&d.startCount, 1)
}

func (d *lifecycleTrackingDaemon) Stop(sig os.Signal) {
	atomic.AddInt32(&d.stopCount, 1)
}

type legacyBlockingStartDaemon struct {
	DefaultDaemon
	entered        chan struct{}
	release        chan struct{}
	stopCalled     chan struct{}
	stopOnce       sync.Once
	startReturned  int32
	stopCount      int32
	concurrentStop int32
}

func (d *legacyBlockingStartDaemon) Start() {
	close(d.entered)
	<-d.release
	atomic.StoreInt32(&d.startReturned, 1)
}

func (d *legacyBlockingStartDaemon) Stop(sig os.Signal) {
	if atomic.LoadInt32(&d.startReturned) == 0 {
		atomic.StoreInt32(&d.concurrentStop, 1)
	}
	atomic.AddInt32(&d.stopCount, 1)
	d.stopOnce.Do(func() {
		close(d.stopCalled)
	})
}

type contextStartDaemon struct {
	DefaultDaemon
	entered                   chan struct{}
	canceled                  chan struct{}
	stopCalled                chan struct{}
	stopOnce                  sync.Once
	returnNil                 bool
	returnImmediately         bool
	returnErrorImmediately    bool
	panicStop                 bool
	retainedDone              <-chan struct{}
	legacyStartCount          int32
	stopCount                 int32
	contextCanceledBeforeStop int32
}

func (d *contextStartDaemon) Start() {
	atomic.AddInt32(&d.legacyStartCount, 1)
}

func (d *contextStartDaemon) StartContext(ctx context.Context) (rtErr error) {
	close(d.entered)
	if d.returnImmediately {
		d.retainedDone = ctx.Done()
		return nil
	}
	if d.returnErrorImmediately {
		d.retainedDone = ctx.Done()
		return errors.New("context start failed")
	}

	<-ctx.Done()
	close(d.canceled)
	if d.returnNil {
		return nil
	}
	return ctx.Err()
}

func (d *contextStartDaemon) Stop(sig os.Signal) {
	atomic.AddInt32(&d.stopCount, 1)
	select {
	case <-d.retainedDone:
		atomic.StoreInt32(&d.contextCanceledBeforeStop, 1)
	default:
	}
	if d.panicStop {
		panic("context stop panic")
	}
	d.stopOnce.Do(func() {
		close(d.stopCalled)
	})
}

type panicIntervalDaemon struct {
	DefaultTimerDaemon
}

func (d *panicIntervalDaemon) Interval() (interval time.Duration) {
	panic("interval panic")
}

type blockingLoopDaemon struct {
	DefaultTimerDaemon
	entered    chan struct{}
	release    chan struct{}
	stopCalled chan struct{}
	enterOnce  sync.Once
	stopOnce   sync.Once
}

func (d *blockingLoopDaemon) Interval() (interval time.Duration) {
	return time.Millisecond
}

func (d *blockingLoopDaemon) Loop() (rtErr error) {
	d.enterOnce.Do(func() {
		close(d.entered)
	})
	<-d.release
	return nil
}

func (d *blockingLoopDaemon) Stop(sig os.Signal) {
	d.stopOnce.Do(func() {
		close(d.stopCalled)
	})
}

func awaitLifecycleSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(lifecycleTestTimeout):
		t.Fatal(message)
	}
}

func awaitLifecycleError(t *testing.T, result <-chan error, message string) (rtErr error) {
	t.Helper()
	select {
	case rtErr = <-result:
		return rtErr
	case <-time.After(lifecycleTestTimeout):
		t.Fatal(message)
		return nil
	}
}

func TestDaemonServiceStopDuringLegacyStartLateStopsOnce(t *testing.T) {
	service := NewDaemonService()
	started := &lifecycleTrackingDaemon{DefaultDaemon: DefaultDaemon{name: "started"}}
	blocking := &legacyBlockingStartDaemon{
		DefaultDaemon: DefaultDaemon{name: "blocking"},
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
		stopCalled:    make(chan struct{}),
	}
	later := &lifecycleTrackingDaemon{DefaultDaemon: DefaultDaemon{name: "later"}}
	require.NoError(t, service.RegisterDaemonWithOrder(started, 1))
	require.NoError(t, service.RegisterDaemonWithOrder(blocking, 2))
	require.NoError(t, service.RegisterDaemonWithOrder(later, 3))

	startResult := make(chan error, 1)
	go func() {
		startResult <- service.Start()
	}()
	awaitLifecycleSignal(t, blocking.entered, "blocking Start was not entered")

	stopResult := make(chan error, 1)
	go func() {
		stopResult <- service.Stop(syscall.SIGTERM)
	}()
	require.NoError(t, awaitLifecycleError(t, stopResult, "Stop waited for legacy Start"))
	assert.Equal(t, int32(1), atomic.LoadInt32(&started.stopCount))
	assert.Equal(t, int32(0), atomic.LoadInt32(&blocking.stopCount))
	assert.Equal(t, int32(0), atomic.LoadInt32(&blocking.concurrentStop))
	assert.Equal(t, int32(0), atomic.LoadInt32(&later.startCount))
	service.timerMutex.Lock()
	loopTimer := service.invokeLoopDaemonTimer
	service.timerMutex.Unlock()
	assert.Nil(t, loopTimer)

	close(blocking.release)
	startErr := awaitLifecycleError(t, startResult, "Start did not finish after release")
	require.Error(t, startErr)
	assert.Contains(t, startErr.Error(), "interrupted")
	awaitLifecycleSignal(t, blocking.stopCalled, "late Stop was not called")
	assert.Equal(t, int32(1), atomic.LoadInt32(&blocking.stopCount))
	assert.Equal(t, int32(0), atomic.LoadInt32(&blocking.concurrentStop))
	assert.Equal(t, StateWait, blocking.State())
	assert.Equal(t, int32(0), atomic.LoadInt32(&service.GetDaemon("blocking").started))
}

func TestDaemonServiceStopCancelsContextStart(t *testing.T) {
	service := NewDaemonService()
	daemon := &contextStartDaemon{
		DefaultDaemon: DefaultDaemon{name: "context-error"},
		entered:       make(chan struct{}),
		canceled:      make(chan struct{}),
		stopCalled:    make(chan struct{}),
	}
	require.NoError(t, service.RegisterDaemon(daemon))

	startResult := make(chan error, 1)
	go func() {
		startResult <- service.Start()
	}()
	awaitLifecycleSignal(t, daemon.entered, "StartContext was not entered")

	stopResult := make(chan error, 1)
	go func() {
		stopResult <- service.Stop(syscall.SIGTERM)
	}()
	awaitLifecycleSignal(t, daemon.canceled, "StartContext was not canceled")
	require.NoError(t, awaitLifecycleError(t, stopResult, "Stop did not await StartContext"))
	startErr := awaitLifecycleError(t, startResult, "Start did not return the context error")
	require.EqualError(t, startErr, context.Canceled.Error())
	assert.Equal(t, int32(0), atomic.LoadInt32(&daemon.legacyStartCount))
	assert.Equal(t, int32(0), atomic.LoadInt32(&daemon.stopCount))
	assert.Equal(t, StateWait, daemon.State())
}

func TestDaemonServiceContextStartSuccessAfterCancelStopsOnce(t *testing.T) {
	service := NewDaemonService()
	daemon := &contextStartDaemon{
		DefaultDaemon: DefaultDaemon{name: "context-success"},
		entered:       make(chan struct{}),
		canceled:      make(chan struct{}),
		stopCalled:    make(chan struct{}),
		returnNil:     true,
	}
	require.NoError(t, service.RegisterDaemon(daemon))

	startResult := make(chan error, 1)
	go func() {
		startResult <- service.Start()
	}()
	awaitLifecycleSignal(t, daemon.entered, "StartContext was not entered")

	stopResult := make(chan error, 1)
	go func() {
		stopResult <- service.Stop(syscall.SIGTERM)
	}()
	awaitLifecycleSignal(t, daemon.canceled, "StartContext was not canceled")
	require.NoError(t, awaitLifecycleError(t, stopResult, "Stop did not await successful StartContext"))
	startErr := awaitLifecycleError(t, startResult, "Start did not report interruption")
	require.Error(t, startErr)
	assert.Contains(t, startErr.Error(), "interrupted")
	awaitLifecycleSignal(t, daemon.stopCalled, "successful StartContext was not stopped")
	assert.Equal(t, int32(1), atomic.LoadInt32(&daemon.stopCount))
	assert.Equal(t, StateWait, daemon.State())
}

func TestDaemonServiceContextStartNormalStop(t *testing.T) {
	service := NewDaemonService()
	daemon := &contextStartDaemon{
		DefaultDaemon:     DefaultDaemon{name: "context-normal"},
		entered:           make(chan struct{}),
		canceled:          make(chan struct{}),
		stopCalled:        make(chan struct{}),
		returnImmediately: true,
	}
	require.NoError(t, service.RegisterDaemon(daemon))
	require.NoError(t, service.Start())
	awaitLifecycleSignal(t, daemon.entered, "StartContext was not entered")
	assert.Equal(t, int32(0), atomic.LoadInt32(&daemon.legacyStartCount))

	select {
	case <-daemon.retainedDone:
		t.Fatal("startup context was canceled before Stop")
	default:
	}

	require.NoError(t, service.Stop(syscall.SIGTERM))
	awaitLifecycleSignal(t, daemon.stopCalled, "Stop was not called")
	awaitLifecycleSignal(t, daemon.retainedDone, "startup context was not canceled by Stop")
	assert.Equal(t, int32(1), atomic.LoadInt32(&daemon.stopCount))
	assert.Equal(t, int32(1), atomic.LoadInt32(&daemon.contextCanceledBeforeStop))
	assert.Equal(t, StateWait, daemon.State())
}

func TestDaemonServiceContextStartErrorCancelsContext(t *testing.T) {
	service := NewDaemonService()
	daemon := &contextStartDaemon{
		DefaultDaemon:          DefaultDaemon{name: "context-immediate-error"},
		entered:                make(chan struct{}),
		canceled:               make(chan struct{}),
		stopCalled:             make(chan struct{}),
		returnErrorImmediately: true,
	}
	require.NoError(t, service.RegisterDaemon(daemon))

	startErr := service.Start()
	require.EqualError(t, startErr, "context start failed")
	awaitLifecycleSignal(t, daemon.retainedDone, "failed startup context was not canceled")
	assert.Equal(t, int32(0), atomic.LoadInt32(&daemon.stopCount))
	assert.Equal(t, StateStart, daemon.State())
	require.NoError(t, service.Stop(syscall.SIGTERM))
}

func TestDaemonServiceStopDaemonReturnsContextStopPanic(t *testing.T) {
	service := NewDaemonService()
	daemon := &contextStartDaemon{
		DefaultDaemon: DefaultDaemon{name: "context-stop-panic"},
		entered:       make(chan struct{}),
		canceled:      make(chan struct{}),
		stopCalled:    make(chan struct{}),
		returnNil:     true,
		panicStop:     true,
	}
	require.NoError(t, service.RegisterDaemon(daemon))
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)

	startResult := make(chan error, 1)
	go func() {
		startResult <- service.StartDaemon(entity)
	}()
	awaitLifecycleSignal(t, daemon.entered, "StartContext was not entered")

	stopErr := service.StopDaemon(entity, syscall.SIGTERM)
	require.Error(t, stopErr)
	assert.Contains(t, stopErr.Error(), "context stop panic")
	startErr := awaitLifecycleError(t, startResult, "StartDaemon did not report interruption")
	require.Error(t, startErr)
	assert.Contains(t, startErr.Error(), "interrupted")
	assert.Equal(t, int32(1), atomic.LoadInt32(&daemon.stopCount))
	assert.Equal(t, StateWait, daemon.State())
}

func TestDaemonServiceUnregisterWaitsForLegacyLateStop(t *testing.T) {
	service := NewDaemonService()
	daemon := &legacyBlockingStartDaemon{
		DefaultDaemon: DefaultDaemon{name: "unregister-blocking"},
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
		stopCalled:    make(chan struct{}),
	}
	require.NoError(t, service.RegisterDaemon(daemon))
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)

	startResult := make(chan error, 1)
	go func() {
		startResult <- service.StartDaemon(entity)
	}()
	awaitLifecycleSignal(t, daemon.entered, "legacy Start was not entered")

	unregisterResult := make(chan error, 1)
	go func() {
		unregisterResult <- service.UnregisterDaemon(daemon.Name())
	}()
	require.Eventually(t, func() (requested bool) {
		entity.lifecycle.mutex.Lock()
		defer entity.lifecycle.mutex.Unlock()
		return entity.lifecycle.cycle != nil && entity.lifecycle.cycle.stopRequested
	}, lifecycleTestTimeout, time.Millisecond)

	select {
	case err := <-unregisterResult:
		t.Fatalf("UnregisterDaemon returned before deferred Stop: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	assert.Same(t, entity, service.GetDaemon(daemon.Name()))
	replacement := &lifecycleTrackingDaemon{DefaultDaemon: DefaultDaemon{name: daemon.Name()}}
	require.Error(t, service.RegisterDaemon(replacement))

	close(daemon.release)
	startErr := awaitLifecycleError(t, startResult, "StartDaemon did not finish after release")
	require.Error(t, startErr)
	assert.Contains(t, startErr.Error(), "interrupted")
	require.NoError(t, awaitLifecycleError(t, unregisterResult, "UnregisterDaemon did not finish after deferred Stop"))
	awaitLifecycleSignal(t, daemon.stopCalled, "deferred Stop was not called")
	assert.Nil(t, service.GetDaemon(daemon.Name()))
	require.NoError(t, service.RegisterDaemon(replacement))
}

func TestDaemonServiceUnregisterWaitsForPreviouslyDeferredLegacyStop(t *testing.T) {
	service := NewDaemonService()
	daemon := &legacyBlockingStartDaemon{
		DefaultDaemon: DefaultDaemon{name: "unregister-preclaimed"},
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
		stopCalled:    make(chan struct{}),
	}
	require.NoError(t, service.RegisterDaemon(daemon))
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)

	startResult := make(chan error, 1)
	go func() {
		startResult <- service.Start()
	}()
	awaitLifecycleSignal(t, daemon.entered, "legacy Start was not entered")
	require.NoError(t, service.Stop(syscall.SIGTERM))

	unregisterResult := make(chan error, 1)
	go func() {
		unregisterResult <- service.UnregisterDaemon(daemon.Name())
	}()
	select {
	case err := <-unregisterResult:
		t.Fatalf("UnregisterDaemon returned before the existing deferred Stop: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	assert.Same(t, entity, service.GetDaemon(daemon.Name()))
	replacement := &lifecycleTrackingDaemon{DefaultDaemon: DefaultDaemon{name: daemon.Name()}}
	require.Error(t, service.RegisterDaemon(replacement))

	close(daemon.release)
	startErr := awaitLifecycleError(t, startResult, "Start did not finish after release")
	require.Error(t, startErr)
	assert.Contains(t, startErr.Error(), "interrupted")
	require.NoError(t, awaitLifecycleError(t, unregisterResult, "UnregisterDaemon did not finish after deferred Stop"))
	awaitLifecycleSignal(t, daemon.stopCalled, "deferred Stop was not called")
	assert.Nil(t, service.GetDaemon(daemon.Name()))
	require.NoError(t, service.RegisterDaemon(replacement))
}

func TestDaemonServiceSchedulingPanicRemainsCaught(t *testing.T) {
	service := NewDaemonService()
	daemon := &panicIntervalDaemon{DefaultTimerDaemon: DefaultTimerDaemon{DefaultDaemon: DefaultDaemon{name: "panic-interval"}}}
	require.NoError(t, service.RegisterDaemon(daemon))
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)

	var startErr error
	require.NotPanics(t, func() {
		startErr = service.StartDaemon(entity)
	})
	require.Error(t, startErr)
	assert.Contains(t, startErr.Error(), "interval panic")
	assert.Equal(t, int32(0), atomic.LoadInt32(&entity.started))
	assert.Equal(t, StateStart, daemon.State())
}

func TestDaemonServiceLoopCompletionDoesNotResurrectState(t *testing.T) {
	service := NewDaemonService()
	daemon := &blockingLoopDaemon{
		DefaultTimerDaemon: DefaultTimerDaemon{DefaultDaemon: DefaultDaemon{name: "blocking-loop"}},
		entered:            make(chan struct{}),
		release:            make(chan struct{}),
		stopCalled:         make(chan struct{}),
	}
	require.NoError(t, service.RegisterDaemon(daemon))
	require.NoError(t, service.Start())
	awaitLifecycleSignal(t, daemon.entered, "Loop was not entered")

	require.NoError(t, service.Stop(syscall.SIGTERM))
	awaitLifecycleSignal(t, daemon.stopCalled, "Stop was not called")
	assert.Equal(t, StateWait, daemon.State())

	close(daemon.release)
	assert.Never(t, func() (resurrected bool) {
		return daemon.State() != StateWait
	}, 100*time.Millisecond, time.Millisecond)
}
