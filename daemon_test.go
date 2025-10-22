package kkdaemon

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService(t *testing.T) {
	assert.EqualValues(t, nil, RegisterDaemon(&DefaultDaemon{name: "SS"}))
	assert.EqualValues(t, 1, GetDaemon("SS").Order)
	assert.NotNil(t, RegisterDaemon(&DefaultDaemon{name: "SS"}))
	assert.Nil(t, RegisterDaemon(&P1{Daemon: &DefaultDaemon{}}))
	p2 := &P2{Daemon: &DefaultDaemon{}}
	assert.Nil(t, RegisterDaemon(p2))
	RegisterSimpleDaemon("P3", func() {
		println("start p3")
	}, func(sig os.Signal) {
		println("stop p3")
	})

	Start()
	assert.Nil(t, UnregisterDaemon("P2"))
	assert.Equal(t, 1, p2.s)
	Stop(syscall.SIGKILL)
	assert.Equal(t, 1, p2.s)
}

type P1 struct {
	Daemon
}

func (p *P1) Start() {
	println("start p1")
}

func (p *P1) Stop(sig os.Signal) {
	println("stop p1")
}

type P2 struct {
	Daemon
	s int
}

func (p *P2) Start() {
	println("start p2")
}

func (p *P2) Stop(sig os.Signal) {
	p.s++
	println("stop p2")
}

// TestRegisterDaemonErrorCases tests daemon registration error scenarios
func TestRegisterDaemonErrorCases(t *testing.T) {
	service := NewDaemonService()
	
	// Test registering nil daemon
	err := service.RegisterDaemon(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil daemon")
	
	// Test registering daemon with empty name (using anonymous struct so reflection cannot get name)
	emptyNameDaemon := &struct{ DefaultDaemon }{}
	err = service.RegisterDaemon(emptyNameDaemon)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is empty")
	
	// Test registering daemon with duplicate name
	daemon1 := &DefaultDaemon{name: "duplicate"}
	err = service.RegisterDaemon(daemon1)
	require.NoError(t, err)
	
	daemon2 := &DefaultDaemon{name: "duplicate"}
	err = service.RegisterDaemon(daemon2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is exist")
}

// TestDaemonStateMachine tests daemon state transition correctness
func TestDaemonStateMachine(t *testing.T) {
	service := NewDaemonService()
	daemon := &testStateDaemon{}
	
	// Initial state should be StateWait
	assert.Equal(t, StateWait, daemon.State())
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	// State should be StateWait after registration
	assert.Equal(t, StateWait, daemon.State())
	
	// Start daemon
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)
	
	err2 := service.StartDaemon(entity)
	require.NoError(t, err2)
	
	// State should be StateStart after starting
	assert.Equal(t, StateStart, daemon.State())
	
	// Stop daemon
	err3 := service.StopDaemon(entity, syscall.SIGTERM)
	require.NoError(t, err3)
	
	// State should return to StateWait after stopping
	assert.Equal(t, StateWait, daemon.State())
}

// TestConcurrentOperations tests concurrent operations safety
func TestConcurrentOperations(t *testing.T) {
	service := NewDaemonService()
	var wg sync.WaitGroup
	errors := make(chan error, 100)
	
	// Concurrently register multiple daemons
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			daemon := &DefaultDaemon{name: fmt.Sprintf("concurrent_%d", index)}
			if err := service.RegisterDaemon(daemon); err != nil {
				errors <- err
			}
		}(i)
	}
	
	wg.Wait()
	close(errors)
	
	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent registration error: %v", err)
	}
	
	// Verify all daemons are registered
	for i := 0; i < 10; i++ {
		daemonName := fmt.Sprintf("concurrent_%d", i)
		entity := service.GetDaemon(daemonName)
		assert.NotNil(t, entity)
		assert.Equal(t, daemonName, entity.Name)
	}
}

// TestGlobalFunctions tests global functions edge cases
func TestGlobalFunctions(t *testing.T) {
	// Test StartDaemon for nonexistent daemon
	err := StartDaemon("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	
	// Test StopDaemon for nonexistent daemon
	err = StopDaemon("nonexistent", syscall.SIGTERM)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	
	// Test GetDaemon for nonexistent daemon
	entity := GetDaemon("nonexistent")
	assert.Nil(t, entity)
	
	// Test UnregisterDaemon for nonexistent daemon (should not error)
	err = UnregisterDaemon("nonexistent")
	assert.NoError(t, err)
}

// TestRegisterSimpleDaemon tests simple daemon registration functionality
func TestRegisterSimpleDaemon(t *testing.T) {
	var startCalled, stopCalled bool
	var stopSignal os.Signal
	
	err := RegisterSimpleDaemon("simple_test", 
		func() { startCalled = true },
		func(sig os.Signal) { 
			stopCalled = true
			stopSignal = sig
		})
	require.NoError(t, err)
	
	// Start service
	err = Start()
	require.NoError(t, err)
	
	assert.True(t, startCalled)
	
	// Stop service
	err = Stop(syscall.SIGTERM)
	require.NoError(t, err)
	
	assert.True(t, stopCalled)
	assert.Equal(t, syscall.SIGTERM, stopSignal)
}

// TestShutdownFunctions tests graceful shutdown functionality
func TestShutdownFunctions(t *testing.T) {
	service := NewDaemonService()
	
	// Initial state should not be shutdown
	assert.False(t, service.IsShutdown())
	
	// Test ShutdownFuture
	future := service.ShutdownFuture()
	assert.NotNil(t, future)
	
	// Perform graceful shutdown in separate goroutine
	go func() {
		time.Sleep(100 * time.Millisecond)
		service.ShutdownGracefully()
	}()
	
	// Wait for shutdown to complete
	select {
	case <-future.Done():
		// Shutdown should succeed
		assert.True(t, service.IsShutdown())
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown timeout")
	}
}

// testStateDaemon is a test daemon for testing state transitions
type testStateDaemon struct {
	DefaultDaemon
}

func (d *testStateDaemon) Start() {
	// Empty implementation for testing
}

func (d *testStateDaemon) Stop(sig os.Signal) {
	// Empty implementation for testing
}
