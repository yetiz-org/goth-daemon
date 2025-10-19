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

// ============================================================================
// Test Daemons for Order Testing
// ============================================================================

// orderTrackingDaemon tracks start and stop order for verification
type orderTrackingDaemon struct {
	DefaultDaemon
	customName    string
	startOrder    *int32
	stopOrder     *int32
	startSequence *[]string
	stopSequence  *[]string
	mu            *sync.Mutex
}

func newOrderTrackingDaemon(name string, startOrder, stopOrder *int32, startSeq, stopSeq *[]string, mu *sync.Mutex) *orderTrackingDaemon {
	return &orderTrackingDaemon{
		customName:    name,
		startOrder:    startOrder,
		stopOrder:     stopOrder,
		startSequence: startSeq,
		stopSequence:  stopSeq,
		mu:            mu,
	}
}

func (d *orderTrackingDaemon) Name() string {
	return d.customName
}

func (d *orderTrackingDaemon) Start() {
	d.mu.Lock()
	defer d.mu.Unlock()
	order := atomic.AddInt32(d.startOrder, 1)
	*d.startSequence = append(*d.startSequence, fmt.Sprintf("%s:%d", d.customName, order))
}

func (d *orderTrackingDaemon) Stop(sig os.Signal) {
	d.mu.Lock()
	defer d.mu.Unlock()
	order := atomic.AddInt32(d.stopOrder, 1)
	*d.stopSequence = append(*d.stopSequence, fmt.Sprintf("%s:%d", d.customName, order))
}

// ============================================================================
// Test 1: Basic RegisterDaemonWithOrder
// ============================================================================

func TestRegisterDaemonWithOrder_Basic(t *testing.T) {
	service := NewDaemonService()
	daemon := &testServiceDaemon{name: "ordered_daemon"}

	err := service.RegisterDaemonWithOrder(daemon, 10)
	require.NoError(t, err)

	entity := service.GetDaemon("ordered_daemon")
	require.NotNil(t, entity)
	assert.Equal(t, 10, entity.Order)
	assert.Equal(t, StateWait, daemon.State())
}

// ============================================================================
// Test 2: Order Conflict Detection (Panic)
// ============================================================================

func TestRegisterDaemonWithOrder_DuplicateOrderPanic(t *testing.T) {
	service := NewDaemonService()
	daemon1 := &testServiceDaemon{name: "daemon1"}
	daemon2 := &testServiceDaemon{name: "daemon2"}

	// Register first daemon with order 5
	err := service.RegisterDaemonWithOrder(daemon1, 5)
	require.NoError(t, err)

	// Attempt to register second daemon with same order should panic
	assert.Panics(t, func() {
		service.RegisterDaemonWithOrder(daemon2, 5)
	}, "Expected panic when registering daemon with duplicate order")

	// Verify first daemon is still registered
	entity1 := service.GetDaemon("daemon1")
	require.NotNil(t, entity1)
	assert.Equal(t, 5, entity1.Order)

	// Verify second daemon was not registered
	entity2 := service.GetDaemon("daemon2")
	assert.Nil(t, entity2)
}

// ============================================================================
// Test 3: Start/Stop Sequence with Manual Order
// ============================================================================

func TestRegisterDaemonWithOrder_StartStopSequence(t *testing.T) {
	service := NewDaemonService()

	var startOrder, stopOrder int32
	var startSequence, stopSequence []string
	var mu sync.Mutex

	// Register daemons with specific orders
	// Lower order starts first, higher order stops first
	daemon1 := newOrderTrackingDaemon("daemon1", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
	daemon2 := newOrderTrackingDaemon("daemon2", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
	daemon3 := newOrderTrackingDaemon("daemon3", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)

	err := service.RegisterDaemonWithOrder(daemon1, 10)
	require.NoError(t, err)
	err = service.RegisterDaemonWithOrder(daemon2, 5)
	require.NoError(t, err)
	err = service.RegisterDaemonWithOrder(daemon3, 15)
	require.NoError(t, err)

	// Start service
	err = service.Start()
	require.NoError(t, err)

	// Verify start sequence: daemon2(5) -> daemon1(10) -> daemon3(15)
	assert.Equal(t, []string{"daemon2:1", "daemon1:2", "daemon3:3"}, startSequence)

	// Stop service
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)

	// Verify stop sequence: daemon3(15) -> daemon1(10) -> daemon2(5)
	assert.Equal(t, []string{"daemon3:1", "daemon1:2", "daemon2:3"}, stopSequence)
}

// ============================================================================
// Test 4: Mixed Auto and Manual Order
// ============================================================================

func TestRegisterDaemonWithOrder_MixedAutoAndManual(t *testing.T) {
	service := NewDaemonService()

	var startOrder, stopOrder int32
	var startSequence, stopSequence []string
	var mu sync.Mutex

	// Register with auto order (will get order 1, 2, 3)
	auto1 := newOrderTrackingDaemon("auto1", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
	auto2 := newOrderTrackingDaemon("auto2", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)

	// Register with manual order
	manual1 := newOrderTrackingDaemon("manual1", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
	manual2 := newOrderTrackingDaemon("manual2", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)

	// Mix registration order
	err := service.RegisterDaemon(auto1) // order 1
	require.NoError(t, err)
	err = service.RegisterDaemonWithOrder(manual1, 5) // order 5
	require.NoError(t, err)
	err = service.RegisterDaemon(auto2) // order 2
	require.NoError(t, err)
	err = service.RegisterDaemonWithOrder(manual2, 10) // order 10
	require.NoError(t, err)

	// Verify orders
	assert.Equal(t, 1, service.GetDaemon("auto1").Order)
	assert.Equal(t, 2, service.GetDaemon("auto2").Order)
	assert.Equal(t, 5, service.GetDaemon("manual1").Order)
	assert.Equal(t, 10, service.GetDaemon("manual2").Order)

	// Start and verify sequence
	err = service.Start()
	require.NoError(t, err)

	// Expected: auto1(1) -> auto2(2) -> manual1(5) -> manual2(10)
	assert.Equal(t, []string{"auto1:1", "auto2:2", "manual1:3", "manual2:4"}, startSequence)

	// Stop and verify sequence
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)

	// Expected: manual2(10) -> manual1(5) -> auto2(2) -> auto1(1)
	assert.Equal(t, []string{"manual2:1", "manual1:2", "auto2:3", "auto1:4"}, stopSequence)
}

// ============================================================================
// Test 5: Unregister Cleans Up Order
// ============================================================================

func TestRegisterDaemonWithOrder_UnregisterCleansOrder(t *testing.T) {
	service := NewDaemonService()
	daemon1 := &testServiceDaemon{name: "daemon1"}
	daemon2 := &testServiceDaemon{name: "daemon2"}

	// Register daemon with order 5
	err := service.RegisterDaemonWithOrder(daemon1, 5)
	require.NoError(t, err)

	// Start the daemon so it can be properly stopped during unregister
	entity := service.GetDaemon("daemon1")
	require.NotNil(t, entity)
	err2 := service.StartDaemon(entity)
	require.NoError(t, err2)

	// Unregister daemon
	err = service.UnregisterDaemon("daemon1")
	require.NoError(t, err)

	// Now we should be able to register another daemon with order 5
	err = service.RegisterDaemonWithOrder(daemon2, 5)
	require.NoError(t, err)

	entity2 := service.GetDaemon("daemon2")
	require.NotNil(t, entity2)
	assert.Equal(t, 5, entity2.Order)
}

// ============================================================================
// Test 6: Negative and Zero Orders
// ============================================================================

func TestRegisterDaemonWithOrder_NegativeAndZeroOrders(t *testing.T) {
	service := NewDaemonService()

	var startOrder, stopOrder int32
	var startSequence, stopSequence []string
	var mu sync.Mutex

	daemon1 := newOrderTrackingDaemon("daemon1", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
	daemon2 := newOrderTrackingDaemon("daemon2", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
	daemon3 := newOrderTrackingDaemon("daemon3", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)

	// Register with negative, zero, and positive orders
	err := service.RegisterDaemonWithOrder(daemon1, -10)
	require.NoError(t, err)
	err = service.RegisterDaemonWithOrder(daemon2, 0)
	require.NoError(t, err)
	err = service.RegisterDaemonWithOrder(daemon3, 10)
	require.NoError(t, err)

	// Start service
	err = service.Start()
	require.NoError(t, err)

	// Verify start sequence: daemon1(-10) -> daemon2(0) -> daemon3(10)
	assert.Equal(t, []string{"daemon1:1", "daemon2:2", "daemon3:3"}, startSequence)

	// Stop service
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)

	// Verify stop sequence: daemon3(10) -> daemon2(0) -> daemon1(-10)
	assert.Equal(t, []string{"daemon3:1", "daemon2:2", "daemon1:3"}, stopSequence)
}

// ============================================================================
// Test 7: Large Number of Daemons with Mixed Orders
// ============================================================================

func TestRegisterDaemonWithOrder_LargeScale(t *testing.T) {
	service := NewDaemonService()

	var startOrder, stopOrder int32
	var startSequence, stopSequence []string
	var mu sync.Mutex

	// Register 10 daemons with various orders
	orders := []int{50, 10, 30, 70, 20, 60, 40, 80, 90, 5}
	// Expected order: 5, 10, 20, 30, 40, 50, 60, 70, 80, 90
	// daemon10(5), daemon2(10), daemon5(20), daemon3(30), daemon7(40), daemon1(50), daemon6(60), daemon4(70), daemon8(80), daemon9(90)
	expectedStartOrder := []string{"daemon10:1", "daemon2:2", "daemon5:3", "daemon3:4", "daemon7:5", "daemon1:6", "daemon6:7", "daemon4:8", "daemon8:9", "daemon9:10"}
	expectedStopOrder := []string{"daemon9:1", "daemon8:2", "daemon4:3", "daemon6:4", "daemon1:5", "daemon7:6", "daemon3:7", "daemon5:8", "daemon2:9", "daemon10:10"}

	for i, order := range orders {
		daemon := newOrderTrackingDaemon(fmt.Sprintf("daemon%d", i+1), &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
		err := service.RegisterDaemonWithOrder(daemon, order)
		require.NoError(t, err)
	}

	// Start service
	err := service.Start()
	require.NoError(t, err)

	// Verify start sequence (sorted by order ascending)
	assert.Equal(t, expectedStartOrder, startSequence)

	// Stop service
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)

	// Verify stop sequence (sorted by order descending)
	assert.Equal(t, expectedStopOrder, stopSequence)
}

// ============================================================================
// Test 8: Concurrent Registration with Order
// ============================================================================

func TestRegisterDaemonWithOrder_ConcurrentRegistration(t *testing.T) {
	service := NewDaemonService()
	var wg sync.WaitGroup
	errors := make(chan error, 20)
	panics := make(chan interface{}, 20)

	// Concurrent registration with different orders
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panics <- r
				}
			}()

			daemon := &testServiceDaemon{name: fmt.Sprintf("concurrent_%d", index)}
			if err := service.RegisterDaemonWithOrder(daemon, index*10); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)
	close(panics)

	// Should have no errors (all different orders)
	errorCount := 0
	for err := range errors {
		if err != nil {
			errorCount++
			t.Logf("Error: %v", err)
		}
	}
	assert.Equal(t, 0, errorCount, "Should have no errors with different orders")

	// Should have no panics
	panicCount := 0
	for p := range panics {
		panicCount++
		t.Logf("Panic: %v", p)
	}
	assert.Equal(t, 0, panicCount, "Should have no panics with different orders")

	// Verify all daemons registered
	for i := 0; i < 10; i++ {
		entity := service.GetDaemon(fmt.Sprintf("concurrent_%d", i))
		assert.NotNil(t, entity)
		assert.Equal(t, i*10, entity.Order)
	}
}

// ============================================================================
// Test 9: Real-world Scenario - Database, Logger, HTTP Server
// ============================================================================

func TestRegisterDaemonWithOrder_RealWorldScenario(t *testing.T) {
	service := NewDaemonService()

	var startOrder, stopOrder int32
	var startSequence, stopSequence []string
	var mu sync.Mutex

	// Simulate real-world daemons with dependency order
	logger := newOrderTrackingDaemon("logger", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
	database := newOrderTrackingDaemon("database", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
	cache := newOrderTrackingDaemon("cache", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
	httpServer := newOrderTrackingDaemon("httpServer", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)

	// Register with dependency order:
	// 1. Logger (needed by all)
	// 2. Database (needed by business logic)
	// 3. Cache (needed by business logic)
	// 4. HTTP Server (depends on all above)
	err := service.RegisterDaemonWithOrder(logger, 1)
	require.NoError(t, err)
	err = service.RegisterDaemonWithOrder(database, 10)
	require.NoError(t, err)
	
	// Test that same order panics
	assert.Panics(t, func() {
		service.RegisterDaemonWithOrder(cache, 10) // Same order should panic
	}, "Expected panic when registering cache with same order as database")

	// Use different order for cache
	err = service.RegisterDaemonWithOrder(cache, 20)
	require.NoError(t, err)
	err = service.RegisterDaemonWithOrder(httpServer, 100)
	require.NoError(t, err)

	// Start service
	err = service.Start()
	require.NoError(t, err)

	// Verify start sequence: logger -> database -> cache -> httpServer
	assert.Equal(t, []string{"logger:1", "database:2", "cache:3", "httpServer:4"}, startSequence)

	// Stop service
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)

	// Verify stop sequence: httpServer -> cache -> database -> logger
	assert.Equal(t, []string{"httpServer:1", "cache:2", "database:3", "logger:4"}, stopSequence)
}

// ============================================================================
// Test 10: Backward Compatibility - Auto Order Still Works
// ============================================================================

func TestRegisterDaemonWithOrder_BackwardCompatibility(t *testing.T) {
	service := NewDaemonService()

	var startOrder, stopOrder int32
	var startSequence, stopSequence []string
	var mu sync.Mutex

	// Register daemons using old RegisterDaemon method (auto order)
	daemon1 := newOrderTrackingDaemon("daemon1", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
	daemon2 := newOrderTrackingDaemon("daemon2", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)
	daemon3 := newOrderTrackingDaemon("daemon3", &startOrder, &stopOrder, &startSequence, &stopSequence, &mu)

	err := service.RegisterDaemon(daemon1)
	require.NoError(t, err)
	err = service.RegisterDaemon(daemon2)
	require.NoError(t, err)
	err = service.RegisterDaemon(daemon3)
	require.NoError(t, err)

	// Verify auto-assigned orders
	assert.Equal(t, 1, service.GetDaemon("daemon1").Order)
	assert.Equal(t, 2, service.GetDaemon("daemon2").Order)
	assert.Equal(t, 3, service.GetDaemon("daemon3").Order)

	// Start service
	err = service.Start()
	require.NoError(t, err)

	// Verify start sequence follows registration order
	assert.Equal(t, []string{"daemon1:1", "daemon2:2", "daemon3:3"}, startSequence)

	// Stop service
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)

	// Verify stop sequence is reverse of start
	assert.Equal(t, []string{"daemon3:1", "daemon2:2", "daemon1:3"}, stopSequence)
}

// ============================================================================
// Test 11: Timer and Scheduler Daemons with Order
// ============================================================================

func TestRegisterDaemonWithOrder_TimerAndSchedulerDaemons(t *testing.T) {
	service := NewDaemonService()

	var loopCount1, loopCount2 int32

	// Create timer daemons with different orders
	timer1 := &testTimerDaemonWithOrder{
		name:      "timer1",
		interval:  50 * time.Millisecond,
		loopCount: &loopCount1,
	}
	timer2 := &testTimerDaemonWithOrder{
		name:      "timer2",
		interval:  50 * time.Millisecond,
		loopCount: &loopCount2,
	}

	// Register with specific orders
	err := service.RegisterDaemonWithOrder(timer1, 10)
	require.NoError(t, err)
	err = service.RegisterDaemonWithOrder(timer2, 5)
	require.NoError(t, err)

	// Verify orders
	assert.Equal(t, 10, service.GetDaemon("timer1").Order)
	assert.Equal(t, 5, service.GetDaemon("timer2").Order)

	// Start service
	err = service.Start()
	require.NoError(t, err)

	// Wait for some loops to execute
	time.Sleep(200 * time.Millisecond)

	// Both should have executed
	assert.True(t, atomic.LoadInt32(&loopCount1) > 0)
	assert.True(t, atomic.LoadInt32(&loopCount2) > 0)

	// Stop service
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)
}

// testTimerDaemonWithOrder is a timer daemon for testing with custom name
type testTimerDaemonWithOrder struct {
	DefaultTimerDaemon
	name      string
	interval  time.Duration
	loopCount *int32
}

func (d *testTimerDaemonWithOrder) Name() string {
	return d.name
}

func (d *testTimerDaemonWithOrder) Interval() time.Duration {
	return d.interval
}

func (d *testTimerDaemonWithOrder) Loop() error {
	atomic.AddInt32(d.loopCount, 1)
	return nil
}

func (d *testTimerDaemonWithOrder) Start() {
	// Empty implementation
}

func (d *testTimerDaemonWithOrder) Stop(sig os.Signal) {
	// Empty implementation
}
