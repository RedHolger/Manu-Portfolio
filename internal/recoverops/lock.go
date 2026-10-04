package recoverops

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// LockController enforces the single-process SQLite controller contract. Keep
// the descriptor open for the entire serve lifetime, including shutdown.
func LockController(path string) (func(), error) {
	f, err := os.OpenFile(path+".controller.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another controller owns this database: %w", err)
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}
