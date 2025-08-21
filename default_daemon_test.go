package kkdaemon

import (
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultDaemonMethods 測試 DefaultDaemon 的所有方法
func TestDefaultDaemonMethods(t *testing.T) {
	daemon := &DefaultDaemon{
		name:   "testDefault",
		state:  StateWait,
		Params: make(map[string]interface{}),
	}
	
	// 測試 Name 方法
	assert.Equal(t, "testDefault", daemon.Name())
	
	// 測試 State 方法
	assert.Equal(t, StateWait, daemon.State())
	
	// 測試 _State 方法
	statePtr := daemon._State()
	assert.NotNil(t, statePtr)
	assert.Equal(t, StateWait, *statePtr)
	
	// 測試 Registered 方法（空實現）
	err := daemon.Registered()
	assert.NoError(t, err)
	
	// 測試 Start 方法（空實現）
	daemon.Start() // 不應該 panic
	
	// 測試 Stop 方法（空實現）
	daemon.Stop(syscall.SIGTERM) // 不應該 panic
}

// TestDefaultDaemonSetName 測試 DefaultDaemon 的 setName 方法
func TestDefaultDaemonSetName(t *testing.T) {
	daemon := &DefaultDaemon{}
	
	// 初始名稱應該為空
	assert.Equal(t, "", daemon.Name())
	
	// 設置名稱
	daemon.setName("newName")
	assert.Equal(t, "newName", daemon.Name())
	
	// 重新設置名稱
	daemon.setName("anotherName")
	assert.Equal(t, "anotherName", daemon.Name())
}

// TestDefaultDaemonParams 測試 DefaultDaemon 的 Params 屬性
func TestDefaultDaemonParams(t *testing.T) {
	daemon := &DefaultDaemon{
		Params: make(map[string]interface{}),
	}
	
	// 測試添加參數
	daemon.Params["key1"] = "value1"
	daemon.Params["key2"] = 42
	daemon.Params["key3"] = true
	
	assert.Equal(t, "value1", daemon.Params["key1"])
	assert.Equal(t, 42, daemon.Params["key2"])
	assert.Equal(t, true, daemon.Params["key3"])
	
	// 測試參數數量
	assert.Equal(t, 3, len(daemon.Params))
}

// TestDefaultDaemonStateManagement 測試 DefaultDaemon 的狀態管理
func TestDefaultDaemonStateManagement(t *testing.T) {
	daemon := &DefaultDaemon{}
	
	// 初始狀態應該是 StateWait
	assert.Equal(t, StateWait, daemon.State())
	
	// 直接修改狀態（模擬內部狀態變更）
	*daemon._State() = StateStart
	assert.Equal(t, StateStart, daemon.State())
	
	*daemon._State() = StateRun
	assert.Equal(t, StateRun, daemon.State())
	
	*daemon._State() = StateStop
	assert.Equal(t, StateStop, daemon.State())
	
	*daemon._State() = StateWait
	assert.Equal(t, StateWait, daemon.State())
}

// TestSimpleDaemonCreation 測試 SimpleDaemon 的創建和基本方法
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
	
	// 創建 SimpleDaemon
	simpleDaemon := &_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{
			name: "testSimple",
		},
		StartFunc: startFunc,
		StopFunc:  stopFunc,
	}
	
	// 測試基本方法
	assert.Equal(t, "testSimple", simpleDaemon.Name())
	assert.Equal(t, StateWait, simpleDaemon.State())
	
	// 測試 Start 方法
	simpleDaemon.Start()
	assert.True(t, startCalled)
	
	// 測試 Stop 方法
	simpleDaemon.Stop(syscall.SIGINT)
	assert.True(t, stopCalled)
	assert.Equal(t, syscall.SIGINT, stopSignal)
}

// TestSimpleDaemonWithNilFunctions 測試帶有 nil 函數的 SimpleDaemon
func TestSimpleDaemonWithNilFunctions(t *testing.T) {
	simpleDaemon := &_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{
			name: "testSimpleNil",
		},
		StartFunc: nil,
		StopFunc:  nil,
	}
	
	// 測試 nil StartFunc（不應該 panic）
	simpleDaemon.Start()
	
	// 測試 nil StopFunc（不應該 panic）
	simpleDaemon.Stop(syscall.SIGTERM)
	
	// 其他方法仍應正常工作
	assert.Equal(t, "testSimpleNil", simpleDaemon.Name())
	assert.Equal(t, StateWait, simpleDaemon.State())
}

// TestRegisterSimpleDaemonFunction 測試 RegisterSimpleDaemon 全局函數
func TestRegisterSimpleDaemonFunction(t *testing.T) {
	var startCalled, stopCalled bool
	var stopSignal os.Signal
	
	// 註冊 SimpleDaemon
	err := RegisterSimpleDaemon("globalSimple",
		func() { startCalled = true },
		func(sig os.Signal) {
			stopCalled = true
			stopSignal = sig
		})
	require.NoError(t, err)
	
	// 檢查是否已註冊
	entity := GetDaemon("globalSimple")
	require.NotNil(t, entity)
	assert.Equal(t, "globalSimple", entity.Name)
	
	// 啟動整個服務（這會啟動所有已註冊的 daemon）
	err = Start()
	require.NoError(t, err)
	assert.True(t, startCalled)
	
	// 停止整個服務
	err = Stop(syscall.SIGKILL)
	require.NoError(t, err)
	assert.True(t, stopCalled)
	assert.Equal(t, syscall.SIGKILL, stopSignal)
}

// TestSimpleDaemonIntegration 測試 SimpleDaemon 的集成功能
func TestSimpleDaemonIntegration(t *testing.T) {
	service := NewDaemonService()
	
	startCount := 0
	stopCount := 0
	
	// 創建多個 SimpleDaemon
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
	
	// 啟動所有 daemon
	err := service.Start()
	require.NoError(t, err)
	assert.Equal(t, 3, startCount)
	
	// 停止所有 daemon
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)
	assert.Equal(t, 3, stopCount)
}

// TestDefaultDaemonWithDaemonInterface 測試 DefaultDaemon 實現 Daemon 接口的完整性
func TestDefaultDaemonWithDaemonInterface(t *testing.T) {
	var daemon Daemon = &DefaultDaemon{
		name: "interfaceTest",
	}
	
	// 測試接口方法
	assert.Equal(t, "interfaceTest", daemon.Name())
	assert.Equal(t, StateWait, daemon.State())
	
	err := daemon.Registered()
	assert.NoError(t, err)
	
	daemon.Start() // 不應該 panic
	daemon.Stop(syscall.SIGTERM) // 不應該 panic
	
	statePtr := daemon._State()
	assert.NotNil(t, statePtr)
}

// TestSimpleDaemonWithDaemonInterface 測試 SimpleDaemon 實現 Daemon 接口的完整性
func TestSimpleDaemonWithDaemonInterface(t *testing.T) {
	var called bool
	
	var daemon Daemon = &_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{
			name: "interfaceSimpleTest",
		},
		StartFunc: func() { called = true },
		StopFunc:  func(sig os.Signal) { called = true },
	}
	
	// 測試接口方法
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

// TestDaemonSetNameInterface 測試 daemonSetName 接口
func TestDaemonSetNameInterface(t *testing.T) {
	// 測試 DefaultDaemon 實現 daemonSetName 接口
	var setNameDaemon daemonSetName = &DefaultDaemon{}
	setNameDaemon.setName("testSetName")
	
	daemon := setNameDaemon.(*DefaultDaemon)
	assert.Equal(t, "testSetName", daemon.Name())
	
	// 測試 SimpleDaemon 也應該實現 daemonSetName 接口（通過嵌入 DefaultDaemon）
	var setNameSimple daemonSetName = &_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{},
	}
	setNameSimple.setName("testSetSimple")
	
	simpleDaemon := setNameSimple.(*_SimpleDaemon)
	assert.Equal(t, "testSetSimple", simpleDaemon.Name())
}
