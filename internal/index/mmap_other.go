//go:build !unix

package index

import (
	"io"
	"os"
)

// mapFile falls back to reading the whole file where mmap is unavailable.
func mapFile(f *os.File, size int) ([]byte, func() error, error) {
	b := make([]byte, size)
	if _, err := io.ReadFull(f, b); err != nil {
		return nil, nil, err
	}
	return b, func() error { return nil }, nil
}
