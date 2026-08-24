package kkdaemon

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	kkpanic "github.com/yetiz-org/goth-panic"
)

// ContextStarter is an optional startup contract a Daemon may implement in
// addition to Daemon.Start. When a daemon implements it, DaemonService calls
// StartContext instead of Start and owns the context of that single start
// invocation: the context is cancelled when the service stops the daemon, and a
// daemon that keeps background work alive past startup may retain it.
//
// A nil return means startup succeeded even when the context was already
// cancelled, so the service still owes the daemon exactly one Stop. A non-nil
// return or a panic means startup failed, so Stop is not called.
//
// The context carries cancellation only. It is never used to pass state.
type ContextStarter interface {
	StartContext(ctx context.Context) (rtErr error)
}

type daemonStartMode uint8

const (
	daemonStartModeLegacy daemonStartMode = iota
	daemonStartModeContext
)

// daemonLifecyclePhase is the private owner of "who may stop this daemon".
// The exported StateWait/StateStart/StateRun/StateStop values keep their
// previous meaning; the phase only records which side of a race owns the next
// transition, which an int32 state alone cannot express.
type daemonLifecyclePhase uint8

const (
	daemonLifecycleIdle daemonLifecyclePhase = iota
	daemonLifecycleStarting
	daemonLifecycleStarted
	daemonLifecycleStopping
)

type daemonStartOutcome uint8

const (
	daemonStartOutcomeFailed daemonStartOutcome = iota
	daemonStartOutcomeStarted
	daemonStartOutcomeLateStop
)

type daemonStopAction uint8

const (
	daemonStopNone daemonStopAction = iota
	daemonStopDeferred
	daemonStopWait
	daemonStopInvoke
)

// daemonStartCycle is one start invocation. Stop claims a cycle instead of the
// entity, so a start that finishes late can tell whether the stop request it
// observes belongs to its own invocation or to a later one.
type daemonStartCycle struct {
	mode          daemonStartMode
	startCtx      context.Context
	cancel        context.CancelFunc
	done          chan struct{}
	doneOnce      sync.Once
	stopRequested bool
	stopSignal    os.Signal
	stopCaught    kkpanic.Caught
}

func (c *daemonStartCycle) closeDone() {
	c.doneOnce.Do(func() {
		close(c.done)
	})
}

type daemonLifecycle struct {
	mutex sync.Mutex
	phase daemonLifecyclePhase
	cycle *daemonStartCycle
}

type daemonStopClaim struct {
	action daemonStopAction
	cycle  *daemonStartCycle
}

// beginStart registers a start cycle and hands the daemon to the caller. The
// returned starter is non-nil only when the daemon opted into ContextStarter.
func (e *DaemonEntity) beginStart() (cycle *daemonStartCycle, starter ContextStarter, rtErr error) {
	e.lifecycle.mutex.Lock()
	defer e.lifecycle.mutex.Unlock()

	if e.lifecycle.phase != daemonLifecycleIdle ||
		!atomic.CompareAndSwapInt32(e.Daemon._State(), StateWait, StateStart) {
		return nil, nil, fmt.Errorf("%s not in WAIT state", e.Daemon.Name())
	}

	cycle = &daemonStartCycle{done: make(chan struct{})}
	if contextStarter, ok := e.Daemon.(ContextStarter); ok {
		cycle.mode = daemonStartModeContext
		cycle.startCtx, cycle.cancel = context.WithCancel(context.Background())
		starter = contextStarter
	}

	e.lifecycle.phase = daemonLifecycleStarting
	e.lifecycle.cycle = cycle
	atomic.StoreInt32(e.Daemon._State(), StateRun)
	return cycle, starter, nil
}

// finishStart closes the start half of cycle and reports what the caller still
// owes the daemon. daemonStartOutcomeLateStop means Stop won the race while the
// daemon was starting and the start path now owns the one and only Stop call.
func (e *DaemonEntity) finishStart(cycle *daemonStartCycle, succeeded bool) (outcome daemonStartOutcome, sig os.Signal) {
	e.lifecycle.mutex.Lock()
	defer e.lifecycle.mutex.Unlock()

	if e.lifecycle.cycle != cycle || e.lifecycle.phase != daemonLifecycleStarting {
		return daemonStartOutcomeFailed, nil
	}

	if cycle.stopRequested && succeeded {
		e.lifecycle.phase = daemonLifecycleStopping
		atomic.CompareAndSwapInt32(e.Daemon._State(), StateRun, StateStop)
		return daemonStartOutcomeLateStop, cycle.stopSignal
	}

	if succeeded {
		atomic.StoreInt32(&e.started, 1)
		atomic.CompareAndSwapInt32(e.Daemon._State(), StateRun, StateStart)
		e.lifecycle.phase = daemonLifecycleStarted
		cycle.closeDone()
		return daemonStartOutcomeStarted, nil
	}

	// The service owns the startup context and must release it on every failed
	// path, including an ordinary error or panic that occurred before Stop.
	if cycle.cancel != nil {
		cycle.cancel()
	}

	// A failed start keeps the historic resting state: StateStart when nobody
	// asked for a stop, StateWait once a stop request made the daemon unusable.
	restingState := StateStart
	if cycle.stopRequested {
		restingState = StateWait
	}

	atomic.StoreInt32(&e.started, 0)
	atomic.CompareAndSwapInt32(e.Daemon._State(), StateRun, restingState)
	e.lifecycle.phase = daemonLifecycleIdle
	e.lifecycle.cycle = nil
	cycle.closeDone()
	return daemonStartOutcomeFailed, nil
}

// claimStop decides who runs Stop for this daemon and never runs it twice.
// allowIdle is set by the direct StopDaemon entry point, which must keep honouring
// the raw daemon state; the service shutdown scan leaves idle daemons untouched so
// a daemon that never started successfully is never stopped.
func (e *DaemonEntity) claimStop(sig os.Signal, allowIdle bool) (claim daemonStopClaim, rtErr error) {
	e.lifecycle.mutex.Lock()
	defer e.lifecycle.mutex.Unlock()

	switch e.lifecycle.phase {
	case daemonLifecycleStarting:
		cycle := e.lifecycle.cycle
		if cycle.stopRequested {
			// A direct caller joins an existing service-wide shutdown so it cannot
			// report success or release the daemon name before deferred cleanup.
			if allowIdle {
				return daemonStopClaim{action: daemonStopWait, cycle: cycle}, nil
			}
			return daemonStopClaim{action: daemonStopNone}, nil
		}

		cycle.stopRequested = true
		cycle.stopSignal = sig
		if cycle.mode == daemonStartModeContext {
			cycle.cancel()
			return daemonStopClaim{action: daemonStopWait, cycle: cycle}, nil
		}

		// A legacy Start cannot be cancelled and must not be called concurrently
		// with Stop, so the start path is left owning the deferred Stop.
		return daemonStopClaim{action: daemonStopDeferred, cycle: cycle}, nil
	case daemonLifecycleStarted:
		if !atomic.CompareAndSwapInt32(e.Daemon._State(), StateStart, StateStop) &&
			!atomic.CompareAndSwapInt32(e.Daemon._State(), StateRun, StateStop) {
			return daemonStopClaim{}, fmt.Errorf("%s not in START/RUN state", e.Daemon.Name())
		}

		// A ContextStarter may retain its context for background work. Cancel it
		// before Stop so cleanup can wait for that work without deadlocking.
		if e.lifecycle.cycle.cancel != nil {
			e.lifecycle.cycle.cancel()
		}
		e.lifecycle.phase = daemonLifecycleStopping
		return daemonStopClaim{action: daemonStopInvoke, cycle: e.lifecycle.cycle}, nil
	case daemonLifecycleStopping:
		if !allowIdle {
			return daemonStopClaim{action: daemonStopNone}, nil
		}

		return daemonStopClaim{}, fmt.Errorf("%s not in START/RUN state", e.Daemon.Name())
	default:
		if !allowIdle {
			return daemonStopClaim{action: daemonStopNone}, nil
		}

		if !atomic.CompareAndSwapInt32(e.Daemon._State(), StateStart, StateStop) &&
			!atomic.CompareAndSwapInt32(e.Daemon._State(), StateRun, StateStop) {
			return daemonStopClaim{}, fmt.Errorf("%s not in START/RUN state", e.Daemon.Name())
		}

		cycle := &daemonStartCycle{done: make(chan struct{})}
		e.lifecycle.phase = daemonLifecycleStopping
		e.lifecycle.cycle = cycle
		return daemonStopClaim{action: daemonStopInvoke, cycle: cycle}, nil
	}
}

// completeStop releases the daemon after its Stop returned. Cancelling here also
// ends the startup context of a ContextStarter that outlived its own startup.
func (e *DaemonEntity) completeStop(cycle *daemonStartCycle, caught kkpanic.Caught) {
	e.lifecycle.mutex.Lock()
	defer e.lifecycle.mutex.Unlock()

	if e.lifecycle.cycle != cycle || e.lifecycle.phase != daemonLifecycleStopping {
		return
	}

	cycle.stopCaught = caught
	if cycle.cancel != nil {
		cycle.cancel()
	}

	atomic.StoreInt32(&e.started, 0)
	atomic.StoreInt32(e.Daemon._State(), StateWait)
	e.lifecycle.phase = daemonLifecycleIdle
	e.lifecycle.cycle = nil
	cycle.closeDone()
}

// beginLoop admits one Looper invocation. Binding it to the live start cycle is
// what stops a loop that finishes after shutdown from resurrecting StateStart.
func (e *DaemonEntity) beginLoop() (cycle *daemonStartCycle, admitted bool) {
	e.lifecycle.mutex.Lock()
	defer e.lifecycle.mutex.Unlock()

	if e.lifecycle.phase != daemonLifecycleStarted ||
		!atomic.CompareAndSwapInt32(e.Daemon._State(), StateStart, StateRun) {
		return nil, false
	}

	return e.lifecycle.cycle, true
}

func (e *DaemonEntity) finishLoop(cycle *daemonStartCycle) {
	e.lifecycle.mutex.Lock()
	defer e.lifecycle.mutex.Unlock()

	if e.lifecycle.cycle != cycle || e.lifecycle.phase != daemonLifecycleStarted {
		return
	}

	atomic.CompareAndSwapInt32(e.Daemon._State(), StateRun, StateStart)
}
