package snapshot

import (
	"syscall"
	"unsafe"
)

var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func moveFileEx(source, destination *uint16) error {
	result, _, err := moveFileExW.Call(uintptr(unsafe.Pointer(source)), uintptr(unsafe.Pointer(destination)), 0x1|0x8)
	if result == 0 {
		return err
	}
	return nil
}
