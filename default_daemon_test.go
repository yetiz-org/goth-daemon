package kkdaemon

import (
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultDaemonMethods tests all methods of DefaultDaemon
func TestDefaultDaemonMethods(t *testing.T) {
	daemon := &DefaultDaemon{
		name:   "testDefault",
		state:  StateWait,
		Params: make(map[string]interface{}),
	}
	
	// Test Name method
	assert.Equal(t, "testDefault", daemon.Name())
	
	// Test State method
	assert.Equal(t, StateWait, daemon.State())
	
	// Test _State method
	statePtr := daemon._State()
	assert.NotNil(t, statePtr)
	assert.Equal(t, StateWait, *statePtr)
	
	// Test Registered method (empty implementation)
	err := daemon.Registered()
	assert.NoError(t, err)
	
	// Test Start method (empty implementation)
	daemon.Start() // Should not panic
	
	// Test Stop method (empty implementation)
	daemon.Stop(syscall.SIGTERM) // Should not panic
}

// TestDefaultDaemonSetName tests DefaultDaemon's setName method
func TestDefaultDaemonSetName(t *testing.T) {
	daemon := &DefaultDaemon{}
	
	// Initial name should be empty
	assert.Equal(t, "", daemon.Name())
	
	// Set name
	daemon.setName("newName")
	assert.Equal(t, "newName", daemon.Name())
	
	// Reset name
	daemon.setName("anotherName")
	assert.Equal(t, "anotherName", daemon.Name())
}

// TestDefaultDaemonParams tests DefaultDaemon's Params property
func TestDefaultDaemonParams(t *testing.T) {
	daemon := &DefaultDaemon{
		Params: make(map[string]interface{}),
	}
	
	// Test adding parameters
	daemon.Params["key1"] = "value1"
	daemon.Params["key2"] = 42
	daemon.Params["key3"] = true
	
	assert.Equal(t, "value1", daemon.Params["key1"])
	assert.Equal(t, 42, daemon.Params["key2"])
	assert.Equal(t, true, daemon.Params["key3"])
	
	// Test parameter count
	assert.Equal(t, 3, len(daemon.Params))
}

// TestDefaultDaemonStateManagement tests DefaultDaemon's state management
func TestDefaultDaemonStateManagement(t *testing.T) {
	daemon := &DefaultDaemon{}
	
	// Initial state should be StateWait
	assert.Equal(t, StateWait, daemon.State())
	
	// Directly modify state (simulate internal state changes)
	*daemon._State() = StateStart
	assert.Equal(t, StateStart, daemon.State())
	
	*daemon._State() = StateRun
	assert.Equal(t, StateRun, daemon.State())
	
	*daemon._State() = StateStop
	assert.Equal(t, StateStop, daemon.State())
	
	*daemon._State() = StateWait
	assert.Equal(t, StateWait, daemon.State())
}

// TestSimpleDaemonCreation tests SimpleDaemon creation and basic methods
func TestSimpleDaemonCreation(t *testing.T) {
	var startCalled, stopCalled bool
	var stopSignal os.Signal
	
	startFunc := func() {
		startCalled = true
	}
	
	stopFunc := func(sig os.Signal) {
		stopCalled = true
		stopSignal = sig
	}
	
	// Create SimpleDaemon
	simpleDaemon := &_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{
			name: "testSimple",
		},
		StartFunc: startFunc,
		StopFunc:  stopFunc,
	}
	
	// Test basic methods
	assert.Equal(t, "testSimple", simpleDaemon.Name())
	assert.Equal(t, StateWait, simpleDaemon.State())
	
	// Test Start method
	simpleDaemon.Start()
	assert.True(t, startCalled)
	
	// Test Stop method
	simpleDaemon.Stop(syscall.SIGINT)
	assert.True(t, stopCalled)
	assert.Equal(t, syscall.SIGINT, stopSignal)
}

// TestSimpleDaemonWithNilFunctions tests SimpleDaemon with nil functions
func TestSimpleDaemonWithNilFunctions(t *testing.T) {
	simpleDaemon := &_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{
			name: "testSimpleNil",
		},
		StartFunc: nil,
		StopFunc:  nil,
	}
	
	// Test nil StartFunc (should not panic)
	simpleDaemon.Start()
	
	// Test nil StopFunc (should not panic)
	simpleDaemon.Stop(syscall.SIGTERM)
	
	// Other methods should still work
	assert.Equal(t, "testSimpleNil", simpleDaemon.Name())
	assert.Equal(t, StateWait, simpleDaemon.State())
}

// TestRegisterSimpleDaemonFunction tests RegisterSimpleDaemon global function
func TestRegisterSimpleDaemonFunction(t *testing.T) {
	var startCalled, stopCalled bool
	var stopSignal os.Signal
	
	// Register SimpleDaemon
	err := RegisterSimpleDaemon("globalSimple",
		func() { startCalled = true },
		func(sig os.Signal) {
			stopCalled = true
			stopSignal = sig
		})
	require.NoError(t, err)
	
	// Check if registered
	entity := GetDaemon("globalSimple")
	require.NotNil(t, entity)
	assert.Equal(t, "globalSimple", entity.Name)
	
	// Start entire service (this starts all registered daemons)
	err = Start()
	require.NoError(t, err)
	assert.True(t, startCalled)
	
	// Stop entire service
	err = Stop(syscall.SIGKILL)
	require.NoError(t, err)
	assert.True(t, stopCalled)
	assert.Equal(t, syscall.SIGKILL, stopSignal)
}

// TestSimpleDaemonIntegration tests SimpleDaemon integration functionality
func TestSimpleDaemonIntegration(t *testing.T) {
	service := NewDaemonService()
	
	startCount := 0
	stopCount := 0
	
	// Create multiple SimpleDaemons
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("integration_%d", i)
		simpleDaemon := &_SimpleDaemon{
			DefaultDaemon: DefaultDaemon{
				name: name,
			},
			StartFunc: func() { startCount++ },
			StopFunc:  func(sig os.Signal) { stopCount++ },
		}
		
		err := service.RegisterDaemon(simpleDaemon)
		require.NoError(t, err)
	}
	
	// Start all daemons
	err := service.Start()
	require.NoError(t, err)
	assert.Equal(t, 3, startCount)
	
	// Stop all daemons
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)
	assert.Equal(t, 3, stopCount)
}

// TestDefaultDaemonWithDaemonInterface tests DefaultDaemon implements Daemon interface completely
func TestDefaultDaemonWithDaemonInterface(t *testing.T) {
	var daemon Daemon = &DefaultDaemon{
		name: "interfaceTest",
	}
	
	// Test interface methods
	assert.Equal(t, "interfaceTest", daemon.Name())
	assert.Equal(t, StateWait, daemon.State())
	
	err := daemon.Registered()
	assert.NoError(t, err)
	
	daemon.Start() // Should not panic
	daemon.Stop(syscall.SIGTERM) // Should not panic
	
	statePtr := daemon._State()
	assert.NotNil(t, statePtr)
}

// TestSimpleDaemonWithDaemonInterface tests SimpleDaemon implements Daemon interface completely
func TestSimpleDaemonWithDaemonInterface(t *testing.T) {
	var called bool
	
	var daemon Daemon = &_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{
			name: "interfaceSimpleTest",
		},
		StartFunc: func() { called = true },
		StopFunc:  func(sig os.Signal) { called = true },
	}
	
	// Test interface methods
	assert.Equal(t, "interfaceSimpleTest", daemon.Name())
	assert.Equal(t, StateWait, daemon.State())
	
	err := daemon.Registered()
	assert.NoError(t, err)
	
	daemon.Start()
	assert.True(t, called)
	
	called = false
	daemon.Stop(syscall.SIGTERM)
	assert.True(t, called)
	
	statePtr := daemon._State()
	assert.NotNil(t, statePtr)
}

// TestDaemonSetNameInterface tests daemonSetName interface
func TestDaemonSetNameInterface(t *testing.T) {
	// Test DefaultDaemon implements daemonSetName interface
	var setNameDaemon daemonSetName = &DefaultDaemon{}
	setNameDaemon.setName("testSetName")
	
	daemon := setNameDaemon.(*DefaultDaemon)
	assert.Equal(t, "testSetName", daemon.Name())
	
	// Test SimpleDaemon also implements daemonSetName interface (through embedding DefaultDaemon)
	var setNameSimple daemonSetName = &_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{},
	}
	setNameSimple.setName("testSetSimple")
	
	simpleDaemon := setNameSimple.(*_SimpleDaemon)
	assert.Equal(t, "testSetSimple", simpleDaemon.Name())
}
