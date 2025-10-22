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

// BenchmarkServiceStart benchmarks service start performance
func BenchmarkServiceStart(b *testing.B) {
	daemonCounts := []int{1, 5, 10, 20} // Significantly reduce daemon count

	for _, count := range daemonCounts {
		b.Run(fmt.Sprintf("daemons_%d", count), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				service := NewDaemonService()

				// Prepare daemons
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

				// Ensure resource cleanup
				service.Stop(syscall.SIGTERM)
				time.Sleep(time.Millisecond) // Give cleanup some time
			}
		})
	}
}

// BenchmarkServiceStop benchmarks service stop performance
func BenchmarkServiceStop(b *testing.B) {
	daemonCounts := []int{1, 5, 10, 20} // Significantly reduce daemon count

	for _, count := range daemonCounts {
		b.Run(fmt.Sprintf("daemons_%d", count), func(b *testing.B) {
			// Limit benchmark iterations to avoid resource accumulation from excessive testing
			if b.N > 1000 {
				b.N = 1000
			}

			for i := 0; i < b.N; i++ {
				b.StopTimer()
				service := NewDaemonService()

				// Prepare daemons
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
				time.Sleep(2 * time.Millisecond) // Increase wait time to ensure startup completion
				b.StartTimer()
				service.Stop(syscall.SIGTERM)
				b.StopTimer()

				// Add extra wait to ensure _LoopInvoker goroutine completely stops
				time.Sleep(time.Millisecond)
				runtime.GC() // Force garbage collection to clean up resources
			}
		})
	}
}

// BenchmarkDaemonRegistration benchmarks daemon registration performance
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

// BenchmarkGetOrderedDaemonEntitySlice benchmarks sorting function performance
func BenchmarkGetOrderedDaemonEntitySlice(b *testing.B) {
	// Prepare different numbers of daemons
	daemonCounts := []int{5, 10, 20, 50} // Reduce count and create new service each time

	for _, count := range daemonCounts {
		b.Run(fmt.Sprintf("daemons_%d", count), func(b *testing.B) {
			service := NewDaemonService()

			// Prepare test data
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

// BenchmarkDaemonMapRange benchmarks DaemonMap.Range performance
func BenchmarkDaemonMapRange(b *testing.B) {
	// Prepare different numbers of daemons
	daemonCounts := []int{5, 10, 20, 50} // Reduce count

	for _, count := range daemonCounts {
		b.Run(fmt.Sprintf("daemons_%d", count), func(b *testing.B) {
			service := NewDaemonService()

			// Prepare test data
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

// BenchmarkMemoryAllocation benchmarks memory allocation performance
func BenchmarkMemoryAllocation(b *testing.B) {
	b.Run("slice_allocation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var el []*DaemonEntity
			for j := 0; j < 50; j++ { // Significantly reduce count
				entity := &DaemonEntity{Name: fmt.Sprintf("entity_%d", j)}
				el = append(el, entity)
			}
			_ = el
		}
	})

	b.Run("pre_allocated_slice", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			el := make([]*DaemonEntity, 0, 50) // Reduce pre-allocated count
			for j := 0; j < 50; j++ {
				entity := &DaemonEntity{Name: fmt.Sprintf("entity_%d", j)}
				el = append(el, entity)
			}
			_ = el
		}
	})
}

// BenchmarkAtomicOperations benchmarks atomic operations performance
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

// BenchmarkConcurrentOperations benchmarks concurrent operations performance
func BenchmarkConcurrentOperations(b *testing.B) {
	service := NewDaemonService()

	// Prepare some daemons
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

// Benchmark helper types
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

// BenchmarkLoopInvokerPerformance benchmarks loop invoker performance
func BenchmarkLoopInvokerPerformance(b *testing.B) {
	service := NewDaemonService()

	// Prepare different numbers of timer daemons
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

// BenchmarkMemoryUsage tests memory usage
func BenchmarkMemoryUsage(b *testing.B) {
	service := NewDaemonService()

	// Prepare a large number of daemons
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
