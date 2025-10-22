package kkdaemon

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDaemonServiceCreation tests the creation and initialization of DaemonService
func TestDaemonServiceCreation(t *testing.T) {
	service := NewDaemonService()
	
	// Check basic properties
	assert.True(t, service.StopWhenKill)
	assert.NotNil(t, service.sig)
	assert.NotNil(t, service.stopFuture)
	assert.NotNil(t, service.shutdownFuture)
	assert.NotNil(t, service.loopInvokerReload)
	assert.Equal(t, int32(0), service.state)
	assert.Equal(t, int32(0), service.shutdownState)
	assert.Equal(t, 0, service.orderIndex)
	
	// Check initial state
	assert.False(t, service.IsShutdown())
}

// TestDaemonServiceRegisterAndUnregister tests the register and unregister functionality
func TestDaemonServiceRegisterAndUnregister(t *testing.T) {
	service := NewDaemonService()
	daemon := &testServiceDaemon{}
	
	// Test registration
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	// Check registration result
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)
	assert.Equal(t, daemon.Name(), entity.Name)
	assert.Equal(t, 1, entity.Order)
	assert.Equal(t, StateWait, daemon.State())
	
	// Start daemon then unregister (only running daemons can be properly stopped)
	err = service.StartDaemon(entity)
	require.NoError(t, err)
	assert.Equal(t, StateStart, daemon.State())
	
	// Test unregister
	err = service.UnregisterDaemon(daemon.Name())
	require.NoError(t, err)
	
	// Check unregister result
	entity = service.GetDaemon(daemon.Name())
	assert.Nil(t, entity)
	assert.True(t, daemon.stopCalled)
}

// TestDaemonServiceStartStop tests the start and stop of the service
func TestDaemonServiceStartStop(t *testing.T) {
	service := NewDaemonService()
	daemon := &testServiceDaemon{}
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	// Test start service
	err = service.Start()
	require.NoError(t, err)
	assert.Equal(t, StateStart, daemon.State())
	assert.True(t, daemon.startCalled)
	
	// Test repeated start (should fail)
	err = service.Start()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in WAIT state")
	
	// Test stop service
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)
	assert.Equal(t, StateWait, daemon.State())
	assert.True(t, daemon.stopCalled)
	assert.Equal(t, syscall.SIGTERM, daemon.stopSignal)
}

// TestDaemonServiceStartStopIndividualDaemon tests the start and stop of individual daemon
func TestDaemonServiceStartStopIndividualDaemon(t *testing.T) {
	service := NewDaemonService()
	daemon := &testServiceDaemon{}
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)
	
	// Test start individual daemon
	err2 := service.StartDaemon(entity)
	require.NoError(t, err2)
	assert.Equal(t, StateStart, daemon.State())
	assert.True(t, daemon.startCalled)
	
	// Test repeated start (should fail)
	err2 = service.StartDaemon(entity)
	require.Error(t, err2)
	
	// Test stop individual daemon
	err3 := service.StopDaemon(entity, syscall.SIGINT)
	require.NoError(t, err3)
	assert.Equal(t, StateWait, daemon.State())
	assert.True(t, daemon.stopCalled)
	assert.Equal(t, syscall.SIGINT, daemon.stopSignal)
}

// TestDaemonServiceSignalHandling tests the signal handling mechanism
func TestDaemonServiceSignalHandling(t *testing.T) {
	service := NewDaemonService()
	daemon := &testServiceDaemon{}
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	err = service.Start()
	require.NoError(t, err)
	
	// Test graceful shutdown signal
	go func() {
		time.Sleep(100 * time.Millisecond)
		service.ShutdownGracefully()
	}()
	
	// Wait for shutdown to complete
	select {
	case <-service.ShutdownFuture().Done():
		assert.True(t, service.IsShutdown())
		assert.True(t, daemon.stopCalled)
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown timeout")
	}
}

// TestDaemonServiceStopWhenKillFlag tests the behavior of StopWhenKill flag
func TestDaemonServiceStopWhenKillFlag(t *testing.T) {
	service := NewDaemonService()
	service.StopWhenKill = false
	daemon := &testServiceDaemon{}
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	err = service.Start()
	require.NoError(t, err)
	
	// When StopWhenKill is false, sending SIGTERM should not stop daemon
	go func() {
		time.Sleep(100 * time.Millisecond)
		service.sig <- syscall.SIGTERM
	}()
	
	// Wait for signal handling to complete
	select {
	case <-service.ShutdownFuture().Done():
		assert.True(t, service.IsShutdown())
		assert.False(t, daemon.stopCalled) // daemon should not be stopped
	case <-time.After(2 * time.Second):
		t.Fatal("Signal handling timeout")
	}
}

// TestDaemonServicePanicRecovery tests the panic recovery mechanism
func TestDaemonServicePanicRecovery(t *testing.T) {
	service := NewDaemonService()
	daemon := &panicDaemon{}
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)
	
	// Start daemon that will cause panic
	err2 := service.StartDaemon(entity)
	require.Error(t, err2) // should catch panic
	
	// Daemon state should be restored to StateStart
	assert.Equal(t, StateStart, daemon.State())
}

// TestDaemonServiceLoopInvoker tests the loop invoker functionality
func TestDaemonServiceLoopInvoker(t *testing.T) {
	service := NewDaemonService()
	timerDaemon := &testTimerLoopDaemon{}
	
	err := service.RegisterDaemon(timerDaemon)
	require.NoError(t, err)
	
	err = service.Start()
	require.NoError(t, err)
	
	// Wait for several loop executions
	time.Sleep(250 * time.Millisecond)
	
	// Check if Loop method was called
	assert.True(t, atomic.LoadInt32(&timerDaemon.loopCount) > 0)
	
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)
}

// TestDaemonServiceConcurrentOperations tests the concurrency safety of DaemonService
func TestDaemonServiceConcurrentOperations(t *testing.T) {
	service := NewDaemonService()
	var wg sync.WaitGroup
	errors := make(chan error, 20)
	
	// Concurrent register and unregister operations
	for i := 0; i < 10; i++ {
		wg.Add(2)
		
		// Register daemon
		go func(index int) {
			defer wg.Done()
			daemon := &testServiceDaemon{name: fmt.Sprintf("concurrent_service_%d", index)}
			if err := service.RegisterDaemon(daemon); err != nil {
				errors <- err
			}
		}(i)
		
		// Attempt to unregister
		go func(index int) {
			defer wg.Done()
			time.Sleep(50 * time.Millisecond) // slightly delay to increase race condition
			if err := service.UnregisterDaemon(fmt.Sprintf("concurrent_service_%d", index)); err != nil {
				errors <- err
			}
		}(i)
	}
	
	wg.Wait()
	close(errors)
	
	// Check errors (some errors are expected, like trying to unregister non-existent daemon)
	errorCount := 0
	for err := range errors {
		if err != nil {
			errorCount++
		}
	}
	
	// Due to concurrent operations, there may be some errors, but not too many
	assert.True(t, errorCount <= 10, "Too many errors in concurrent operations")
}

// testServiceDaemon is a test daemon for testing DaemonService
type testServiceDaemon struct {
	DefaultDaemon
	name        string
	startCalled bool
	stopCalled  bool
	stopSignal  os.Signal
}

func (d *testServiceDaemon) Name() string {
	if d.name != "" {
		return d.name
	}
	return "testServiceDaemon"
}

func (d *testServiceDaemon) Start() {
	d.startCalled = true
}

func (d *testServiceDaemon) Stop(sig os.Signal) {
	d.stopCalled = true
	d.stopSignal = sig
}

// panicDaemon is a daemon for testing panic recovery
type panicDaemon struct {
	DefaultDaemon
}

func (d *panicDaemon) Start() {
	panic("test panic in start")
}

func (d *panicDaemon) Stop(sig os.Signal) {
	// Empty implementation
}

// testTimerLoopDaemon is a timer daemon for testing loop invoker
type testTimerLoopDaemon struct {
	DefaultTimerDaemon
	loopCount int32
}

func (d *testTimerLoopDaemon) Interval() time.Duration {
	return 50 * time.Millisecond // Short interval for testing
}

func (d *testTimerLoopDaemon) Loop() error {
	atomic.AddInt32(&d.loopCount, 1)
	return nil
}

func (d *testTimerLoopDaemon) Start() {
	// Empty implementation
}

func (d *testTimerLoopDaemon) Stop(sig os.Signal) {
	// Empty implementation
}

// TestDaemonServicePartialStartupFailure tests shutdown behavior when partial startup failure occurs
// Scenario: There are 6 daemons (1,2,3,4,5,6), the 3rd one fails to start
// Expected: Only daemons 1 and 2 will be stopped, 3,4,5,6 will not be stopped
func TestDaemonServicePartialStartupFailure(t *testing.T) {
	service := NewDaemonService()
	
	// Create 6 daemons, the 3rd one will panic on start
	daemons := make([]*trackingDaemon, 6)
	for i := 0; i < 6; i++ {
		shouldPanic := (i == 2) // The 3rd daemon (index 2) will panic
		daemons[i] = &trackingDaemon{
			name:        fmt.Sprintf("daemon_%d", i+1),
			shouldPanic: shouldPanic,
		}
		err := service.RegisterDaemonWithOrder(daemons[i], i+1)
		require.NoError(t, err)
	}
	
	// Try to start all daemons, should fail at the 3rd one
	err := service.Start()
	require.Error(t, err, "Start should fail when daemon 3 panics")
	assert.Contains(t, err.Error(), "panic in start")
	
	// Verify startup status: only the first 2 should start successfully
	assert.True(t, daemons[0].startCalled, "daemon 1 should be started")
	assert.True(t, daemons[1].startCalled, "daemon 2 should be started")
	assert.True(t, daemons[2].startCalled, "daemon 3 should attempt to start (but panic)")
	assert.False(t, daemons[3].startCalled, "daemon 4 should not be started")
	assert.False(t, daemons[4].startCalled, "daemon 5 should not be started")
	assert.False(t, daemons[5].startCalled, "daemon 6 should not be started")
	
	// Call ShutdownGracefully to clean up
	service.ShutdownGracefully()
	
	// Wait for shutdown to complete
	select {
	case <-service.ShutdownFuture().Done():
		// Verify stop status: only successfully started daemons (1, 2) should be stopped
		assert.True(t, daemons[0].stopCalled, "daemon 1 should be stopped")
		assert.True(t, daemons[1].stopCalled, "daemon 2 should be stopped")
		assert.False(t, daemons[2].stopCalled, "daemon 3 should not be stopped (never fully started)")
		assert.False(t, daemons[3].stopCalled, "daemon 4 should not be stopped (never started)")
		assert.False(t, daemons[4].stopCalled, "daemon 5 should not be stopped (never started)")
		assert.False(t, daemons[5].stopCalled, "daemon 6 should not be stopped (never started)")
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown timeout")
	}
}

// trackingDaemon is a test daemon for tracking start and stop calls
type trackingDaemon struct {
	DefaultDaemon
	name        string
	shouldPanic bool
	startCalled bool
	stopCalled  bool
}

func (d *trackingDaemon) Name() string {
	return d.name
}

func (d *trackingDaemon) Start() {
	d.startCalled = true
	if d.shouldPanic {
		panic("panic in start")
	}
}

func (d *trackingDaemon) Stop(sig os.Signal) {
	d.stopCalled = true
}
