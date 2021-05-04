package watchdog

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-logr/logr"
	. "golang.org/x/sys/unix"
)

const (
	watchdogDevice = "/dev/watchdog1"
)

var _ Watchdog = &linuxWatchdog{}

type linuxWatchdog struct {
	fd           int
	info         *watchdogInfo
	stop         chan struct{}
	once         sync.Once
	mutex        sync.Mutex
	lastFoodTime time.Time
	log          logr.Logger
}

type watchdogInfo struct {
	options         uint32
	firmwareVersion uint32
	identity        [32]byte
}

func StartWatchdog(log logr.Logger) (Watchdog, error) {

	if _, err := os.Stat(watchdogDevice); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("watchdog device not found: %v", err)
		}
		return nil, fmt.Errorf("failed to check for watchdog device: %v", err)
	}

	wdFd, err := openDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to open LinuxWatchdog device %s: %v", watchdogDevice, err)
	}

	stop := make(chan struct{})
	wd := &linuxWatchdog{
		fd:    wdFd,
		info:  getInfo(wdFd),
		stop:  stop,
		once:  sync.Once{},
		mutex: sync.Mutex{},
		log:   log,
	}

	wdTimeout, err := wd.getTimeout()
	if err != nil {
		_ = wd.disarm()
		return nil, fmt.Errorf("failed to get timeout of watchdog, disarmed: %v", err)
	}

	// feed until stopped
	ticker := time.NewTicker(*wdTimeout / 3)
	go func() {
		for {
			select {
			case <-stop:
				ticker.Stop()
				return
			case <-ticker.C:
				if err := wd.feed(); err != nil {
					log.Error(err, "failed to feed watchdog!")
				} else {
					wd.mutex.Lock()
					wd.lastFoodTime = time.Now()
					wd.mutex.Unlock()
				}
			}
		}
	}()

	return wd, nil
}

func (wd *linuxWatchdog) Stop() {
	wd.once.Do(func() { close(wd.stop) })
}

func (wd *linuxWatchdog) LastFoodTime() time.Time {
	wd.mutex.Lock()
	defer wd.mutex.Unlock()
	return wd.lastFoodTime
}

func (wd *linuxWatchdog) getTimeout() (*time.Duration, error) {
	timeout, err := IoctlGetInt(wd.fd, WDIOC_GETTIMEOUT)

	if err != nil {
		return nil, err
	}

	timeoutDuration := time.Duration(timeout) * time.Second
	return &timeoutDuration, nil
}

func (wd *linuxWatchdog) feed() error {
	food := []byte("a")
	_, err := Write(wd.fd, food)

	return err
}

//Disarm closes the LinuxWatchdog without triggering reboots, even if the LinuxWatchdog will not be fed any more
func (wd *linuxWatchdog) disarm() error {
	b := []byte("V") // "V" is a special char for signaling LinuxWatchdog disarm
	_, err := Write(wd.fd, b)

	if err != nil {
		return err
	}

	return Close(wd.fd)
}

func getInfo(fd int) *watchdogInfo {
	info := watchdogInfo{}

	_, _, errNo := syscall.Syscall(
		syscall.SYS_IOCTL, uintptr(fd),
		WDIOC_GETSUPPORT, uintptr(unsafe.Pointer(&info)))

	if errNo != 0 {
		return nil
	}

	return &info
}

func openDevice() (int, error) {
	return Open(watchdogDevice, O_WRONLY, 0644)
}
