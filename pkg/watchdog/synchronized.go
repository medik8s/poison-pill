package watchdog

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/wait"
)

var _ Watchdog = &synchronizedWatchdog{}

// synchronizedWatchdog implements the Watchdog interface with synchronized calls of the implementation specific methods
type synchronizedWatchdog struct {
	impl         watchdogImpl
	timeout      time.Duration
	isStarted    bool
	stop         context.CancelFunc
	isStopped    bool
	mutex        sync.Mutex
	lastFoodTime time.Time
	log          logr.Logger
}

func newSynced(log logr.Logger, impl watchdogImpl) *synchronizedWatchdog {
	return &synchronizedWatchdog{
		impl: impl,
		log:  log,
	}
}

func (swd *synchronizedWatchdog) Start(ctx context.Context) error {
	swd.mutex.Lock()
	defer swd.mutex.Unlock()
	if swd.isStarted {
		return errors.New("watchdog was isStarted more than once. This is likely to be caused by being added to a manager multiple times")
	}
	timeout, err := swd.impl.start()
	if err != nil {
		// TODO or return the error and fail the pod's start?
		return nil
	}
	swd.timeout = *timeout
	swd.isStarted = true
	swd.log.Info("watchdog isStarted")
	swd.mutex.Unlock()

	feedCtx, cancel := context.WithCancel(context.Background())
	swd.stop = cancel
	// feed until isStopped
	go wait.NonSlidingUntilWithContext(feedCtx, func(feedCtx context.Context) {
		swd.mutex.Lock()
		defer swd.mutex.Unlock()
		// this should not happen because the context is cancelled already.. but just in case
		if swd.isStopped {
			return
		}
		if err := swd.impl.feed(); err != nil {
			swd.log.Error(err, "failed to feed watchdog!")
		} else {
			swd.lastFoodTime = time.Now()
		}
	}, swd.timeout/3)

	<-ctx.Done()

	// pod is being isStopped, disarm!
	swd.mutex.Lock()
	if swd.isStarted && !swd.isStopped {
		if err := swd.impl.disarm(); err != nil {
			swd.log.Error(err, "failed to disarm watchdog!")
		} else {
			swd.log.Info("disarmed watchdog")
			// we can stop feeding after disarm
			swd.stop()
			swd.isStopped = true
		}
	}

	return nil
}

func (swd *synchronizedWatchdog) IsStarted() bool {
	swd.mutex.Lock()
	defer swd.mutex.Unlock()
	return swd.isStarted
}

func (swd *synchronizedWatchdog) Stop() {
	swd.mutex.Lock()
	defer swd.mutex.Unlock()
	if !swd.isStarted || swd.isStopped {
		return
	}
	if swd.isStarted {
		swd.stop()
		swd.isStopped = true
	}
}

func (swd *synchronizedWatchdog) GetTimeout() time.Duration {
	swd.mutex.Lock()
	defer swd.mutex.Unlock()
	return swd.timeout
}

func (swd *synchronizedWatchdog) LastFoodTime() time.Time {
	swd.mutex.Lock()
	defer swd.mutex.Unlock()
	return swd.lastFoodTime
}
