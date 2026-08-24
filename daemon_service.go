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
	orderMutex            sync.Mutex     // Protects orderIndex and usedOrders access
	usedOrders            map[int]string // Tracks used orders: order -> daemon name
	sig                   chan os.Signal
	signalMutex           sync.Mutex
	signalStopCh          chan struct{}
	stopFuture            concurrent.Future
	shutdownFuture        concurrent.Future
	loopInvokerReload     chan int
	state                 int32
	shutdownState         int32
	invokeLoopDaemonTimer *time.Timer
	timerMutex            sync.Mutex // Protects invokeLoopDaemonTimer and stopFuture access

	// Start admission fields
	startAdmissionMutex sync.Mutex // Serializes daemon admission against the shutdown scan
	startEpoch          int64      // Identifies the running Start; Stop invalidates it to abort admission

	// Slice cache optimization fields
	daemonEntityCache []*DaemonEntity // Cache sorted daemon entity slice (includes only TimerDaemon and SchedulerDaemon)
	daemonCacheMutex  sync.RWMutex    // RW lock for cache protection
	daemonCacheValid  bool            // Whether cache is valid

	// Cache for all daemons (used by Start/Stop methods)
	allDaemonCache      []*DaemonEntity // Cache all daemon entities
	allDaemonCacheMutex sync.RWMutex    // RW lock for all daemon cache protection
	allDaemonCacheValid bool            // Whether all daemon cache is valid
}

func NewDaemonService() (service *DaemonService) {
	ds := &DaemonService{
		StopWhenKill:      true,
		sig:               make(chan os.Signal, 1),
		stopFuture:        concurrent.NewFuture(),
		shutdownFuture:    concurrent.NewFuture(),
		loopInvokerReload: make(chan int),
		usedOrders:        make(map[int]string),
	}
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

func (s *DaemonService) resolveDaemonName(daemon Daemon) (name string, rtErr error) {
	if daemon == nil {
		return "", fmt.Errorf("nil daemon")
	}

	name = daemon.Name()
	if name == "" {
		name = reflect.TypeOf(daemon).Elem().Name()
	}

	if name == "" {
		return "", fmt.Errorf("name is empty")
	}

	if daemon.Name() != name {
		if cast, ok := daemon.(daemonSetName); ok {
			cast.setName(name)
		}
	}

	return name, nil
}

func (s *DaemonService) cloneDaemonEntities(src []*DaemonEntity) (dst []*DaemonEntity) {
	if src == nil {
		return nil
	}

	dst = make([]*DaemonEntity, len(src))
	copy(dst, src)
	return dst
}

func (s *DaemonService) RegisterDaemon(daemon Daemon) (rtErr error) {
	name, err := s.resolveDaemonName(daemon)
	if err != nil {
		return err
	}

	entity := &DaemonEntity{Name: name, Daemon: daemon}
	if _, loaded := s.DaemonMap.LoadOrStore(name, entity); loaded {
		return fmt.Errorf("name is exist")
	}

	s.orderMutex.Lock()
	s.orderIndex++
	order := s.orderIndex
	s.usedOrders[order] = name // Track auto-assigned order
	s.orderMutex.Unlock()

	entity.Order = order

	// Daemon registered successfully, invalidate cache
	s.invalidateDaemonCache()

	atomic.StoreInt32(daemon._State(), StateWait)
	return daemon.Registered()
}

// RegisterDaemonWithOrder registers a daemon with a specific order.
// Order determines startup and shutdown sequence:
//   - Lower order values start earlier (Start phase)
//   - Higher order values stop earlier (Stop phase)
//
// This ensures dependencies are handled correctly: services that start first, stop last.
//
// Panics if the specified order is already used by another daemon.
func (s *DaemonService) RegisterDaemonWithOrder(daemon Daemon, order int) (rtErr error) {
	name, err := s.resolveDaemonName(daemon)
	if err != nil {
		return err
	}

	// Check for order conflict
	s.orderMutex.Lock()
	if existingName, exists := s.usedOrders[order]; exists {
		s.orderMutex.Unlock()
		panic(fmt.Sprintf("order %d is already used by daemon '%s', cannot register daemon '%s' with the same order", order, existingName, name))
	}
	s.usedOrders[order] = name
	s.orderMutex.Unlock()

	// Register daemon with specified order
	if _, loaded := s.DaemonMap.LoadOrStore(name, &DaemonEntity{Name: name, Daemon: daemon, Order: order}); loaded {
		// Rollback usedOrders if daemon name already exists
		s.orderMutex.Lock()
		delete(s.usedOrders, order)
		s.orderMutex.Unlock()
		return fmt.Errorf("name is exist")
	}

	// Daemon registered successfully, invalidate cache
	s.invalidateDaemonCache()

	atomic.StoreInt32(daemon._State(), StateWait)
	return daemon.Registered()
}

func (s *DaemonService) GetDaemon(name string) (entity *DaemonEntity) {
	if v, f := s.DaemonMap.Load(name); f {
		return v.(*DaemonEntity)
	}

	return nil
}

// getAllDaemonEntitySlice gets cached slice of all daemons (used by Start/Stop methods)
func (s *DaemonService) getAllDaemonEntitySlice() (entities []*DaemonEntity) {
	// First try to use cache
	s.allDaemonCacheMutex.RLock()
	if s.allDaemonCacheValid {
		result := s.cloneDaemonEntities(s.allDaemonCache)
		s.allDaemonCacheMutex.RUnlock()
		return result
	}
	s.allDaemonCacheMutex.RUnlock()

	// Cache invalid, need to rebuild
	s.allDaemonCacheMutex.Lock()
	defer s.allDaemonCacheMutex.Unlock()

	// Double check to avoid multiple goroutines rebuilding cache simultaneously
	if s.allDaemonCacheValid {
		return s.cloneDaemonEntities(s.allDaemonCache)
	}

	// Rebuild cache
	var el []*DaemonEntity
	s.DaemonMap.Range(func(_, value interface{}) (next bool) {
		el = append(el, value.(*DaemonEntity))
		return true
	})

	// Update cache
	s.allDaemonCache = el
	s.allDaemonCacheValid = true

	// Return cache copy
	return s.cloneDaemonEntities(el)
}

func (s *DaemonService) UnregisterDaemon(name string) (rtErr error) {
	if v, f := s.DaemonMap.Load(name); f {
		entity := v.(*DaemonEntity)
		var c kkpanic.Caught
		kkpanic.Try(func() {
			c = s.StopDaemon(entity, unregisterSignal)
		}).CatchAll(func(caught kkpanic.Caught) {
			c = caught
		})

		// Clean up usedOrders map
		s.orderMutex.Lock()
		delete(s.usedOrders, entity.Order)
		s.orderMutex.Unlock()

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

func (s *DaemonService) entityNext(entity *DaemonEntity) (next time.Time) {
	entity.nextMutex.RLock()
	defer entity.nextMutex.RUnlock()
	return entity.Next
}

func (s *DaemonService) getOrderedDaemonEntitySlice() (entities []*DaemonEntity) {
	// First try to use cache
	s.daemonCacheMutex.RLock()
	if s.daemonCacheValid {
		result := s.cloneDaemonEntities(s.daemonEntityCache)
		s.daemonCacheMutex.RUnlock()
		return result
	}
	s.daemonCacheMutex.RUnlock()

	// Cache invalid, need to rebuild
	s.daemonCacheMutex.Lock()
	defer s.daemonCacheMutex.Unlock()

	// Double check to avoid multiple goroutines rebuilding cache simultaneously
	if s.daemonCacheValid {
		return s.cloneDaemonEntities(s.daemonEntityCache)
	}

	// Rebuild cache
	var el []*DaemonEntity
	s.DaemonMap.Range(func(_, value interface{}) (next bool) {
		switch value.(*DaemonEntity).Daemon.(type) {
		case TimerDaemon, SchedulerDaemon:
			el = append(el, value.(*DaemonEntity))
		}
		return true
	})

	sort.Slice(el, func(i, j int) (less bool) {
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
	return s.cloneDaemonEntities(el)
}

func (s *DaemonService) Start() (rtErr error) {
	s.startAdmissionMutex.Lock()
	if !atomic.CompareAndSwapInt32(&s.state, StateWait, StateStart) {
		s.startAdmissionMutex.Unlock()
		return kkpanic.Convert("DaemonService not in WAIT state")
	}
	s.startEpoch++
	epoch := s.startEpoch

	// Signal handling belongs to the admitted start epoch. Stop uses the same
	// admission lock, so it cannot tear the epoch down before the handler exists.
	s.startSignalHandling()
	s.startAdmissionMutex.Unlock()

	// Use cache to get all daemons
	el := s.getAllDaemonEntitySlice()

	sort.Slice(el, func(i, j int) (less bool) {
		return el[i].Order < el[j].Order
	})

	for _, entity := range el {
		cycle, starter, err := s.admitStart(epoch, entity)
		if err != nil {
			return err
		}

		if c := s.runStartCycle(entity, cycle, starter); c != nil {
			return c
		}
	}

	if !s.admitLoopInvoker(epoch) {
		return kkpanic.Convert("DaemonService start interrupted by stop")
	}

	return nil
}

// admitStart hands entity to this Start run, or refuses once Stop has taken over.
// The epoch check and the cycle registration share startAdmissionMutex, which is
// what guarantees that every admitted daemon is visible to Stop's shutdown scan
// and that no daemon is launched after that scan.
func (s *DaemonService) admitStart(epoch int64, entity *DaemonEntity) (cycle *daemonStartCycle, starter ContextStarter, rtErr error) {
	s.startAdmissionMutex.Lock()
	defer s.startAdmissionMutex.Unlock()

	if s.startEpoch != epoch {
		return nil, nil, kkpanic.Convert(fmt.Sprintf("%s start interrupted by stop", entity.Daemon.Name()))
	}

	cycle, starter, err := entity.beginStart()
	if err != nil {
		return nil, nil, kkpanic.Convert(err)
	}

	return cycle, starter, nil
}

// admitLoopInvoker starts the loop invoker only while this Start run still owns
// the service, so a Stop that already finished never leaves a live invoker behind.
func (s *DaemonService) admitLoopInvoker(epoch int64) (admitted bool) {
	s.startAdmissionMutex.Lock()
	defer s.startAdmissionMutex.Unlock()

	if s.startEpoch != epoch {
		return false
	}

	s._LoopInvoker()
	return true
}

func (s *DaemonService) StartDaemon(entity *DaemonEntity) (rtCaught kkpanic.Caught) {
	// Direct starts share the service admission barrier. They remain valid while
	// the service is idle or running, but cannot slip behind an active Stop scan.
	s.startAdmissionMutex.Lock()
	if atomic.LoadInt32(&s.state) == StateStop {
		s.startAdmissionMutex.Unlock()
		return kkpanic.Convert(fmt.Sprintf("%s start interrupted by stop", entity.Daemon.Name()))
	}
	cycle, starter, err := entity.beginStart()
	s.startAdmissionMutex.Unlock()
	if err != nil {
		return kkpanic.Convert(err)
	}

	return s.runStartCycle(entity, cycle, starter)
}

// runStartCycle runs the daemon start that cycle was opened for and settles it.
// When Stop claimed the daemon mid-start, this path also owns the single late
// Stop, so Stop is never called concurrently with a still-running Start.
func (s *DaemonService) runStartCycle(entity *DaemonEntity, cycle *daemonStartCycle, starter ContextStarter) (rtCaught kkpanic.Caught) {
	var startErr error
	kkpanic.Try(func() {
		if starter != nil {
			startErr = starter.StartContext(cycle.startCtx)
			if startErr != nil {
				return
			}
		} else {
			entity.Daemon.Start()
		}

		// Scheduling setup is part of startup and stays inside the same panic
		// boundary. A panic here must settle the cycle as a failed start.
		s.entitySetNext(entity)
	}).CatchAll(func(caught kkpanic.Caught) {
		rtCaught = caught
	})

	succeeded := rtCaught == nil && startErr == nil
	outcome, sig := entity.finishStart(cycle, succeeded)
	switch outcome {
	case daemonStartOutcomeStarted:
		s.reloadLoopInvoker()
		kklogger.InfoJ("DaemonService.StartDaemon", fmt.Sprintf("entity %s started", entity.Name))
		return nil
	case daemonStartOutcomeLateStop:
		s.invokeStop(entity, cycle, sig)
		return kkpanic.Convert(fmt.Sprintf("%s start interrupted by stop", entity.Daemon.Name()))
	}

	if rtCaught != nil {
		kklogger.ErrorJ("DaemonService.Start", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, rtCaught.String()))
		return rtCaught
	}

	if startErr != nil {
		kklogger.ErrorJ("DaemonService.Start", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, startErr.Error()))
		return kkpanic.Convert(startErr)
	}

	return nil
}

// reloadLoopInvoker wakes a running loop invoker so a daemon started while the
// service is already up joins the next scheduling round.
func (s *DaemonService) reloadLoopInvoker() {
	s.timerMutex.Lock()
	running := s.invokeLoopDaemonTimer != nil
	stopFuture := s.stopFuture
	s.timerMutex.Unlock()

	if !running {
		return
	}

	// Stop may end the invoker after the timer check. Waiting on the same stop
	// future prevents a reload send from blocking after the receiver has exited.
	select {
	case s.loopInvokerReload <- 1:
	case <-stopFuture.Done():
	}
}

func (s *DaemonService) _LoopInvoker() {
	s.timerMutex.Lock()
	s.invokeLoopDaemonTimer = time.NewTimer(time.Second)
	s.invokeLoopDaemonTimer.Stop()
	s.timerMutex.Unlock()

	go func() {
		for {
			now := time.Now()
			next := _MaxTime
			needsCacheInvalidation := false

			updateNext := func(t time.Time) {
				if next.After(t) {
					next = t
				}
			}

			for _, entity := range s.getOrderedDaemonEntitySlice() {
				entityNext := s.entityNext(entity)
				isDue := !entityNext.After(now)
				if isDue {
					if cycle, admitted := entity.beginLoop(); admitted {
						// Set next execution time immediately before starting goroutine
						s.entitySetNext(entity)
						needsCacheInvalidation = true

						go func(entity *DaemonEntity, cycle *daemonStartCycle) {
							defer entity.finishLoop(cycle)

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
						}(entity, cycle)
					}
				}

				updateNext(s.entityNext(entity))
				if !isDue {
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
	}()
}

func (s *DaemonService) Stop(sig os.Signal) (rtErr error) {
	// Changing the service state and invalidating the epoch under one admission
	// lock is the shutdown linearization point. Every earlier admission is visible
	// to the shutdown scan, and no later daemon or signal handler can be admitted.
	s.startAdmissionMutex.Lock()
	if !atomic.CompareAndSwapInt32(&s.state, StateStart, StateStop) {
		s.startAdmissionMutex.Unlock()
		return kkpanic.Convert("DaemonService not in START state")
	}
	s.startEpoch++
	s.startAdmissionMutex.Unlock()

	s.stopSignalHandling()

	defer func() {
		s.timerMutex.Lock()
		s.stopFuture = concurrent.NewFuture()
		s.timerMutex.Unlock()
		atomic.StoreInt32(&s.state, StateWait)
	}()

	s.timerMutex.Lock()
	s.stopFuture.Completable().Complete(nil)
	s.timerMutex.Unlock()

	// Use cache to get all daemons
	el := s.getAllDaemonEntitySlice()

	sort.Slice(el, func(i, j int) (less bool) {
		return el[i].Order > el[j].Order
	})

	s.timerMutex.Lock()
	if s.invokeLoopDaemonTimer != nil {
		s.invokeLoopDaemonTimer.Stop()
		s.invokeLoopDaemonTimer = nil
	}
	s.timerMutex.Unlock()

	for _, entity := range el {
		s.shutdownEntity(entity, sig)
	}

	return nil
}

// shutdownEntity stops one daemon on behalf of Stop. Daemons that never started
// successfully are skipped, a cancellable startup is cancelled and awaited, and a
// legacy blocking Start is only marked so Stop never waits for it.
func (s *DaemonService) shutdownEntity(entity *DaemonEntity, sig os.Signal) {
	claim, err := entity.claimStop(sig, false)
	if err != nil {
		kklogger.ErrorJ("DaemonService.Stop", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, err.Error()))
		return
	}

	switch claim.action {
	case daemonStopInvoke:
		s.invokeStop(entity, claim.cycle, sig)
	case daemonStopWait:
		<-claim.cycle.done
	case daemonStopDeferred:
		kklogger.InfoJ("DaemonService.Stop", fmt.Sprintf("entity %s stop deferred until its blocking start returns", entity.Name))
	}
}

func (s *DaemonService) StopDaemon(entity *DaemonEntity, sig os.Signal) (rtCaught kkpanic.Caught) {
	claim, err := entity.claimStop(sig, true)
	if err != nil {
		return kkpanic.Convert(err)
	}

	switch claim.action {
	case daemonStopInvoke:
		return s.invokeStop(entity, claim.cycle, sig)
	case daemonStopWait, daemonStopDeferred:
		// Direct callers own completion semantics. Unlike service-wide shutdown,
		// they wait so a successful return means the daemon is actually stopped.
		<-claim.cycle.done
		return claim.cycle.stopCaught
	}

	return nil
}

// invokeStop runs the daemon Stop the caller claimed and releases the daemon.
func (s *DaemonService) invokeStop(entity *DaemonEntity, cycle *daemonStartCycle, sig os.Signal) (rtCaught kkpanic.Caught) {
	kkpanic.Try(func() {
		entity.Daemon.Stop(sig)
		kklogger.InfoJ("DaemonService.StopDaemon", fmt.Sprintf("entity %s stopped", entity.Name))
	}).CatchAll(func(caught kkpanic.Caught) {
		rtCaught = caught
		kklogger.ErrorJ("DaemonService.Stop", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, caught.String()))
	})

	entity.completeStop(cycle, rtCaught)
	return rtCaught
}

func (s *DaemonService) IsShutdown() (shutdown bool) {
	return atomic.LoadInt32(&s.shutdownState) == 1
}

func (s *DaemonService) ShutdownGracefully() {
	if !atomic.CompareAndSwapInt32(&s.shutdownState, 0, 1) {
		return
	}

	if atomic.LoadInt32(&s.state) != StateStart {
		s.shutdownFuture.Completable().Complete(shutdownGracefullySignal)
		return
	}

	select {
	case s.sig <- shutdownGracefullySignal:
	default:
	}
}

func (s *DaemonService) ShutdownFuture() (future concurrent.Future) {
	return s.shutdownFuture
}

func (s *DaemonService) startSignalHandling() {
	s.signalMutex.Lock()
	defer s.signalMutex.Unlock()

	if s.signalStopCh != nil {
		return
	}

drainStart:
	for {
		select {
		case <-s.sig:
			continue
		default:
			break drainStart
		}
	}

	s.signalStopCh = make(chan struct{})
	signal.Notify(s.sig, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	go s.judgeStopWhenKill(s.signalStopCh)
}

func (s *DaemonService) stopSignalHandling() {
	s.signalMutex.Lock()
	stopCh := s.signalStopCh
	if stopCh == nil {
		s.signalMutex.Unlock()
		return
	}
	s.signalStopCh = nil
	signal.Stop(s.sig)
	close(stopCh)

drainStop:
	for {
		select {
		case <-s.sig:
			continue
		default:
			break drainStop
		}
	}

	s.signalMutex.Unlock()
}

func (s *DaemonService) judgeStopWhenKill(stopCh <-chan struct{}) {
	select {
	case sig := <-s.sig:
		atomic.StoreInt32(&s.shutdownState, 1)
		if !s.StopWhenKill && sig != shutdownGracefullySignal {
			s.shutdownFuture.Completable().Complete(sig)
			return
		}

		kklogger.InfoJ("DaemonService:judgeStopWhenKill", fmt.Sprintf("SIGNAL: %s, SHUTDOWN CATCH", sig.String()))
		_ = s.Stop(sig)
		kklogger.InfoJ("DaemonService:judgeStopWhenKill", "Done")
		s.shutdownFuture.Completable().Complete(sig)
	case <-stopCh:
		return
	}
}
