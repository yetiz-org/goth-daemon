package kkdaemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"sync"
	"syscall"

	kklogger "github.com/kklab-com/goth-kklogger"
	kkpanic "github.com/kklab-com/goth-panic"
)

var DaemonMap = sync.Map{}
var StopWhenKill = true
var sig = make(chan os.Signal, 1)
var StopWhenKillDone = make(chan int)
var shutdown = false

type DaemonEntity struct {
	Daemon Daemon
	Order  int
}

type Daemon interface {
	Registered()
	Start()
	Stop(sig os.Signal)
	Restart()
	Name() string
	Info() string
}

type DefaultDaemon struct {
	name string
}

func (d *DefaultDaemon) Registered() {

}

func (d *DefaultDaemon) Start() {

}

func (d *DefaultDaemon) Stop(sig os.Signal) {

}

func (d *DefaultDaemon) Restart() {

}

func (d *DefaultDaemon) Name() string {
	return d.name
}

func (d *DefaultDaemon) Info() string {
	bs, _ := json.Marshal(d)
	return string(bs)
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

	if _, loaded := DaemonMap.LoadOrStore(name, &DaemonEntity{Order: order, Daemon: daemon}); loaded {
		return fmt.Errorf("name is exist")
	}

	daemon.Registered()
	return nil
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
		var caught kkpanic.Caught
		kkpanic.Try(func() {
			entity.Daemon.Start()
			kklogger.InfoJ("daemon.Start", fmt.Sprintf("entity %s started", entity.Daemon.Name()))
		}).CatchAll(func(caught kkpanic.Caught) {
			kklogger.ErrorJ("daemon.Start", fmt.Sprintf("Daemon %s fail, message: %s", entity.Daemon.Name(), caught.String()))
		})

		if caught != nil {
			panic(&PanicResult{
				Daemon: entity.Daemon,
				Caught: caught,
			})
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
		var caught kkpanic.Caught
		kkpanic.Try(func() {
			entity.Daemon.Stop(sig)
			kklogger.InfoJ("daemon.Stop", fmt.Sprintf("entity %s stopped", entity.Daemon.Name()))
		}).CatchAll(func(caught kkpanic.Caught) {
			kklogger.ErrorJ("daemon.Stop", fmt.Sprintf("Daemon %s fail, message: %s", entity.Daemon.Name(), caught.String()))
		})

		if caught != nil {
			panic(&PanicResult{
				Daemon: entity.Daemon,
				Caught: caught,
			})
		}
	}
}

func IsShutdown() bool {
	return shutdown
}

func Shutdown(signal os.Signal) {
	sig <- signal
}

type PanicResult struct {
	Daemon Daemon
	Caught kkpanic.Caught
}

func init() {
	signal.Notify(sig, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		shutdown = true
		s := <-sig
		if !StopWhenKill {
			close(StopWhenKillDone)
			return
		}

		msg := fmt.Sprintf("SIGNAL: %s, SHUTDOWN CATCH", s.String())
		kklogger.InfoJ("kkdaemon:init._StopWhenKillOnce", msg)
		Stop(s)
		kklogger.InfoJ("kkdaemon:init._StopWhenKillOnce", "Done")
		close(StopWhenKillDone)
	}()
}
