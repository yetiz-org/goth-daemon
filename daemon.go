package kkdaemon

import (
	"os"
	"syscall"
	"time"

	"github.com/kklab-com/goth-kkutil/concurrent"
	kkpanic "github.com/kklab-com/goth-panic"
)

var DefaultService = NewDaemonService()

const StateWait = int32(0)
const StateStart = int32(1)
const StateRun = int32(2)
const StateStop = int32(3)
const shutdownGracefullySignal = syscall.Signal(0xff)
const unregisterSignal = syscall.Signal(0xfe)

var _MaxTime = time.Unix(1<<63-62135596801, 999999999)

type DaemonEntity struct {
	Name   string
	Daemon Daemon
	Order  int
	Next   time.Time
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

type _SimpleDaemon struct {
	DefaultDaemon
	StartFunc func()
	StopFunc  func(sig os.Signal)
}

func (s *_SimpleDaemon) Name() string {
	return s.name
}

func (s *_SimpleDaemon) Start() {
	if s.StartFunc != nil {
		s.StartFunc()
	}
}

func (s *_SimpleDaemon) Stop(sig os.Signal) {
	if s.StopFunc != nil {
		s.StopFunc(sig)
	}
}

func RegisterDaemon(daemon Daemon) error {
	return DefaultService.RegisterDaemon(daemon)
}

func RegisterSimpleDaemon(name string, startFunc func(), stopFunc func(sig os.Signal)) error {
	return RegisterDaemon(&_SimpleDaemon{
		DefaultDaemon: DefaultDaemon{
			name: name,
		},
		StartFunc: startFunc,
		StopFunc:  stopFunc,
	})
}

func GetDaemon(name string) *DaemonEntity {
	return DefaultService.GetDaemon(name)
}

func UnregisterDaemon(name string) error {
	return DefaultService.UnregisterDaemon(name)
}

func Start() {
	DefaultService.Start()
}

func Stop(sig os.Signal) {
	DefaultService.Stop(sig)
}

func IsShutdown() bool {
	return DefaultService.IsShutdown()
}

func ShutdownGracefully() {
	DefaultService.ShutdownGracefully()
}

func ShutdownFuture() concurrent.Future {
	return DefaultService.ShutdownFuture()
}

type PanicResult struct {
	Daemon Daemon
	Caught kkpanic.Caught
}
