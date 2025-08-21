package kkdaemon

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Timer daemon for testing
type testLifecycleTimerDaemon struct {
	DefaultTimerDaemon
}

func (d *testLifecycleTimerDaemon) Interval() time.Duration {
	return 100 * time.Millisecond
}

func (d *testLifecycleTimerDaemon) Loop() error {
	return nil
}

// Scheduler daemon for testing
type testLifecycleSchedulerDaemon struct {
	DefaultSchedulerDaemon
}

func (d *testLifecycleSchedulerDaemon) When() CronSyntax {
	return "*/1 * * * * *"
}

func (d *testLifecycleSchedulerDaemon) Loop() error {
	return nil
}

// TestServiceStartupAndShutdownFlow tests the complete service startup and shutdown flow
func TestServiceStartupAndShutdownFlow(t *testing.T) {
	service := NewDaemonService()

	// Create multiple different types of daemons for testing
	var startOrder []string
	var stopOrder []string
	var mu sync.Mutex

	// Create simple daemon
	simpleDaemon := &_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{
			name: "simple1",
		},
		StartFunc: func() {
			mu.Lock()
			startOrder = append(startOrder, "simple1")
			mu.Unlock()
		},
		StopFunc: func(sig os.Signal) {
			mu.Lock()
			stopOrder = append(stopOrder, "simple1")
			mu.Unlock()
		},
	}

	// Create timer daemon
	timerDaemon := &testLifecycleTimerDaemon{
		DefaultTimerDaemon: DefaultTimerDaemon{
			DefaultDaemon: DefaultDaemon{
				name: "timer1",
			},
		},
	}

	// Create scheduler daemon
	schedulerDaemon := &testLifecycleSchedulerDaemon{
		DefaultSchedulerDaemon: DefaultSchedulerDaemon{
			DefaultDaemon: DefaultDaemon{
				name: "scheduler1",
			},
		},
	}

	// Register daemons
	require.NoError(t, service.RegisterDaemon(simpleDaemon))
	require.NoError(t, service.RegisterDaemon(timerDaemon))
	require.NoError(t, service.RegisterDaemon(schedulerDaemon))

	// Test service state transitions
	assert.Equal(t, StateWait, atomic.LoadInt32(&service.state))

	// Start service
	startTime := time.Now()
	err := service.Start()
	require.NoError(t, err)
	startupDuration := time.Since(startTime)

	// Verify service state
	assert.Equal(t, StateStart, atomic.LoadInt32(&service.state))

	// Wait for daemon execution
	time.Sleep(150 * time.Millisecond)

	// Verify startup order
	mu.Lock()
	assert.Contains(t, startOrder, "simple1")
	mu.Unlock()

	// Test shutdown flow
	shutdownTime := time.Now()
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)
	shutdownDuration := time.Since(shutdownTime)

	// Verify service state
	assert.Equal(t, StateWait, atomic.LoadInt32(&service.state))

	// Verify shutdown order
	mu.Lock()
	assert.Contains(t, stopOrder, "simple1")
	mu.Unlock()

	// Performance check: startup and shutdown should complete within reasonable time
	assert.Less(t, startupDuration, 100*time.Millisecond, "Service startup should be fast")
	assert.Less(t, shutdownDuration, 100*time.Millisecond, "Service shutdown should be fast")
}

// TestServiceConcurrentStartStop tests concurrent service start and stop
func TestServiceConcurrentStartStop(t *testing.T) {
	service := NewDaemonService()

	// Add some daemons
	for i := 0; i < 10; i++ {
		daemon := &_SimpleDaemon{
			DefaultDaemon: DefaultDaemon{
				name: fmt.Sprintf("simple_%d", i),
			},
			StartFunc: func() {},
			StopFunc:  func(sig os.Signal) {},
		}
		require.NoError(t, service.RegisterDaemon(daemon))
	}

	var wg sync.WaitGroup
	resultChan := make(chan error, 10)

	// Concurrent start test
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := service.Start()
			resultChan <- err
		}()
	}

	wg.Wait()
	close(resultChan)

	// Only the first start should succeed, others should fail
	successCount := 0
	errorCount := 0
	for err := range resultChan {
		if err == nil {
			successCount++
		} else {
			errorCount++
		}
	}
	assert.Equal(t, 1, successCount, "Only one Start() should succeed")
	assert.Equal(t, 9, errorCount, "Nine Start() calls should fail")

	// Concurrent stop test
	resultChan = make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := service.Stop(syscall.SIGTERM)
			resultChan <- err
		}()
	}

	wg.Wait()
	close(resultChan)

	// Only the first stop should succeed
	successCount = 0
	errorCount = 0
	for err := range resultChan {
		if err == nil {
			successCount++
		} else {
			errorCount++
		}
	}
	assert.Equal(t, 1, successCount, "Only one Stop() should succeed")
	assert.Equal(t, 9, errorCount, "Nine Stop() calls should fail")
}

// TestServiceStateTransitions tests the correctness of service state transitions
func TestServiceStateTransitions(t *testing.T) {
	service := NewDaemonService()

	// Initial state should be WAIT
	assert.Equal(t, StateWait, atomic.LoadInt32(&service.state))

	// Can only start from WAIT state
	err := service.Start()
	require.NoError(t, err)
	assert.Equal(t, StateStart, atomic.LoadInt32(&service.state))

	// Repeated start should fail
	err = service.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not in WAIT state")

	// Can only stop from START state
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)
	assert.Equal(t, StateWait, atomic.LoadInt32(&service.state))

	// Repeated stop should fail
	err = service.Stop(syscall.SIGTERM)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not in START state")
}

// TestServiceRapidStartStop tests the stability of rapid service start/stop cycles
func TestServiceRapidStartStop(t *testing.T) {
	service := NewDaemonService()

	// Add a simple daemon
	daemon := &_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{
			name: "rapid",
		},
		StartFunc: func() {},
		StopFunc:  func(sig os.Signal) {},
	}
	require.NoError(t, service.RegisterDaemon(daemon))

	// Rapid start/stop cycles
	for i := 0; i < 100; i++ {
		err := service.Start()
		require.NoError(t, err, "Start should succeed on iteration %d", i)

		err = service.Stop(syscall.SIGTERM)
		require.NoError(t, err, "Stop should succeed on iteration %d", i)

		// Check if state is correctly reset
		assert.Equal(t, StateWait, atomic.LoadInt32(&service.state))
	}
}

// TestServiceMemoryUsage tests memory usage during service start/stop cycles
func TestServiceMemoryUsage(t *testing.T) {
	service := NewDaemonService()

	// Add multiple daemons
	for i := 0; i < 100; i++ {
		daemon := &_SimpleDaemon{
			DefaultDaemon: DefaultDaemon{
				name: fmt.Sprintf("memory_%d", i),
			},
			StartFunc: func() {},
			StopFunc:  func(sig os.Signal) {},
		}
		require.NoError(t, service.RegisterDaemon(daemon))
	}

	runtime.GC()
	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// Execute multiple start/stop cycles
	for i := 0; i < 50; i++ {
		err := service.Start()
		require.NoError(t, err)

		err = service.Stop(syscall.SIGTERM)
		require.NoError(t, err)
	}

	runtime.GC()
	runtime.ReadMemStats(&m2)

	// Memory growth should be controlled within reasonable limits, avoiding uint64 overflow issues
	var memoryGrowth int64
	if m2.Alloc >= m1.Alloc {
		memoryGrowth = int64(m2.Alloc - m1.Alloc)
	} else {
		// If memory actually decreased (due to GC), record as negative growth
		memoryGrowth = -int64(m1.Alloc - m2.Alloc)
	}
	t.Logf("Memory growth: %d bytes", memoryGrowth)

	// Allow some memory growth, but should not grow indefinitely
	// Use absolute value to check, as memory may decrease due to GC
	if memoryGrowth > 0 {
		assert.Less(t, memoryGrowth, int64(10*1024*1024), "Memory growth should be limited")
	} else {
		t.Logf("Memory actually decreased due to GC, which is expected")
	}
}

// TestServiceGoroutineLeaks tests if the service has goroutine leaks
func TestServiceGoroutineLeaks(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()

	service := NewDaemonService()

	// Add timer daemons (which create goroutines)
	for i := 0; i < 10; i++ {
		daemon := &testLifecycleTimerDaemon{
			DefaultTimerDaemon: DefaultTimerDaemon{
				DefaultDaemon: DefaultDaemon{
					name: fmt.Sprintf("leak_%d", i),
				},
			},
		}
		require.NoError(t, service.RegisterDaemon(daemon))
	}

	// Start and stop multiple times
	for i := 0; i < 10; i++ {
		err := service.Start()
		require.NoError(t, err)

		time.Sleep(50 * time.Millisecond) // Let goroutines run for a while

		err = service.Stop(syscall.SIGTERM)
		require.NoError(t, err)

		time.Sleep(50 * time.Millisecond) // Wait for goroutine cleanup
	}

	// Wait for a while to ensure all goroutines are cleaned up
	time.Sleep(200 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Initial goroutines: %d, Final goroutines: %d", initialGoroutines, finalGoroutines)

	// Allow small amount of goroutine growth (test framework may create some)
	assert.LessOrEqual(t, finalGoroutines-initialGoroutines, 2, "Should not leak goroutines")
}

// TestServiceResourceCleanup tests the completeness of service resource cleanup
func TestServiceResourceCleanup(t *testing.T) {
	service := NewDaemonService()

	// Add various types of daemons
	simpleDaemon := &_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{
			name: "cleanup1",
		},
		StartFunc: func() {},
		StopFunc:  func(sig os.Signal) {},
	}
	timerDaemon := &testLifecycleTimerDaemon{
		DefaultTimerDaemon: DefaultTimerDaemon{
			DefaultDaemon: DefaultDaemon{
				name: "cleanup2",
			},
		},
	}

	require.NoError(t, service.RegisterDaemon(simpleDaemon))
	require.NoError(t, service.RegisterDaemon(timerDaemon))

	// Start service
	err := service.Start()
	require.NoError(t, err)

	// Verify resources have been created
	assert.NotNil(t, service.invokeLoopDaemonTimer)
	assert.NotNil(t, service.loopInvokerReload)
	assert.NotNil(t, service.stopFuture)

	// Stop service
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)

	// Verify resources have been cleaned up
	assert.Nil(t, service.invokeLoopDaemonTimer)
	assert.Equal(t, StateWait, atomic.LoadInt32(&service.state))

	// New stopFuture should have been created
	assert.NotNil(t, service.stopFuture)
}

// testPerformanceDaemon daemon for performance testing
type testPerformanceDaemon struct {
	DefaultDaemon
	executionCount int64
}

func (d *testPerformanceDaemon) Start() {
	atomic.AddInt64(&d.executionCount, 1)
}

func (d *testPerformanceDaemon) Stop(sig os.Signal) {}

// TestServiceStartupPerformance tests service startup performance
func TestServiceStartupPerformance(t *testing.T) {
	service := NewDaemonService()

	// Add large number of daemons to test performance
	daemonCount := 1000
	for i := 0; i < daemonCount; i++ {
		daemon := &testPerformanceDaemon{}
		daemon.setName(fmt.Sprintf("perf_%d", i))
		require.NoError(t, service.RegisterDaemon(daemon))
	}

	// Measure startup time
	startTime := time.Now()
	err := service.Start()
	require.NoError(t, err)
	startupDuration := time.Since(startTime)

	t.Logf("Started %d daemons in %v", daemonCount, startupDuration)

	// Measure stop time
	stopTime := time.Now()
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)
	stopDuration := time.Since(stopTime)

	t.Logf("Stopped %d daemons in %v", daemonCount, stopDuration)

	// Performance requirements: startup and shutdown of large number of daemons should complete within reasonable time
	assert.Less(t, startupDuration, 1*time.Second, "Startup should be fast even with many daemons")
	assert.Less(t, stopDuration, 1*time.Second, "Shutdown should be fast even with many daemons")
}
