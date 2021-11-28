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

	kklogger "github.com/kklab-com/goth-kklogger"
	"github.com/kklab-com/goth-kkutil/concurrent"
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
	shutdownState         int32
	invokeLoopDaemonTimer *time.Timer
}

func NewDaemonService() *DaemonService {
	ds := &DaemonService{
		StopWhenKill:   true,
		sig:            make(chan os.Signal),
		stopFuture:     concurrent.NewFuture(nil),
		shutdownFuture: concurrent.NewFuture(nil),
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
			s.stopDaemon(v.(*DaemonEntity), unregisterSignal)
		}).CatchAll(func(caught kkpanic.Caught) {
			c = caught
		})

		s.DaemonMap.Delete(name)
		return c
	}

	return nil
}

func (s *DaemonService) startDaemon(entity *DaemonEntity) {
	if !atomic.CompareAndSwapInt32(entity.Daemon._State(), StateWait, StateStart) {
		kklogger.ErrorJ("DaemonService.startDaemon", fmt.Sprintf("%s not in WAIT state", entity.Daemon.Name()))
		return
	}

	atomic.StoreInt32(entity.Daemon._State(), StateRun)
	entity.Daemon.Start()
	atomic.StoreInt32(entity.Daemon._State(), StateStart)
	s.entitySetNext(entity)
	kklogger.InfoJ("DaemonService.startDaemon", fmt.Sprintf("entity %s started", entity.Name))
}

func (s *DaemonService) invokeLoopDaemon() {
	s.invokeLoopDaemonTimer = time.NewTimer(time.Second)
	s.invokeLoopDaemonTimer.Stop()
	go func() {
		el := s.getOrderedDaemonEntitySlice()
		if len(el) == 0 {
			return
		}

		for {
			now := time.Now()
			next := _MaxTime
			for _, entity := range s.getOrderedDaemonEntitySlice() {
				now = time.Now()
				if !entity.Next.After(now) {
					if atomic.CompareAndSwapInt32(entity.Daemon._State(), StateStart, StateRun) {
						go func(entity *DaemonEntity) {
							if looper, ok := entity.Daemon.(Looper); ok {
								kkpanic.LogCatch(func() {
									kklogger.TraceJ("DaemonService.invokeLoopDaemon#Run", entity.Name)
									if err := looper.Loop(); err != nil {
										kklogger.ErrorJ(fmt.Sprintf("DaemonService.invokeLoopDaemon#Err!%s", entity.Name), err.Error())
									} else {
										kklogger.TraceJ("DaemonService.invokeLoopDaemon#Done", entity.Name)
									}
								})
							}
						}(entity)

						s.entitySetNext(entity)
						if next.After(entity.Next) {
							next = entity.Next
						}

						atomic.StoreInt32(entity.Daemon._State(), StateStart)
					}
				} else {
					if next.After(entity.Next) {
						next = entity.Next
					}

					break
				}
			}

			s.invokeLoopDaemonTimer.Reset(next.Sub(now))
			select {
			case <-s.invokeLoopDaemonTimer.C:
				continue
			case <-s.stopFuture.Done():
				return
			}
		}
	}()
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

func (s *DaemonService) stopDaemon(entity *DaemonEntity, sig os.Signal) {
	defer func() { atomic.StoreInt32(entity.Daemon._State(), StateWait) }()
	if !atomic.CompareAndSwapInt32(entity.Daemon._State(), StateStart, StateStop) &&
		!atomic.CompareAndSwapInt32(entity.Daemon._State(), StateRun, StateStop) {
		kklogger.ErrorJ("DaemonService.stopDaemon", fmt.Sprintf("%s not in START/RUN state", entity.Daemon.Name()))
		return
	}

	entity.Daemon.Stop(sig)
	kklogger.InfoJ("DaemonService.stopDaemon", fmt.Sprintf("entity %s stopped", entity.Name))
}

func (s *DaemonService) Start() {
	var el []*DaemonEntity
	s.DaemonMap.Range(func(key, value interface{}) bool {
		el = append(el, value.(*DaemonEntity))
		return true
	})

	sort.Slice(el, func(i, j int) bool {
		return el[i].Order < el[j].Order
	})

	for _, entity := range el {
		var c kkpanic.Caught
		kkpanic.Try(func() {
			s.startDaemon(entity)
		}).CatchAll(func(caught kkpanic.Caught) {
			c = caught
			kklogger.ErrorJ("DaemonService.Start", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, caught.String()))
		})

		if c != nil {
			s.ShutdownGracefully()
			return
		}
	}

	s.invokeLoopDaemon()
}

func (s *DaemonService) Stop(sig os.Signal) {
	defer func() { s.stopFuture = concurrent.NewFuture(nil) }()
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
		var c kkpanic.Caught
		kkpanic.Try(func() {
			s.stopDaemon(entity, sig)
		}).CatchAll(func(caught kkpanic.Caught) {
			c = caught
			kklogger.ErrorJ("DaemonService.Stop", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, caught.String()))
		})

		if c != nil {
			panic(&PanicResult{
				Daemon: entity.Daemon,
				Caught: c,
			})
		}
	}
}

func (s *DaemonService) IsShutdown() bool {
	return s.shutdownState == 1
}

func (s *DaemonService) ShutdownGracefully() {
	if atomic.CompareAndSwapInt32(&s.shutdownState, 0, 1) {
		if !s.IsShutdown() {
			s.sig <- shutdownGracefullySignal
		}
	}
}

func (s *DaemonService) ShutdownFuture() concurrent.Future {
	return s.shutdownFuture
}

func (s *DaemonService) judgeStopWhenKill() {
	go func() {
		sig := <-s.sig
		if !s.StopWhenKill && sig != shutdownGracefullySignal {
			s.shutdownFuture.Completable().Complete(sig)
			return
		}

		kklogger.InfoJ("DaemonService:judgeStopWhenKill", fmt.Sprintf("SIGNAL: %s, SHUTDOWN CATCH", sig.String()))
		s.Stop(sig)
		kklogger.InfoJ("DaemonService:judgeStopWhenKill", "Done")
		s.shutdownFuture.Completable().Complete(sig)
	}()
}
