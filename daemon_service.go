package kkdaemon

import (
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	concurrent "github.com/yetiz-org/goth-concurrent"
	kklogger "github.com/yetiz-org/goth-kklogger"
	kkpanic "github.com/yetiz-org/goth-panic"
)

type DaemonService struct {
	// daemons map
	DaemonMap sync.Map
	// stop all daemon when get kill signal, default: `true`
	StopWhenKill          bool
	orderIndex            int
	orderMutex            sync.Mutex // Protects orderIndex access
	sig                   chan os.Signal
	stopFuture            concurrent.Future
	shutdownFuture        concurrent.Future
	loopInvokerReload     chan int
	state                 int32
	shutdownState         int32
	invokeLoopDaemonTimer *time.Timer
	timerMutex            sync.Mutex // Protects invokeLoopDaemonTimer and stopFuture access

	// Slice cache optimization fields
	daemonEntityCache []*DaemonEntity // Cache sorted daemon entity slice (includes only TimerDaemon and SchedulerDaemon)
	daemonCacheMutex  sync.RWMutex    // RW lock for cache protection
	daemonCacheValid  bool            // Whether cache is valid

	// Cache for all daemons (used by Start/Stop methods)
	allDaemonCache      []*DaemonEntity // Cache all daemon entities
	allDaemonCacheMutex sync.RWMutex    // RW lock for all daemon cache protection
	allDaemonCacheValid bool            // Whether all daemon cache is valid
}

func NewDaemonService() *DaemonService {
	ds := &DaemonService{
		StopWhenKill:      true,
		sig:               make(chan os.Signal),
		stopFuture:        concurrent.NewFuture(),
		shutdownFuture:    concurrent.NewFuture(),
		loopInvokerReload: make(chan int),
	}

	signal.Notify(ds.sig, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGHUP)
	go ds.judgeStopWhenKill()
	return ds
}

// invalidateDaemonCache invalidates daemon cache
func (s *DaemonService) invalidateDaemonCache() {
	// Invalidate timer and scheduler daemon cache
	s.daemonCacheMutex.Lock()
	s.daemonCacheValid = false
	s.daemonEntityCache = nil
	s.daemonCacheMutex.Unlock()

	// Invalidate all daemon cache
	s.allDaemonCacheMutex.Lock()
	s.allDaemonCacheValid = false
	s.allDaemonCache = nil
	s.allDaemonCacheMutex.Unlock()
}

func (s *DaemonService) RegisterDaemon(daemon Daemon) error {
	if daemon == nil {
		return fmt.Errorf("nil daemon")
	}

	name := daemon.Name()
	if name == "" {
		name = reflect.TypeOf(daemon).Elem().Name()
	}

	if name == "" {
		return fmt.Errorf("name is empty")
	}

	if daemon.Name() != name {
		if cast, ok := daemon.(daemonSetName); ok {
			cast.setName(name)
		}
	}

	if v, loaded := s.DaemonMap.LoadOrStore(name, &DaemonEntity{Name: name, Daemon: daemon}); loaded {
		return fmt.Errorf("name is exist")
	} else {
		s.orderMutex.Lock()
		s.orderIndex++
		order := s.orderIndex
		s.orderMutex.Unlock()

		v.(*DaemonEntity).Order = order
		// Daemon registered successfully, invalidate cache
		s.invalidateDaemonCache()
	}

	atomic.StoreInt32(daemon._State(), StateWait)
	return daemon.Registered()
}

func (s *DaemonService) GetDaemon(name string) *DaemonEntity {
	if v, f := s.DaemonMap.Load(name); f {
		return v.(*DaemonEntity)
	}

	return nil
}

// getAllDaemonEntitySlice gets cached slice of all daemons (used by Start/Stop methods)
func (s *DaemonService) getAllDaemonEntitySlice() []*DaemonEntity {
	// First try to use cache
	s.allDaemonCacheMutex.RLock()
	if s.allDaemonCacheValid && s.allDaemonCache != nil {
		// Create cache copy to avoid concurrent modifications
		result := make([]*DaemonEntity, len(s.allDaemonCache))
		copy(result, s.allDaemonCache)
		s.allDaemonCacheMutex.RUnlock()
		return result
	}
	s.allDaemonCacheMutex.RUnlock()

	// Cache invalid, need to rebuild
	s.allDaemonCacheMutex.Lock()
	defer s.allDaemonCacheMutex.Unlock()

	// Double check to avoid multiple goroutines rebuilding cache simultaneously
	if s.allDaemonCacheValid && s.allDaemonCache != nil {
		result := make([]*DaemonEntity, len(s.allDaemonCache))
		copy(result, s.allDaemonCache)
		return result
	}

	// Rebuild cache
	var el []*DaemonEntity
	s.DaemonMap.Range(func(key, value interface{}) bool {
		el = append(el, value.(*DaemonEntity))
		return true
	})

	// Update cache
	s.allDaemonCache = el
	s.allDaemonCacheValid = true

	// Return cache copy
	result := make([]*DaemonEntity, len(el))
	copy(result, el)
	return result
}

func (s *DaemonService) UnregisterDaemon(name string) error {
	if v, f := s.DaemonMap.Load(name); f {
		var c kkpanic.Caught
		kkpanic.Try(func() {
			c = s.StopDaemon(v.(*DaemonEntity), unregisterSignal)
		}).CatchAll(func(caught kkpanic.Caught) {
			c = caught
		})

		s.DaemonMap.Delete(name)
		// Daemon unregistered successfully, invalidate cache
		s.invalidateDaemonCache()
		return c
	}

	return nil
}

func (s *DaemonService) entitySetNext(entity *DaemonEntity) {
	entity.nextMutex.Lock()
	defer entity.nextMutex.Unlock()

	switch daemon := entity.Daemon.(type) {
	case TimerDaemon:
		interval := daemon.Interval()
		entity.Next = time.Now().Truncate(interval).Add(interval)
	case SchedulerDaemon:
		entity.Next = daemon.When().Next(time.Now())
	}
}

func (s *DaemonService) getOrderedDaemonEntitySlice() []*DaemonEntity {
	// First try to use cache
	s.daemonCacheMutex.RLock()
	if s.daemonCacheValid && s.daemonEntityCache != nil {
		// Create cache copy to avoid concurrent modifications
		result := make([]*DaemonEntity, len(s.daemonEntityCache))
		copy(result, s.daemonEntityCache)
		s.daemonCacheMutex.RUnlock()
		return result
	}
	s.daemonCacheMutex.RUnlock()

	// Cache invalid, need to rebuild
	s.daemonCacheMutex.Lock()
	defer s.daemonCacheMutex.Unlock()

	// Double check to avoid multiple goroutines rebuilding cache simultaneously
	if s.daemonCacheValid && s.daemonEntityCache != nil {
		result := make([]*DaemonEntity, len(s.daemonEntityCache))
		copy(result, s.daemonEntityCache)
		return result
	}

	// Rebuild cache
	var el []*DaemonEntity
	s.DaemonMap.Range(func(key, value interface{}) bool {
		switch value.(*DaemonEntity).Daemon.(type) {
		case TimerDaemon, SchedulerDaemon:
			el = append(el, value.(*DaemonEntity))
		}
		return true
	})

	sort.Slice(el, func(i, j int) bool {
		el[i].nextMutex.RLock()
		el[j].nextMutex.RLock()
		result := el[i].Next.Before(el[j].Next)
		el[j].nextMutex.RUnlock()
		el[i].nextMutex.RUnlock()
		return result
	})

	// Update cache
	s.daemonEntityCache = el
	s.daemonCacheValid = true

	// Return cache copy
	result := make([]*DaemonEntity, len(el))
	copy(result, el)
	return result
}

func (s *DaemonService) Start() error {
	if !atomic.CompareAndSwapInt32(&s.state, StateWait, StateStart) {
		return kkpanic.Convert("DaemonService not in WAIT state")
	}

	// Use cache to get all daemons
	el := s.getAllDaemonEntitySlice()

	sort.Slice(el, func(i, j int) bool {
		return el[i].Order < el[j].Order
	})

	for _, entity := range el {
		if c := s.StartDaemon(entity); c != nil {
			return c
		}
	}

	s._LoopInvoker()
	return nil
}

func (s *DaemonService) StartDaemon(entity *DaemonEntity) kkpanic.Caught {
	if !atomic.CompareAndSwapInt32(entity.Daemon._State(), StateWait, StateStart) {
		return kkpanic.Convert(fmt.Sprintf("%s not in WAIT state", entity.Daemon.Name()))
	}

	var c kkpanic.Caught
	kkpanic.Try(func() {
		atomic.StoreInt32(entity.Daemon._State(), StateRun)
		entity.Daemon.Start()
		s.entitySetNext(entity)
		if s.invokeLoopDaemonTimer != nil {
			s.loopInvokerReload <- 1
		}

		kklogger.InfoJ("DaemonService.StartDaemon", fmt.Sprintf("entity %s started", entity.Name))
	}).CatchAll(func(caught kkpanic.Caught) {
		c = caught
		kklogger.ErrorJ("DaemonService.Start", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, caught.String()))
	}).Finally(func() {
		atomic.StoreInt32(entity.Daemon._State(), StateStart)
	})

	return c
}

func (s *DaemonService) _LoopInvoker() {
	s.timerMutex.Lock()
	s.invokeLoopDaemonTimer = time.NewTimer(time.Second)
	s.invokeLoopDaemonTimer.Stop()
	s.timerMutex.Unlock()

	go func(s *DaemonService) {
		for {
			now := time.Now()
			next := _MaxTime
			needsCacheInvalidation := false

			for _, entity := range s.getOrderedDaemonEntitySlice() {
				now = time.Now()
				if entity.Next.Before(now) || entity.Next.Equal(now) {
					if atomic.CompareAndSwapInt32(entity.Daemon._State(), StateStart, StateRun) {
						// Set next execution time immediately before starting goroutine
						s.entitySetNext(entity)
						needsCacheInvalidation = true

						go func(entity *DaemonEntity) {
							defer func() {
								atomic.StoreInt32(entity.Daemon._State(), StateStart)
							}()

							if looper, ok := entity.Daemon.(Looper); ok {
								kkpanic.Catch(func() {
									kklogger.TraceJ("DaemonService._LoopInvoker#Run", entity.Name)
									if err := looper.Loop(); err != nil {
										kklogger.ErrorJ(fmt.Sprintf("DaemonService._LoopInvoker#Err!%s", entity.Name), err.Error())
									} else {
										kklogger.TraceJ("DaemonService._LoopInvoker#Done", entity.Name)
									}
								}, func(r kkpanic.Caught) {
									kklogger.ErrorJ("panic.Log", r)
								})
							}
						}(entity)
					}

					if next.After(entity.Next) {
						next = entity.Next
					}
				} else {
					if next.After(entity.Next) {
						next = entity.Next
					}
					break
				}
			}

			// Invalidate cache if any daemon was executed and next time was updated
			if needsCacheInvalidation {
				s.invalidateDaemonCache()
			}

			s.timerMutex.Lock()
			timer := s.invokeLoopDaemonTimer
			if timer == nil {
				s.timerMutex.Unlock()
				return
			}
			s.timerMutex.Unlock()

			wait := next.Sub(now)
			if next.Before(now) {
				wait = time.Microsecond
			}

			timer.Reset(wait)

			// Get stopFuture safely
			s.timerMutex.Lock()
			stopFuture := s.stopFuture
			s.timerMutex.Unlock()

			select {
			case <-timer.C:
				continue
			case <-s.loopInvokerReload:
				s.timerMutex.Lock()
				if currentTimer := s.invokeLoopDaemonTimer; currentTimer != nil {
					currentTimer.Stop()
				}
				s.timerMutex.Unlock()
				continue
			case <-stopFuture.Done():
				return
			}
		}
	}(s)
}

func (s *DaemonService) Stop(sig os.Signal) error {
	if !atomic.CompareAndSwapInt32(&s.state, StateStart, StateStop) {
		return kkpanic.Convert("DaemonService not in START state")
	}

	defer func(s *DaemonService) {
		s.timerMutex.Lock()
		s.stopFuture = concurrent.NewFuture()
		s.timerMutex.Unlock()
		atomic.StoreInt32(&s.state, StateWait)
	}(s)

	s.timerMutex.Lock()
	s.stopFuture.Completable().Complete(nil)
	s.timerMutex.Unlock()

	// Use cache to get all daemons
	el := s.getAllDaemonEntitySlice()

	sort.Slice(el, func(i, j int) bool {
		return el[i].Order > el[j].Order
	})

	s.timerMutex.Lock()
	if s.invokeLoopDaemonTimer != nil {
		s.invokeLoopDaemonTimer.Stop()
		s.invokeLoopDaemonTimer = nil
	}
	s.timerMutex.Unlock()

	for _, entity := range el {
		if c := s.StopDaemon(entity, sig); c != nil {
			kklogger.ErrorJ("DaemonService.Stop", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, c.String()))
		}
	}

	return nil
}

func (s *DaemonService) StopDaemon(entity *DaemonEntity, sig os.Signal) kkpanic.Caught {
	defer func() { atomic.StoreInt32(entity.Daemon._State(), StateWait) }()
	if !atomic.CompareAndSwapInt32(entity.Daemon._State(), StateStart, StateStop) &&
		!atomic.CompareAndSwapInt32(entity.Daemon._State(), StateRun, StateStop) {
		return kkpanic.Convert(fmt.Sprintf("%s not in START/RUN state", entity.Daemon.Name()))
	}

	var c kkpanic.Caught
	kkpanic.Try(func() {
		entity.Daemon.Stop(sig)
		kklogger.InfoJ("DaemonService.StopDaemon", fmt.Sprintf("entity %s stopped", entity.Name))
	}).CatchAll(func(caught kkpanic.Caught) {
		c = caught
		kklogger.ErrorJ("DaemonService.Stop", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, caught.String()))
	})

	return c
}

func (s *DaemonService) IsShutdown() bool {
	return s.shutdownState == 1
}

func (s *DaemonService) ShutdownGracefully() {
	if atomic.CompareAndSwapInt32(&s.shutdownState, 0, 1) {
		s.sig <- shutdownGracefullySignal
	}
}

func (s *DaemonService) ShutdownFuture() concurrent.Future {
	return s.shutdownFuture
}

func (s *DaemonService) judgeStopWhenKill() {
	go func(s *DaemonService) {
		sig := <-s.sig
		atomic.StoreInt32(&s.shutdownState, 1)
		if !s.StopWhenKill && sig != shutdownGracefullySignal {
			s.shutdownFuture.Completable().Complete(sig)
			return
		}

		kklogger.InfoJ("DaemonService:judgeStopWhenKill", fmt.Sprintf("SIGNAL: %s, SHUTDOWN CATCH", sig.String()))
		s.Stop(sig)
		kklogger.InfoJ("DaemonService:judgeStopWhenKill", "Done")
		s.shutdownFuture.Completable().Complete(sig)
	}(s)
}
