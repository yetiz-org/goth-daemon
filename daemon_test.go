package kkdaemon

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestService(t *testing.T) {
	assert.EqualValues(t, nil, RegisterDaemon(&DefaultDaemon{name: "SS"}))
	assert.EqualValues(t, 1, GetDaemon("SS").Order)
	assert.NotNil(t, RegisterDaemon(&DefaultDaemon{name: "SS"}))
	assert.Nil(t, RegisterDaemon(&P1{Daemon: &DefaultDaemon{}}))
	p2 := &P2{Daemon: &DefaultDaemon{}}
	assert.Nil(t, RegisterDaemon(p2))
	RegisterServiceInline("P3", func() {
		println("start p3")
	}, func(sig os.Signal) {
		println("stop p3")
	})

	Start()
	assert.Nil(t, UnregisterDaemon("P2"))
	assert.Equal(t, 1, p2.s)
	Stop(syscall.SIGKILL)
	assert.Equal(t, 1, p2.s)
}

type P1 struct {
	Daemon
}

func (p *P1) Start() {
	println("start p1")
}

func (p *P1) Stop(sig os.Signal) {
	println("stop p1")
}

type P2 struct {
	Daemon
	s int
}

func (p *P2) Start() {
	println("start p2")
}

func (p *P2) Stop(sig os.Signal) {
	p.s++
	println("stop p2")
}
