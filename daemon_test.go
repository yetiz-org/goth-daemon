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

// TestRegisterDaemonErrorCases 測試註冊 daemon 的錯誤場景
func TestRegisterDaemonErrorCases(t *testing.T) {
	service := NewDaemonService()
	
	// 測試註冊 nil daemon
	err := service.RegisterDaemon(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil daemon")
	
	// 測試註冊空名稱的 daemon (使用匿名結構體，這樣反射也無法獲取名稱)
	emptyNameDaemon := &struct{ DefaultDaemon }{}
	err = service.RegisterDaemon(emptyNameDaemon)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is empty")
	
	// 測試重複註冊相同名稱的 daemon
	daemon1 := &DefaultDaemon{name: "duplicate"}
	err = service.RegisterDaemon(daemon1)
	require.NoError(t, err)
	
	daemon2 := &DefaultDaemon{name: "duplicate"}
	err = service.RegisterDaemon(daemon2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is exist")
}

// TestDaemonStateMachine 測試 daemon 狀態轉換的正確性
func TestDaemonStateMachine(t *testing.T) {
	service := NewDaemonService()
	daemon := &testStateDaemon{}
	
	// 初始狀態應該是 StateWait
	assert.Equal(t, StateWait, daemon.State())
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	// 註冊後狀態應該是 StateWait
	assert.Equal(t, StateWait, daemon.State())
	
	// 啟動 daemon
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)
	
	err2 := service.StartDaemon(entity)
	require.NoError(t, err2)
	
	// 啟動後狀態應該是 StateStart
	assert.Equal(t, StateStart, daemon.State())
	
	// 停止 daemon
	err3 := service.StopDaemon(entity, syscall.SIGTERM)
	require.NoError(t, err3)
	
	// 停止後狀態應該回到 StateWait
	assert.Equal(t, StateWait, daemon.State())
}

// TestConcurrentOperations 測試並發操作的安全性
func TestConcurrentOperations(t *testing.T) {
	service := NewDaemonService()
	var wg sync.WaitGroup
	errors := make(chan error, 100)
	
	// 並發註冊多個 daemon
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
	
	// 檢查是否有錯誤
	for err := range errors {
		t.Errorf("Concurrent registration error: %v", err)
	}
	
	// 驗證所有 daemon 都已註冊
	for i := 0; i < 10; i++ {
		daemonName := fmt.Sprintf("concurrent_%d", i)
		entity := service.GetDaemon(daemonName)
		assert.NotNil(t, entity)
		assert.Equal(t, daemonName, entity.Name)
	}
}

// TestGlobalFunctions 測試全局函數的邊界條件
func TestGlobalFunctions(t *testing.T) {
	// 測試 StartDaemon 不存在的 daemon
	err := StartDaemon("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	
	// 測試 StopDaemon 不存在的 daemon
	err = StopDaemon("nonexistent", syscall.SIGTERM)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	
	// 測試 GetDaemon 不存在的 daemon
	entity := GetDaemon("nonexistent")
	assert.Nil(t, entity)
	
	// 測試 UnregisterDaemon 不存在的 daemon（應該不報錯）
	err = UnregisterDaemon("nonexistent")
	assert.NoError(t, err)
}

// TestRegisterSimpleDaemon 測試簡單 daemon 註冊功能
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
	
	// 啟動服務
	err = Start()
	require.NoError(t, err)
	
	assert.True(t, startCalled)
	
	// 停止服務
	err = Stop(syscall.SIGTERM)
	require.NoError(t, err)
	
	assert.True(t, stopCalled)
	assert.Equal(t, syscall.SIGTERM, stopSignal)
}

// TestShutdownFunctions 測試優雅關閉功能
func TestShutdownFunctions(t *testing.T) {
	service := NewDaemonService()
	
	// 初始狀態不應該是關閉狀態
	assert.False(t, service.IsShutdown())
	
	// 測試 ShutdownFuture
	future := service.ShutdownFuture()
	assert.NotNil(t, future)
	
	// 在單獨的 goroutine 中進行優雅關閉
	go func() {
		time.Sleep(100 * time.Millisecond)
		service.ShutdownGracefully()
	}()
	
	// 等待關閉完成
	select {
	case <-future.Done():
		// 關閉應該成功
		assert.True(t, service.IsShutdown())
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown timeout")
	}
}

// testStateDaemon 用於測試狀態轉換的測試 daemon
type testStateDaemon struct {
	DefaultDaemon
}

func (d *testStateDaemon) Start() {
	// 測試用空實現
}

func (d *testStateDaemon) Stop(sig os.Signal) {
	// 測試用空實現
}
