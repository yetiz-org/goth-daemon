package kkdaemon

import (
	"time"
)

type TimerDaemon interface {
	Daemon
	Interval() time.Duration
	Loop() error
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
