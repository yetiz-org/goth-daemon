package kkdaemon

import (
	"fmt"
	"os"
	"time"

	kklogger "github.com/kklab-com/goth-kklogger"
	"github.com/kklab-com/goth-kkutil/concurrent"
	kkpanic "github.com/kklab-com/goth-panic"
)

type TimerDaemon interface {
	Daemon
	Interval() time.Duration
	Loop() error
	prepare()
	stopFuture() concurrent.Future
	loopStoppedFuture() concurrent.Future
}

type DefaultTimerDaemon struct {
	DefaultDaemon
	stopSig        concurrent.Future
	loopStoppedSig concurrent.Future
}

func (d *DefaultTimerDaemon) prepare() {
	if d.stopSig == nil || d.stopSig.IsDone() {
		d.stopSig = concurrent.NewFuture(nil)
	}

	if d.loopStoppedSig == nil || d.stopSig.IsDone() {
		d.loopStoppedSig = concurrent.NewFuture(nil)
	}
}

func (d *DefaultTimerDaemon) Interval() time.Duration {
	return time.Minute
}

func (d *DefaultTimerDaemon) Loop() error {
	return nil
}

func (d *DefaultTimerDaemon) stopFuture() concurrent.Future {
	return d.stopSig
}

func (d *DefaultTimerDaemon) loopStoppedFuture() concurrent.Future {
	return d.loopStoppedSig
}

func timerDaemonStart(daemon TimerDaemon) {
	daemon.prepare()
	daemon.Start()
	go func(daemon TimerDaemon) {
		timer := time.NewTimer(time.Nanosecond)
		for daemon.State() == StateRun {
			nextInterval := daemon.Interval()
			timer.Reset(truncateDuration(nextInterval))
			select {
			case <-daemon.stopFuture().Done():
				daemon.Stop(daemon.stopFuture().Get().(os.Signal))
				daemon.loopStoppedFuture().Completable().Complete(daemon.stopFuture().Get())
			case <-timer.C:
				kkpanic.LogCatch(func() {
					if err := daemon.Loop(); err != nil {
						kklogger.ErrorJ(fmt.Sprintf("TimerDaemon.Loop#%s", daemon.Name()), err.Error())
					}
				})

			case <-time.After(nextInterval * 5):
				continue
			}
		}
	}(daemon)
}

func timerDaemonStop(daemon TimerDaemon, sig os.Signal) {
	if daemon.stopFuture() != nil && !daemon.stopFuture().IsDone() {
		daemon.stopFuture().Completable().Complete(sig)
	}

	if daemon.loopStoppedFuture() != nil && !daemon.loopStoppedFuture().IsDone() {
		daemon.loopStoppedFuture().Await()
	}
}

func truncateDuration(interval time.Duration) time.Duration {
	return time.Now().Truncate(interval).Add(interval).Sub(time.Now())
}
