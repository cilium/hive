//go:build darwin || linux

package script

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// MakeRaw sets the terminal to raw mode, but with interrupt signals enabled.
func MakeRaw(fd int) (restore func(), err error) {
	var ioctlReadTermios, ioctlWriteTermios uint
	if runtime.GOOS == "darwin" {
		ioctlReadTermios = 0x40487413  // unix.TIOCGETA
		ioctlWriteTermios = 0x80487414 // unix.TIOCSETA
	} else {
		ioctlReadTermios = 0x5401  // unix.TCGETS
		ioctlWriteTermios = 0x5402 // unix.TCSETS
	}
	termios, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return nil, err
	}

	oldState := *termios

	termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	termios.Oflag &^= unix.OPOST
	termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.IEXTEN
	termios.Lflag |= unix.ISIG // Enable interrupt signals
	termios.Cflag &^= unix.CSIZE | unix.PARENB
	termios.Cflag |= unix.CS8
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, termios); err != nil {
		return nil, err
	}

	return func() {
		unix.IoctlSetTermios(fd, ioctlWriteTermios, &oldState)
	}, nil
}
