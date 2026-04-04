package cli

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	reset  = ""
	bold   = ""
	red    = ""
	green  = ""
	yellow = ""
	cyan   = ""
	gray   = ""
)

func init() {
	if isTerminal(os.Stdout.Fd()) {
		reset = "\033[0m"
		bold = "\033[1m"
		red = "\033[31m"
		green = "\033[32m"
		yellow = "\033[33m"
		cyan = "\033[36m"
		gray = "\033[90m"
	}
}

func isTerminal(fd uintptr) bool {
	var termios syscall.Termios
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, fd,
		uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return err == 0
}
