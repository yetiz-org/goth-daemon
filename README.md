# goth-daemon

daemon process management with timer job support and stop all when kill signal raised.

## HOWTO

```go
// register daemon
kkdaemon.RegisterDaemon(daemons.DaemonSetupEnvironment)
kkdaemon.RegisterDaemon(daemons.DaemonSetupLogger)

// start daemon service
kkdaemon.Start()

// wait process killed
kkdaemon.ShutdownFuture().Await()

// or trigger `GracefullyShutdown` manually
go func(){
    <-time.After(time.Minute)
    kkdaemon.ShutdownGracefully()
}()

```

## Define Daemon

### DefaultDaemon

use default daemon to execute function when daemon service `Start` and `Stop`

```go
type DefaultDaemonExample struct {
kkdaemon.DefaultTimerDaemon
}

func (d *DefaultDaemonExample) Registered() error {
// init func
return nil
}

func (d *DefaultDaemonExample) Start() {
// do when start
}

func (d *DefaultDaemonExample) Stop(sig os.Signal) {
// do when stop
}

```

### TimerDaemon

timer daemon provide `Loop` func to be execute every `Interval` duration

```go
type TimerDaemonExample struct {
kkdaemon.DefaultTimerDaemon
}

func (d *TimerDaemonExample) Registered() error {
// init func
return nil
}

func (d *TimerDaemonExample) Start() {
// do when start
}

func (d *TimerDaemonExample) Interval() time.Duration {
// run every minute
return time.Minute
}

func (d *TimerDaemonExample) Loop() error {
// do something
return nil
}

func (d *TimerDaemonExample) Stop(sig os.Signal) {
// do when stop
}

```

### SchedulerDaemon

scheduler daemon use cron syntax to manage daemon `Loop` when be executed.

```go
type SchedulerDaemonExample struct {
kkdaemon.DefaultSchedulerDaemon
}

func (d *SchedulerDaemonExample) Registered() error {
// init func
return nil
}

func (d *SchedulerDaemonExample) Start() {
// do when start
}

func (d *SchedulerDaemonExample) When() kkdaemon.CronSyntax {
// run every two minute
return "*/2 * * * *"
}

func (d *SchedulerDaemonExample) Loop() error {
// do something
return nil
}

func (d *SchedulerDaemonExample) Stop(sig os.Signal) {
// do when stop
}

```