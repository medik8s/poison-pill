package watchdog

import (
	"time"
)

const (
	timeout = 1 * time.Second
)

var _ Watchdog = &dummyWatchdog{}

type dummyWatchdog struct {
	stop         chan struct{}
	lastFoodTime time.Time
}

func StartDummyWatchdog() Watchdog {
	stop := make(chan struct{})
	d := &dummyWatchdog{
		stop: stop,
	}

	// feed until stopped
	ticker := time.NewTicker(timeout / 3)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				d.lastFoodTime = time.Now()
			}
		}
	}()
	return d
}

func (d *dummyWatchdog) Stop() {
	select {
	case d.stop <- struct{}{}:
	default: // no-op, already stopped
	}
}

func (d *dummyWatchdog) LastFoodTime() time.Time {
	return d.lastFoodTime
}
