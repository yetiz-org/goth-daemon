package kkdaemon

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

type CronSyntax string

var parser = cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func (c CronSyntax) Next(from time.Time) time.Time {
	if schedule, err := parser.Parse(string(c)); err != nil {
		panic(fmt.Sprintf("%s can't be parsed.", c))
	} else {
		return schedule.Next(from)
	}
}

type SchedulerDaemon interface {
	Daemon
	Looper
	When() CronSyntax
}

type DefaultSchedulerDaemon struct {
	DefaultDaemon
}

func (d *DefaultSchedulerDaemon) When() CronSyntax {
	return "* * * * *"
}

func (d *DefaultSchedulerDaemon) Loop() error {
	return nil
}
