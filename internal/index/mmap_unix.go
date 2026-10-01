//go:build unix

package index

import (
	"os"
	"syscall"
)

// mapFile maps f read-only and shared, so every process attached to the
// same index shares one copy of its pages.
func mapFile(f *os.File, size int) ([]byte, func() error, error) {
	b, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, nil, err
	}
	return b, func() error { return syscall.Munmap(b) }, nil
}
