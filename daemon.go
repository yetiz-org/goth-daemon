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

	kklogger "github.com/kklab-com/goth-kklogger"
	"github.com/kklab-com/goth-kkutil/concurrent"
	kkpanic "github.com/kklab-com/goth-panic"
)

var DaemonMap = sync.Map{}
var AutoStopWhenKill = true
var sig = make(chan os.Signal)
var stopWhenKillDoneFuture = concurrent.NewFuture(nil)
var shutdown = false
var shutdownOnce = sync.Once{}

const StateWait = int32(0)
const StateRun = int32(1)
const StateStop = int32(2)

const shutdownGracefullySignal = syscall.Signal(0xff)

type DaemonEntity struct {
	Name    string
	Daemon  Daemon
	Order   int
	started bool
}

type Daemon interface {
	Registered() error
	State() int32
	Start()
	Stop(sig os.Signal)
	Name() string
	_State() *int32
}

type daemonSetName interface {
	setName(name string)
}

type DefaultDaemon struct {
	name   string
	state  int32
	Params map[string]interface{}
}

func (d *DefaultDaemon) Registered() error {
	return nil
}

func (d *DefaultDaemon) State() int32 {
	return d.state
}

func (d *DefaultDaemon) Start() {

}

func (d *DefaultDaemon) Stop(sig os.Signal) {

}

func (d *DefaultDaemon) setName(name string) {
	d.name = name
}

func (d *DefaultDaemon) Name() string {
	return d.name
}

func (d *DefaultDaemon) _State() *int32 {
	return &d.state
}

func RegisterDaemon(order int, daemon Daemon) error {
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

	if _, loaded := DaemonMap.LoadOrStore(name, &DaemonEntity{Name: name, Order: order, Daemon: daemon}); loaded {
		return fmt.Errorf("name is exist")
	}

	if cast, ok := daemon.(TimerDaemon); ok {
		cast.prepare()
	}

	atomic.StoreInt32(daemon._State(), StateWait)
	return daemon.Registered()
}

type _InlineService struct {
	DefaultDaemon
	StartFunc func()
	StopFunc  func(sig os.Signal)
}

func (s *_InlineService) Name() string {
	return s.name
}

func (s *_InlineService) Start() {
	if s.StartFunc != nil {
		s.StartFunc()
	}
}

func (s *_InlineService) Stop(sig os.Signal) {
	if s.StopFunc != nil {
		s.StopFunc(sig)
	}
}

func RegisterServiceInline(name string, order int, startFunc func(), stopFunc func(sig os.Signal)) error {
	return RegisterDaemon(order, &_InlineService{
		DefaultDaemon: DefaultDaemon{
			name: name,
		},
		StartFunc: startFunc,
		StopFunc:  stopFunc,
	})
}

func GetService(name string) *DaemonEntity {
	if v, f := DaemonMap.Load(name); f {
		return v.(*DaemonEntity)
	}

	return nil
}

func Start() {
	var el []*DaemonEntity
	DaemonMap.Range(func(key, value interface{}) bool {
		el = append(el, value.(*DaemonEntity))
		return true
	})

	sort.Slice(el, func(i, j int) bool {
		return el[i].Order < el[j].Order
	})

	for _, entity := range el {
		var c kkpanic.Caught
		kkpanic.Try(func() {
			if !atomic.CompareAndSwapInt32(entity.Daemon._State(), StateWait, StateRun) {
				kklogger.ErrorJ("kkdaemon.Start", fmt.Sprintf("%s not in WAIT state", entity.Daemon.Name()))
				return
			}

			if daemon, ok := entity.Daemon.(TimerDaemon); ok {
				timerDaemonStart(daemon)
			} else {
				entity.Daemon.Start()
			}

			entity.started = true
			kklogger.InfoJ("kkdaemon.Start", fmt.Sprintf("entity %s started", entity.Name))
		}).CatchAll(func(caught kkpanic.Caught) {
			c = caught
			kklogger.ErrorJ("kkdaemon.Start", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, caught.String()))
		})

		if c != nil {
			ShutdownGracefully()
			return
		}
	}
}

func Stop(sig os.Signal) {
	var el []*DaemonEntity
	DaemonMap.Range(func(key, value interface{}) bool {
		el = append(el, value.(*DaemonEntity))
		return true
	})

	sort.Slice(el, func(i, j int) bool {
		return el[i].Order > el[j].Order
	})

	for _, entity := range el {
		if !entity.started {
			continue
		}

		var c kkpanic.Caught
		kkpanic.Try(func() {
			if !atomic.CompareAndSwapInt32(entity.Daemon._State(), StateRun, StateStop) {
				kklogger.ErrorJ("kkdaemon.Stop", fmt.Sprintf("%s not in RUN state", entity.Daemon.Name()))
				return
			}

			if daemon, ok := entity.Daemon.(TimerDaemon); ok {
				timerDaemonStop(daemon, sig)
			} else {
				entity.Daemon.Stop(sig)
			}

			kklogger.InfoJ("kkdaemon.Stop", fmt.Sprintf("entity %s stopped", entity.Name))
		}).CatchAll(func(caught kkpanic.Caught) {
			c = caught
			kklogger.ErrorJ("kkdaemon.Stop", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, caught.String()))
		})

		if c != nil {
			panic(&PanicResult{
				Daemon: entity.Daemon,
				Caught: c,
			})
		}
	}
}

func IsShutdown() bool {
	return shutdown
}

func ShutdownGracefully() {
	shutdownOnce.Do(func() {
		if !IsShutdown() {
			sig <- shutdownGracefullySignal
		}
	})
}

func WaitShutdown() {
	stopWhenKillDoneFuture.Await()
}

func ShutdownDone() concurrent.Future {
	return stopWhenKillDoneFuture
}

type PanicResult struct {
	Daemon Daemon
	Caught kkpanic.Caught
}

func init() {
	signal.Notify(sig, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		s := <-sig
		shutdown = true
		if !AutoStopWhenKill && s != shutdownGracefullySignal {
			stopWhenKillDoneFuture.Completable().Complete(s)
			return
		}

		msg := fmt.Sprintf("SIGNAL: %s, SHUTDOWN CATCH", s.String())
		kklogger.InfoJ("kkdaemon:AutoStopWhenKill", msg)
		Stop(s)
		kklogger.InfoJ("kkdaemon:AutoStopWhenKill", "Done")
		stopWhenKillDoneFuture.Completable().Complete(s)
	}()
}
