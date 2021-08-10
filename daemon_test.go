package kkdaemon

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestService(t *testing.T) {
	assert.EqualValues(t, nil, RegisterService(&DefaultService{
		ServiceName:  "SS",
		ServiceOrder: 1,
	}))

	assert.EqualValues(t, 1, GetService("SS").Order())
	assert.NotNil(t, RegisterService(&DefaultService{
		ServiceName:  "SS",
		ServiceOrder: 2,
	}))

	RegisterService(&P1{Service: &DefaultService{
		ServiceName:  "P1",
		ServiceOrder: 1,
	}})

	RegisterService(&P2{Service: &DefaultService{
		ServiceName:  "P2",
		ServiceOrder: 2,
	}})

	RegisterServiceInline("P3", 3, func() {
		println("start p3")
	}, func(sig os.Signal) {
		println("stop p3")
	})

	Start()
	Stop(nil)
}

type P1 struct {
	Service
}

func (p *P1) Start() {
	println("start p1")
}

func (p *P1) Stop(sig os.Signal) {
	println("stop p1")
}

type P2 struct {
	Service
}

func (p *P2) Start() {
	println("start p2")
}

func (p *P2) Stop(sig os.Signal) {
	println("stop p2")
}
