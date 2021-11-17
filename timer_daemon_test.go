package kkdaemon

import (
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTimerDaemon(t *testing.T) {
	DaemonMap = sync.Map{}
	daemon := &testTimerDaemon{}
	assert.Nil(t, RegisterDaemon(1, daemon))
	Start()
	assert.Equal(t, 1, daemon.start)
	<-time.After(time.Second)
	assert.True(t, daemon.loop > 10)
	Stop(syscall.SIGKILL)
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
