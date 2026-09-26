package snapshot

import "syscall"

func replaceFile(source, destination string) error {
	// MoveFileEx replaces the destination without first deleting the old snapshot.
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return moveFileEx(from, to)
}
