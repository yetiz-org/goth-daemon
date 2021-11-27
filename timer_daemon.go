package kkdaemon

import (
	"time"
)

type Looper interface {
	Loop() error
}

type TimerDaemon interface {
	Daemon
	Looper
	Interval() time.Duration
}

type DefaultTimerDaemon struct {
	DefaultDaemon
}

func (d *DefaultTimerDaemon) Interval() time.Duration {
	return time.Minute
}

func (d *DefaultTimerDaemon) Loop() error {
	return nil
}
