package kkdaemon

import (
	"time"
)

type Interval time.Duration

func (c Interval) Next(from time.Time) time.Time {
	return from.Truncate(time.Duration(c)).Add(time.Duration(c))
}

type Looper interface {
	Loop() error
}

type TimerDaemon interface {
	Daemon
	Looper
	Interval() Interval
}

type DefaultTimerDaemon struct {
	DefaultDaemon
}

func (d *DefaultTimerDaemon) Interval() Interval {
	return Interval(time.Minute)
}

func (d *DefaultTimerDaemon) Loop() error {
	return nil
}
