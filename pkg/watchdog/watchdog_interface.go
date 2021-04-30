package watchdog

import "time"

type Watchdog interface {
	Stop()
	LastFoodTime() time.Time
}
