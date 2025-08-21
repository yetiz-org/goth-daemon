package kkdaemon

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// BenchmarkServiceStart 基準測試服務啟動性能
func BenchmarkServiceStart(b *testing.B) {
	daemonCounts := []int{1, 5, 10, 20}  // 大幅減少 daemon 數量
	
	for _, count := range daemonCounts {
		b.Run(fmt.Sprintf("daemons_%d", count), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				service := NewDaemonService()
				
				// 準備 daemon
				for j := 0; j < count; j++ {
					daemon := &_SimpleDaemon{
						DefaultDaemon: DefaultDaemon{
							name: fmt.Sprintf("bench_%d_%d", i, j),
						},
						StartFunc: func() {},
						StopFunc:  func(sig os.Signal) {},
					}
					service.RegisterDaemon(daemon)
				}
				
				b.StartTimer()
				service.Start()
				b.StopTimer()
				
				// 確保資源清理
				service.Stop(syscall.SIGTERM)
				time.Sleep(time.Millisecond) // 給清理一點時間
			}
		})
	}
}

// BenchmarkServiceStop 基準測試服務停止性能
func BenchmarkServiceStop(b *testing.B) {
	daemonCounts := []int{1, 5, 10, 20}  // 大幅減少 daemon 數量
	
	for _, count := range daemonCounts {
		b.Run(fmt.Sprintf("daemons_%d", count), func(b *testing.B) {
			// 限制基準測試迭代次數，避免過度測試導致資源累積
			if b.N > 1000 {
				b.N = 1000
			}
			
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				service := NewDaemonService()
				
				// 準備 daemon
				for j := 0; j < count; j++ {
					daemon := &_SimpleDaemon{
						DefaultDaemon: DefaultDaemon{
							name: fmt.Sprintf("bench_%d_%d", i, j),
						},
						StartFunc: func() {},
						StopFunc:  func(sig os.Signal) {},
					}
					service.RegisterDaemon(daemon)
				}
				
				service.Start()
				time.Sleep(2 * time.Millisecond) // 增加等待時間確保啟動完成
				b.StartTimer()
				service.Stop(syscall.SIGTERM)
				b.StopTimer()
				
				// 添加額外等待確保 _LoopInvoker goroutine 完全停止
				time.Sleep(time.Millisecond)
				runtime.GC() // 強制垃圾回收，清理資源
			}
		})
	}
}

// BenchmarkDaemonRegistration 基準測試 daemon 註冊性能
func BenchmarkDaemonRegistration(b *testing.B) {
	service := NewDaemonService()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		daemon := &_SimpleDaemon{
			DefaultDaemon: DefaultDaemon{
				name: fmt.Sprintf("reg_bench_%d", i),
			},
			StartFunc: func() {},
			StopFunc:  func(sig os.Signal) {},
		}
		service.RegisterDaemon(daemon)
	}
}

// BenchmarkGetOrderedDaemonEntitySlice 基準測試排序函數性能
func BenchmarkGetOrderedDaemonEntitySlice(b *testing.B) {
	// 準備不同數量的 daemon
	daemonCounts := []int{5, 10, 20, 50}  // 減少數量並且每次創建新的 service
	
	for _, count := range daemonCounts {
		b.Run(fmt.Sprintf("daemons_%d", count), func(b *testing.B) {
			service := NewDaemonService()
			
			// 準備測試數據
			for i := 0; i < count; i++ {
				var daemon Daemon
				if i%2 == 0 {
					daemon = &benchmarkTimerDaemon{
						DefaultTimerDaemon: DefaultTimerDaemon{
							DefaultDaemon: DefaultDaemon{
								name: fmt.Sprintf("timer_%d_%d", count, i),
							},
						},
					}
				} else {
					daemon = &benchmarkSchedulerDaemon{
						DefaultSchedulerDaemon: DefaultSchedulerDaemon{
							DefaultDaemon: DefaultDaemon{
								name: fmt.Sprintf("scheduler_%d_%d", count, i),
							},
						},
					}
				}
				service.RegisterDaemon(daemon)
			}
			
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				service.getOrderedDaemonEntitySlice()
			}
		})
	}
}

// BenchmarkDaemonMapRange 基準測試 DaemonMap.Range 性能
func BenchmarkDaemonMapRange(b *testing.B) {
	// 準備不同數量的 daemon
	daemonCounts := []int{5, 10, 20, 50}  // 減少數量
	
	for _, count := range daemonCounts {
		b.Run(fmt.Sprintf("daemons_%d", count), func(b *testing.B) {
			service := NewDaemonService()
			
			// 準備測試數據
			for i := 0; i < count; i++ {
				daemon := &_SimpleDaemon{
					DefaultDaemon: DefaultDaemon{
						name: fmt.Sprintf("range_%d_%d", count, i),
					},
					StartFunc: func() {},
					StopFunc:  func(sig os.Signal) {},
				}
				service.RegisterDaemon(daemon)
			}
			
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var counter int
				service.DaemonMap.Range(func(key, value interface{}) bool {
					counter++
					return true
				})
			}
		})
	}
}

// BenchmarkMemoryAllocation 基準測試記憶體分配性能
func BenchmarkMemoryAllocation(b *testing.B) {
	b.Run("slice_allocation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var el []*DaemonEntity
			for j := 0; j < 50; j++ { // 大幅減少數量
				entity := &DaemonEntity{Name: fmt.Sprintf("entity_%d", j)}
				el = append(el, entity)
			}
			_ = el
		}
	})
	
	b.Run("pre_allocated_slice", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			el := make([]*DaemonEntity, 0, 50) // 減少預分配數量
			for j := 0; j < 50; j++ {
				entity := &DaemonEntity{Name: fmt.Sprintf("entity_%d", j)}
				el = append(el, entity)
			}
			_ = el
		}
	})
}

// BenchmarkAtomicOperations 基準測試原子操作性能
func BenchmarkAtomicOperations(b *testing.B) {
	var state int32
	
	b.Run("CompareAndSwap", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			atomic.CompareAndSwapInt32(&state, StateWait, StateStart)
			atomic.CompareAndSwapInt32(&state, StateStart, StateWait)
		}
	})
	
	b.Run("LoadStore", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			atomic.LoadInt32(&state)
			atomic.StoreInt32(&state, StateStart)
		}
	})
}

// BenchmarkConcurrentOperations 基準測試並發操作性能
func BenchmarkConcurrentOperations(b *testing.B) {
	service := NewDaemonService()
	
	// 準備一些 daemon
	for i := 0; i < 100; i++ {
		daemon := &_SimpleDaemon{
			DefaultDaemon: DefaultDaemon{
				name: fmt.Sprintf("concurrent_%d", i),
			},
			StartFunc: func() {},
			StopFunc:  func(sig os.Signal) {},
		}
		service.RegisterDaemon(daemon)
	}
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			service.Start()
			service.Stop(syscall.SIGTERM)
		}
	})
}

// 基準測試用的輔助類型
type benchmarkTimerDaemon struct {
	DefaultTimerDaemon
}

func (d *benchmarkTimerDaemon) Interval() time.Duration {
	return 100 * time.Millisecond
}

func (d *benchmarkTimerDaemon) Loop() error {
	return nil
}

type benchmarkSchedulerDaemon struct {
	DefaultSchedulerDaemon
}

func (d *benchmarkSchedulerDaemon) When() CronSyntax {
	return "*/5 * * * * *"
}

func (d *benchmarkSchedulerDaemon) Loop() error {
	return nil
}

// BenchmarkLoopInvokerPerformance 基準測試循環調用器性能
func BenchmarkLoopInvokerPerformance(b *testing.B) {
	service := NewDaemonService()
	
	// 準備不同數量的定時器 daemon
	daemonCounts := []int{10, 50, 100}
	
	for _, count := range daemonCounts {
		for i := 0; i < count; i++ {
			daemon := &benchmarkTimerDaemon{
				DefaultTimerDaemon: DefaultTimerDaemon{
					DefaultDaemon: DefaultDaemon{
						name: fmt.Sprintf("loop_%d_%d", count, i),
					},
				},
			}
			service.RegisterDaemon(daemon)
		}
		
		b.Run(fmt.Sprintf("timer_daemons_%d", count), func(b *testing.B) {
			service.Start()
			
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				service.getOrderedDaemonEntitySlice()
			}
			b.StopTimer()
			
			service.Stop(syscall.SIGTERM)
		})
	}
}

// BenchmarkMemoryUsage 測試記憶體使用情況
func BenchmarkMemoryUsage(b *testing.B) {
	service := NewDaemonService()
	
	// 準備大量 daemon
	for i := 0; i < 1000; i++ {
		daemon := &_SimpleDaemon{
			DefaultDaemon: DefaultDaemon{
				name: fmt.Sprintf("memory_%d", i),
			},
			StartFunc: func() {},
			StopFunc:  func(sig os.Signal) {},
		}
		service.RegisterDaemon(daemon)
	}
	
	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.Start()
		service.Stop(syscall.SIGTERM)
	}
	b.StopTimer()
	
	runtime.GC()
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	
	b.Logf("Memory before: %d bytes", m1.Alloc)
	b.Logf("Memory after: %d bytes", m2.Alloc)
	b.Logf("Memory diff: %d bytes", int64(m2.Alloc)-int64(m1.Alloc))
	b.Logf("Total allocations: %d bytes", m2.TotalAlloc-m1.TotalAlloc)
}
