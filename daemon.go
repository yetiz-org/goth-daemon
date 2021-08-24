package kkdaemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
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

type DaemonEntity struct {
	Daemon Daemon
	Name   string
	Order  int
}

type Daemon interface {
	Start()
	Stop(sig os.Signal)
	Restart()
	Info() string
}

type DefaultDaemon struct {
}

func (s *DefaultDaemon) Start() {

}

func (s *DefaultDaemon) Stop(sig os.Signal) {

}

func (s *DefaultDaemon) Restart() {

}

func (s *DefaultDaemon) Info() string {
	bs, _ := json.Marshal(s)
	return string(bs)
}

func RegisterDaemon(name string, order int, daemon Daemon) error {
	if daemon == nil {
		return fmt.Errorf("nil daemon")
	}

	if name == "" {
		return fmt.Errorf("name is empty")
	}

	if _, loaded := DaemonMap.LoadOrStore(name, &DaemonEntity{Name: name, Order: order, Daemon: daemon}); loaded {
		return fmt.Errorf("name is exist")
	}

	return nil
}

type _InlineService struct {
	DefaultDaemon
	StartFunc func()
	StopFunc  func(sig os.Signal)
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
	return RegisterDaemon(name, order, &_InlineService{
		DefaultDaemon: DefaultDaemon{},
		StartFunc:     startFunc,
		StopFunc:      stopFunc,
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
			kklogger.InfoJ("daemon.Start", fmt.Sprintf("entity %s started", entity.Name))
		}).CatchAll(func(caught kkpanic.Caught) {
			kklogger.ErrorJ("daemon.Start", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, caught.String()))
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
			kklogger.InfoJ("daemon.Stop", fmt.Sprintf("entity %s stopped", entity.Name))
		}).CatchAll(func(caught kkpanic.Caught) {
			kklogger.ErrorJ("daemon.Stop", fmt.Sprintf("Daemon %s fail, message: %s", entity.Name, caught.String()))
		})

		if caught != nil {
			panic(&PanicResult{
				Daemon: entity.Daemon,
				Caught: caught,
			})
		}
	}
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
