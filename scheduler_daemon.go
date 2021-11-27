package kkdaemon

type CronSyntax string

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
