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

// TestDaemonServiceCreation 測試 DaemonService 的創建和初始化
func TestDaemonServiceCreation(t *testing.T) {
	service := NewDaemonService()
	
	// 檢查基本屬性
	assert.True(t, service.StopWhenKill)
	assert.NotNil(t, service.sig)
	assert.NotNil(t, service.stopFuture)
	assert.NotNil(t, service.shutdownFuture)
	assert.NotNil(t, service.loopInvokerReload)
	assert.Equal(t, int32(0), service.state)
	assert.Equal(t, int32(0), service.shutdownState)
	assert.Equal(t, 0, service.orderIndex)
	
	// 檢查初始狀態
	assert.False(t, service.IsShutdown())
}

// TestDaemonServiceRegisterAndUnregister 測試註冊和取消註冊功能
func TestDaemonServiceRegisterAndUnregister(t *testing.T) {
	service := NewDaemonService()
	daemon := &testServiceDaemon{}
	
	// 測試註冊
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	// 檢查註冊結果
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)
	assert.Equal(t, daemon.Name(), entity.Name)
	assert.Equal(t, 1, entity.Order)
	assert.Equal(t, StateWait, daemon.State())
	
	// 啟動 daemon 後再取消註冊（只有運行中的 daemon 才能被正確停止）
	err = service.StartDaemon(entity)
	require.NoError(t, err)
	assert.Equal(t, StateStart, daemon.State())
	
	// 測試取消註冊
	err = service.UnregisterDaemon(daemon.Name())
	require.NoError(t, err)
	
	// 檢查取消註冊結果
	entity = service.GetDaemon(daemon.Name())
	assert.Nil(t, entity)
	assert.True(t, daemon.stopCalled)
}

// TestDaemonServiceStartStop 測試服務的啟動和停止
func TestDaemonServiceStartStop(t *testing.T) {
	service := NewDaemonService()
	daemon := &testServiceDaemon{}
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	// 測試啟動服務
	err = service.Start()
	require.NoError(t, err)
	assert.Equal(t, StateStart, daemon.State())
	assert.True(t, daemon.startCalled)
	
	// 測試重複啟動（應該失敗）
	err = service.Start()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in WAIT state")
	
	// 測試停止服務
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)
	assert.Equal(t, StateWait, daemon.State())
	assert.True(t, daemon.stopCalled)
	assert.Equal(t, syscall.SIGTERM, daemon.stopSignal)
}

// TestDaemonServiceStartStopIndividualDaemon 測試單個 daemon 的啟動和停止
func TestDaemonServiceStartStopIndividualDaemon(t *testing.T) {
	service := NewDaemonService()
	daemon := &testServiceDaemon{}
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)
	
	// 測試啟動單個 daemon
	err2 := service.StartDaemon(entity)
	require.NoError(t, err2)
	assert.Equal(t, StateStart, daemon.State())
	assert.True(t, daemon.startCalled)
	
	// 測試重複啟動（應該失敗）
	err2 = service.StartDaemon(entity)
	require.Error(t, err2)
	
	// 測試停止單個 daemon
	err3 := service.StopDaemon(entity, syscall.SIGINT)
	require.NoError(t, err3)
	assert.Equal(t, StateWait, daemon.State())
	assert.True(t, daemon.stopCalled)
	assert.Equal(t, syscall.SIGINT, daemon.stopSignal)
}

// TestDaemonServiceSignalHandling 測試信號處理機制
func TestDaemonServiceSignalHandling(t *testing.T) {
	service := NewDaemonService()
	daemon := &testServiceDaemon{}
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	err = service.Start()
	require.NoError(t, err)
	
	// 測試優雅關閉信號
	go func() {
		time.Sleep(100 * time.Millisecond)
		service.ShutdownGracefully()
	}()
	
	// 等待關閉完成
	select {
	case <-service.ShutdownFuture().Done():
		assert.True(t, service.IsShutdown())
		assert.True(t, daemon.stopCalled)
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown timeout")
	}
}

// TestDaemonServiceStopWhenKillFlag 測試 StopWhenKill 標誌的行為
func TestDaemonServiceStopWhenKillFlag(t *testing.T) {
	service := NewDaemonService()
	service.StopWhenKill = false
	daemon := &testServiceDaemon{}
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	err = service.Start()
	require.NoError(t, err)
	
	// 當 StopWhenKill 為 false 時，發送 SIGTERM 不應該停止 daemon
	go func() {
		time.Sleep(100 * time.Millisecond)
		service.sig <- syscall.SIGTERM
	}()
	
	// 等待信號處理完成
	select {
	case <-service.ShutdownFuture().Done():
		assert.True(t, service.IsShutdown())
		assert.False(t, daemon.stopCalled) // daemon 不應該被停止
	case <-time.After(2 * time.Second):
		t.Fatal("Signal handling timeout")
	}
}

// TestDaemonServicePanicRecovery 測試 panic 恢復機制
func TestDaemonServicePanicRecovery(t *testing.T) {
	service := NewDaemonService()
	daemon := &panicDaemon{}
	
	err := service.RegisterDaemon(daemon)
	require.NoError(t, err)
	
	entity := service.GetDaemon(daemon.Name())
	require.NotNil(t, entity)
	
	// 啟動會引發 panic 的 daemon
	err2 := service.StartDaemon(entity)
	require.Error(t, err2) // 應該捕獲到 panic
	
	// daemon 狀態應該被恢復到 StateStart
	assert.Equal(t, StateStart, daemon.State())
}

// TestDaemonServiceLoopInvoker 測試循環調用器功能
func TestDaemonServiceLoopInvoker(t *testing.T) {
	service := NewDaemonService()
	timerDaemon := &testTimerLoopDaemon{}
	
	err := service.RegisterDaemon(timerDaemon)
	require.NoError(t, err)
	
	err = service.Start()
	require.NoError(t, err)
	
	// 等待幾次循環執行
	time.Sleep(250 * time.Millisecond)
	
	// 檢查 Loop 方法是否被調用
	assert.True(t, timerDaemon.loopCount > 0)
	
	err = service.Stop(syscall.SIGTERM)
	require.NoError(t, err)
}

// TestDaemonServiceConcurrentOperations 測試 DaemonService 的併發安全性
func TestDaemonServiceConcurrentOperations(t *testing.T) {
	service := NewDaemonService()
	var wg sync.WaitGroup
	errors := make(chan error, 20)
	
	// 併發註冊和取消註冊操作
	for i := 0; i < 10; i++ {
		wg.Add(2)
		
		// 註冊 daemon
		go func(index int) {
			defer wg.Done()
			daemon := &testServiceDaemon{name: fmt.Sprintf("concurrent_service_%d", index)}
			if err := service.RegisterDaemon(daemon); err != nil {
				errors <- err
			}
		}(i)
		
		// 嘗試取消註冊
		go func(index int) {
			defer wg.Done()
			time.Sleep(50 * time.Millisecond) // 稍微延遲以增加競爭條件
			if err := service.UnregisterDaemon(fmt.Sprintf("concurrent_service_%d", index)); err != nil {
				errors <- err
			}
		}(i)
	}
	
	wg.Wait()
	close(errors)
	
	// 檢查錯誤（一些錯誤是預期的，比如試圖取消註冊不存在的 daemon）
	errorCount := 0
	for err := range errors {
		if err != nil {
			errorCount++
		}
	}
	
	// 由於併發操作，可能會有一些錯誤，但不應該有太多
	assert.True(t, errorCount <= 10, "Too many errors in concurrent operations")
}

// testServiceDaemon 用於測試 DaemonService 的測試 daemon
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

// panicDaemon 用於測試 panic 恢復的 daemon
type panicDaemon struct {
	DefaultDaemon
}

func (d *panicDaemon) Start() {
	panic("test panic in start")
}

func (d *panicDaemon) Stop(sig os.Signal) {
	// 空實現
}

// testTimerLoopDaemon 用於測試循環調用器的定時器 daemon
type testTimerLoopDaemon struct {
	DefaultTimerDaemon
	loopCount int32
}

func (d *testTimerLoopDaemon) Interval() time.Duration {
	return 50 * time.Millisecond // 短間隔用於測試
}

func (d *testTimerLoopDaemon) Loop() error {
	atomic.AddInt32(&d.loopCount, 1)
	return nil
}

func (d *testTimerLoopDaemon) Start() {
	// 空實現
}

func (d *testTimerLoopDaemon) Stop(sig os.Signal) {
	// 空實現
}
