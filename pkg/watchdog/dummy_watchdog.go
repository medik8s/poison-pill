package watchdog

import (
	"sync"
	"time"
)

const (
	timeout = 1 * time.Second
)

var _ Watchdog = &dummyWatchdog{}

type dummyWatchdog struct {
	stop         chan interface{}
	lastFoodTime time.Time
	once         sync.Once
}

func StartDummyWatchdog() Watchdog {
	stop := make(chan interface{})
	d := &dummyWatchdog{
		stop: stop,
		once: sync.Once{},
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
	d.once.Do(func() { d.stop <- true })
}

func (d *dummyWatchdog) LastFoodTime() time.Time {
	return d.lastFoodTime
}
