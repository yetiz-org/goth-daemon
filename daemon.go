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

var ServiceMap = sync.Map{}
var StopWhenKill = true
var sig = make(chan os.Signal, 1)

type Service interface {
	Start()
	Stop()
	Restart()
	Info() string
	Name() string
	Order() int
}

type DefaultService struct {
	name  string
	order int
}

func (s *DefaultService) Start() {

}

func (s *DefaultService) Stop() {

}

func (s *DefaultService) Restart() {

}

func (s *DefaultService) Name() string {
	return s.name
}

func (s *DefaultService) Order() int {
	return s.order
}

func (s *DefaultService) Info() string {
	bs, _ := json.Marshal(s)
	return string(bs)
}

func RegisterService(service Service) error {
	if service == nil {
		return fmt.Errorf("nil Service")
	}

	if service.Name() == "" {
		return fmt.Errorf("service.Name is empty")
	}

	if _, loaded := ServiceMap.LoadOrStore(service.Name(), service); loaded {
		return fmt.Errorf("service name is exist")
	}

	return nil
}

func GetService(name string) Service {
	if v, f := ServiceMap.Load(name); f {
		return v.(Service)
	}

	return nil
}

func Start() {
	var sl []Service
	ServiceMap.Range(func(key, value interface{}) bool {
		sl = append(sl, value.(Service))
		return true
	})

	sort.Slice(sl, func(i, j int) bool {
		return sl[i].Order() < sl[j].Order()
	})

	for _, service := range sl {
		var caught kkpanic.Caught
		kkpanic.Try(func() {
			service.Start()
			kklogger.InfoJ("daemon.Start", fmt.Sprintf("service %s started", service.Name()))
		}).CatchAll(func(caught kkpanic.Caught) {
			kklogger.ErrorJ("daemon.Start", fmt.Sprintf("Service %s fail, message: %s", service.Name(), caught.String()))
		})

		if caught != nil {
			panic(&PanicResult{
				Service: service,
				Caught:  caught,
			})
		}
	}
}

func Stop() {
	var sl []Service
	ServiceMap.Range(func(key, value interface{}) bool {
		sl = append(sl, value.(Service))
		return true
	})

	sort.Slice(sl, func(i, j int) bool {
		return sl[i].Order() > sl[j].Order()
	})

	for _, service := range sl {
		var caught kkpanic.Caught
		kkpanic.Try(func() {
			service.Stop()
			kklogger.InfoJ("daemon.Stop", fmt.Sprintf("service %s stopped", service.Name()))
		}).CatchAll(func(caught kkpanic.Caught) {
			kklogger.ErrorJ("daemon.Stop", fmt.Sprintf("Service %s fail, message: %s", service.Name(), caught.String()))
		})

		if caught != nil {
			panic(&PanicResult{
				Service: service,
				Caught:  caught,
			})
		}
	}
}

type PanicResult struct {
	Service Service
	Caught  kkpanic.Caught
}

func init() {
	signal.Notify(sig, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		s := <-sig
		if !StopWhenKill {
			return
		}
		
		msg := fmt.Sprintf("SIGNAL: %s, SHUTDOWN CATCH", s.String())
		kklogger.InfoJ("kkdaemon:init._StopWhenKillOnce", msg)
		Stop()
		kklogger.InfoJ("kkdaemon:init._StopWhenKillOnce", "Done")
	}()
}
