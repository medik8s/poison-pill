package watchdog

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	. "golang.org/x/sys/unix"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	watchdogDevice = "/dev/watchdog1"
)

var (
	log = ctrl.Log.WithName("watchdog")
)

var _ Watchdog = &linuxWatchdog{}

type linuxWatchdog struct {
	fd           int
	info         *watchdogInfo
	stop         chan struct{}
	lastFoodTime time.Time
}

type watchdogInfo struct {
	options         uint32
	firmwareVersion uint32
	identity        [32]byte
}

func StartWatchdog() (Watchdog, error) {

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
		fd:   wdFd,
		info: getInfo(wdFd),
		stop: stop,
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
				return
			case <-ticker.C:
				if err := wd.feed(); err != nil {
					log.Error(err, "failed to feed watchdog!")
				} else {
					wd.lastFoodTime = time.Now()
				}
			}
		}
	}()

	return wd, nil
}

func (wd *linuxWatchdog) Stop() {
	select {
	case wd.stop <- struct{}{}:
	default: // no-op, already stopped
	}
}

func (wd *linuxWatchdog) LastFoodTime() time.Time {
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
