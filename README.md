# goth-daemon

daemon process management with timer job support and stop all when kill signal raised.

## HOWTO

### Basic Usage

```go
// register daemon (auto-assigned order)
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

### Advanced Usage - Daemon Startup Order

When you need to control the startup and shutdown order of daemons (e.g., logger must start first, HTTP server must start last), use `RegisterDaemonWithOrder`:

```go
// Register daemons with specific order
// Lower order values start earlier, higher order values stop earlier
kkdaemon.RegisterDaemonWithOrder(daemons.DaemonLogger, 1)      // Start first
kkdaemon.RegisterDaemonWithOrder(daemons.DaemonDatabase, 10)   // Start second
kkdaemon.RegisterDaemonWithOrder(daemons.DaemonCache, 20)      // Start third
kkdaemon.RegisterDaemonWithOrder(daemons.DaemonHTTPServer, 100) // Start last

// Start service
kkdaemon.Start()

// On shutdown, the order is reversed:
// HTTPServer stops first, then Cache, then Database, then Logger stops last
kkdaemon.ShutdownGracefully()
```

**Important Notes:**
- Each order value must be unique. Registering two daemons with the same order will cause a panic.
- You can mix `RegisterDaemon()` (auto-order) and `RegisterDaemonWithOrder()` (manual order) in the same service.
- Auto-assigned orders start from 1 and increment sequentially.
- Negative and zero order values are supported.

**Example - Mixed Auto and Manual Order:**
```go
kkdaemon.RegisterDaemonWithOrder(logger, 1)  // Manual: order 1
kkdaemon.RegisterDaemon(worker1)             // Auto: order 2
kkdaemon.RegisterDaemon(worker2)             // Auto: order 3
kkdaemon.RegisterDaemonWithOrder(server, 100) // Manual: order 100

// Startup order: logger(1) -> worker1(2) -> worker2(3) -> server(100)
// Shutdown order: server(100) -> worker2(3) -> worker1(2) -> logger(1)
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

## Cancellable Startup - ContextStarter

`Start()` cannot be interrupted. During service-wide shutdown, a blocking legacy start
is not stopped concurrently and does not delay the other daemons; its cleanup is deferred
until `Start` returns successfully. A daemon that needs cooperative cancellation during
startup can implement the optional `ContextStarter` interface in addition to `Daemon`:

```go
type ContextStarter interface {
    StartContext(ctx context.Context) error
}
```

When a daemon implements it, the service calls `StartContext` instead of `Start` and owns
the context of that single start invocation.

```go
type ServerDaemonExample struct {
    kkdaemon.DefaultDaemon
    listener net.Listener
}

func (d *ServerDaemonExample) StartContext(ctx context.Context) error {
    // Perform blocking startup work that honors ctx.Done().
    listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", ":8080")
    if err != nil {
        return err
    }

    d.listener = listener
    return nil
}

func (d *ServerDaemonExample) Stop(sig os.Signal) {
    d.listener.Close()
}
```

**Contract:**
- The context is cancelled when the service stops the daemon, and `Stop` waits for
  `StartContext` to return. It carries cancellation only and is never used to pass state.
- Returning `nil` means startup succeeded, even when the context was already cancelled.
  The service then still calls `Stop` exactly once.
- Returning an error (including `ctx.Err()`) or panicking means startup failed, so `Stop`
  is not called. Existing failed-start state semantics remain unchanged.
- `Start` and `StartContext` are never both called for the same start. Existing daemons
  that only implement `Daemon` require no source changes and continue to use `Start`.
- A daemon that only implements `Daemon` cannot be cancelled. Service-wide `Stop` does not
  wait for a blocking `Start` and calls `Stop` once after `Start` returns. Direct
  `StopDaemon` and `UnregisterDaemon` wait for that deferred cleanup before returning.
