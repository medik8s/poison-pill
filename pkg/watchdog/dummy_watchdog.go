package watchdog

import (
	"time"
)

const (
	timeout = time.Second
)

var _ Watchdog = &dummyWatchdog{}

type dummyWatchdog struct {
	stop         chan interface{}
	lastFoodTime time.Time
}

func StartDummyWatchdog() Watchdog {
	stop := make(chan interface{})
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
	d.stop <- true
}

func (d *dummyWatchdog) LastFoodTime() time.Time {
	return d.lastFoodTime
}
