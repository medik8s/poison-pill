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
	stop         chan struct{}
	once         sync.Once
	mutex        sync.Mutex
	lastFoodTime time.Time
}

func StartDummyWatchdog() Watchdog {
	stop := make(chan struct{})
	d := &dummyWatchdog{
		stop:  stop,
		once:  sync.Once{},
		mutex: sync.Mutex{},
	}

	// feed until stopped
	ticker := time.NewTicker(timeout / 3)
	go func() {
		for {
			select {
			case <-stop:
				ticker.Stop()
				return
			case <-ticker.C:
				d.mutex.Lock()
				d.lastFoodTime = time.Now()
				d.mutex.Unlock()
			}
		}
	}()
	return d
}

func (d *dummyWatchdog) Stop() {
	d.once.Do(func() { close(d.stop) })
}

func (d *dummyWatchdog) LastFoodTime() time.Time {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.lastFoodTime
}
