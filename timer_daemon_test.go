package kkdaemon

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTimerDaemon(t *testing.T) {
	daemon := &testTimerDaemon{}
	assert.Nil(t, RegisterDaemon(daemon))
	daemon2 := &testTimerDaemon2{}
	assert.Nil(t, RegisterDaemon(daemon2))
	Start()
	assert.Equal(t, 1, daemon.start)
	<-time.After(time.Second * 1)
	assert.True(t, daemon.loop > 70)
	assert.True(t, daemon2.loop > 15)
	Stop(syscall.SIGKILL)
	assert.Equal(t, "testTimerDaemon", daemon.Name())
	assert.Equal(t, "testTimerDaemon2", daemon2.Name())
	assert.Equal(t, 1, daemon.stop)
}

type testTimerDaemon struct {
	DefaultTimerDaemon
	start int
	loop  int
	stop  int
}

func (d *testTimerDaemon) Interval() time.Duration {
	return time.Millisecond * 10
}

func (d *testTimerDaemon) Start() {
	d.start = 1
}

func (d *testTimerDaemon) Loop() error {
	d.loop++
	return nil
}

func (d *testTimerDaemon) Stop(sig os.Signal) {
	d.stop = 1
}

type testTimerDaemon2 struct {
	DefaultTimerDaemon
	start int
	loop  int
	stop  int
}

func (d *testTimerDaemon2) Interval() time.Duration {
	return time.Millisecond * 50
}

func (d *testTimerDaemon2) Start() {
	d.start = 1
}

func (d *testTimerDaemon2) Loop() error {
	d.loop++
	return nil
}

func (d *testTimerDaemon2) Stop(sig os.Signal) {
	d.stop = 1
}
