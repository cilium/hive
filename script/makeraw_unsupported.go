//go:build !darwin && !linux

package script

import (
	"fmt"
	"runtime"
)

func MakeRaw(fd int) (restore func(), err error) {
	return func() {}, fmt.Errorf("MakeRaw: not supported on %s", runtime.GOOS)
}
