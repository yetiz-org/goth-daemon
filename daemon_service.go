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

	concurrent "github.com/kklab-com/goth-concurrent"
	kklogger "github.com/kklab-com/goth-kklogger"
	kkpanic "github.com/kklab-com/goth-panic"
)

type DaemonService struct {
	// daemons map
	DaemonMap sync.Map
	// stop all daemon when get kill signal, default: `true`
	StopWhenKill          bool
	orderIndex            int
	sig                   chan os.Signal
	stopFuture            concurrent.Future
	shutdownFuture        concurrent.Future
	loopInvokerReload     chan int
	state                 int32
	shutdownState         int32
	invokeLoopDaemonTimer *time.Timer
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
		s.orderIndex++
		v.(*DaemonEntity).Order = s.orderIndex
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

func (s *DaemonService) UnregisterDaemon(name string) error {
	if v, f := s.DaemonMap.Load(name); f {
		var c kkpanic.Caught
		kkpanic.Try(func() {
			c = s.StopDaemon(v.(*DaemonEntity), unregisterSignal)
		}).CatchAll(func(caught kkpanic.Caught) {
			c = caught
		})

		s.DaemonMap.Delete(name)
		return c
	}

	return nil
}

func (s *DaemonService) entitySetNext(entity *DaemonEntity) {
	switch daemon := entity.Daemon.(type) {
	case TimerDaemon:
		interval := daemon.Interval()
		entity.Next = time.Now().Truncate(interval).Add(interval)
	case SchedulerDaemon:
		entity.Next = daemon.When().Next(time.Now())
	}
}

func (s *DaemonService) getOrderedDaemonEntitySlice() []*DaemonEntity {
	var el []*DaemonEntity
	s.DaemonMap.Range(func(key, value interface{}) bool {
		switch value.(*DaemonEntity).Daemon.(type) {
		case TimerDaemon, SchedulerDaemon:
			el = append(el, value.(*DaemonEntity))
		}

		return true
	})

	sort.Slice(el, func(i, j int) bool {
		return el[i].Next.Before(el[j].Next)
	})

	return el
}

func (s *DaemonService) Start() error {
	if !atomic.CompareAndSwapInt32(&s.state, StateWait, StateStart) {
		return kkpanic.Convert("DaemonService not in WAIT state")
	}

	var el []*DaemonEntity
	s.DaemonMap.Range(func(key, value interface{}) bool {
		el = append(el, value.(*DaemonEntity))
		return true
	})

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
	s.invokeLoopDaemonTimer = time.NewTimer(time.Second)
	s.invokeLoopDaemonTimer.Stop()
	go func(s *DaemonService) {
		for {
			now := time.Now()
			next := _MaxTime
			for _, entity := range s.getOrderedDaemonEntitySlice() {
				now = time.Now()
				if entity.Next.Before(now) {
					if atomic.CompareAndSwapInt32(entity.Daemon._State(), StateStart, StateRun) {
						go func(entity *DaemonEntity) {
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

							atomic.StoreInt32(entity.Daemon._State(), StateStart)
						}(entity)
					}

					s.entitySetNext(entity)
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

			if s.invokeLoopDaemonTimer == nil {
				return
			}

			wait := next.Sub(now)
			if next.Before(now) {
				wait = time.Microsecond
			}

			s.invokeLoopDaemonTimer.Reset(wait)
			select {
			case <-s.invokeLoopDaemonTimer.C:
				continue
			case <-s.loopInvokerReload:
				s.invokeLoopDaemonTimer.Stop()
				continue
			case <-s.stopFuture.Done():
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
		s.stopFuture = concurrent.NewFuture()
		s.state = StateWait
	}(s)

	s.stopFuture.Completable().Complete(nil)

	var el []*DaemonEntity
	s.DaemonMap.Range(func(key, value interface{}) bool {
		el = append(el, value.(*DaemonEntity))
		return true
	})

	sort.Slice(el, func(i, j int) bool {
		return el[i].Order > el[j].Order
	})

	if s.invokeLoopDaemonTimer != nil {
		s.invokeLoopDaemonTimer.Stop()
		s.invokeLoopDaemonTimer = nil
	}

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
		s.shutdownState = 1
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
