// Package store owns one .mem directory: its memory files (the source of
// truth) and the derived index, which it rebuilds whenever the files'
// fingerprint changes.
package store

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tshubham2/code-mem/internal/index"
	"github.com/tshubham2/code-mem/internal/memory"
)

// Store is one .mem directory.
type Store struct {
	Dir    string // the .mem directory itself
	Label  string // repo name, or "global"
	Global bool

	idx *index.Index
}

// Problem is a memory file that could not be indexed.
type Problem struct {
	Name string
	Err  error
}

func (s *Store) memDir() string    { return filepath.Join(s.Dir, "memories") }
func (s *Store) indexPath() string { return filepath.Join(s.Dir, ".index", "index.bin") }

// BootPath is the generated boot index, meant to be committed.
func (s *Store) BootPath() string { return filepath.Join(s.Dir, "index.md") }

// Path is the file holding memory slug.
func (s *Store) Path(slug string) string { return filepath.Join(s.memDir(), slug+".md") }

// Index returns the current index. Call Refresh first.
func (s *Store) Index() *index.Index { return s.idx }

// Has reports whether slug is a real memory in this store.
func (s *Store) Has(slug string) bool {
	id, ok := s.idx.Lookup(slug)
	return ok && !s.idx.Phantom(id)
}

type fileStat struct {
	name  string
	size  int64
	mtime int64
}

func (s *Store) list() ([]fileStat, error) {
	ents, err := os.ReadDir(s.memDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []fileStat
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // removed between ReadDir and Info
		}
		out = append(out, fileStat{e.Name(), info.Size(), info.ModTime().UnixNano()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// Fingerprint hashes the name, size and mtime of every memory file. It
// costs one stat per file and no reads.
func (s *Store) Fingerprint() (uint64, error) {
	files, err := s.list()
	if err != nil {
		return 0, err
	}
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint32(buf[:], index.Version)
	h.Write(buf[:4])
	for _, f := range files {
		h.Write([]byte(f.name))
		h.Write([]byte{0})
		binary.LittleEndian.PutUint64(buf[:], uint64(f.size))
		h.Write(buf[:])
		binary.LittleEndian.PutUint64(buf[:], uint64(f.mtime))
		h.Write(buf[:])
	}
	return h.Sum64(), nil
}

// Scan parses every memory file. Invalid files are returned as problems
// and left out of the result.
func (s *Store) Scan() ([]*memory.Memory, []Problem, error) {
	files, err := s.list()
	if err != nil {
		return nil, nil, err
	}
	var mems []*memory.Memory
	var probs []Problem
	for _, f := range files {
		name := strings.TrimSuffix(f.name, ".md")
		data, err := os.ReadFile(filepath.Join(s.memDir(), f.name))
		if err != nil {
			probs = append(probs, Problem{name, err})
			continue
		}
		m, err := memory.Parse(name, data)
		if err != nil {
			probs = append(probs, Problem{name, err})
			continue
		}
		mems = append(mems, m)
	}
	return mems, probs, nil
}

// Refresh makes Index current: it reuses the index on disk when its
// fingerprint matches the files and rebuilds otherwise.
func (s *Store) Refresh() error {
	fp, err := s.Fingerprint()
	if err != nil {
		return err
	}
	if s.idx != nil && s.idx.Fingerprint() == fp {
		return nil
	}
	if x, err := index.Open(s.indexPath()); err == nil {
		if x.Fingerprint() == fp {
			s.swap(x)
			return nil
		}
		x.Close()
	}
	return s.rebuild(fp)
}

// Rebuild unconditionally rebuilds the index and boot index.
func (s *Store) Rebuild() error {
	fp, err := s.Fingerprint()
	if err != nil {
		return err
	}
	return s.rebuild(fp)
}

func (s *Store) rebuild(fp uint64) error {
	mems, _, err := s.Scan()
	if err != nil {
		return err
	}
	data := index.Build(mems, fp)
	if _, err := os.Stat(s.memDir()); errors.Is(err, fs.ErrNotExist) {
		// Nothing saved yet: keep the index in memory, create no files.
		x, err := index.Decode(data)
		if err != nil {
			return err
		}
		s.swap(x)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.indexPath()), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(s.indexPath(), data); err != nil {
		return err
	}
	x, err := index.Open(s.indexPath())
	if err != nil {
		return err
	}
	s.swap(x)
	return writeIfChanged(s.BootPath(), []byte(BootIndex(s.Label, x)))
}

func (s *Store) swap(x *index.Index) {
	if s.idx != nil {
		s.idx.Close()
	}
	s.idx = x
}

// Close releases the mapped index.
func (s *Store) Close() error {
	if s.idx == nil {
		return nil
	}
	err := s.idx.Close()
	s.idx = nil
	return err
}

// Read loads one memory file.
func (s *Store) Read(slug string) (*memory.Memory, error) {
	data, err := os.ReadFile(s.Path(slug))
	if err != nil {
		return nil, err
	}
	return memory.Parse(slug, data)
}

// Write saves m, creating the store on first use, and refreshes the index.
func (s *Store) Write(m *memory.Memory) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if err := s.ensure(); err != nil {
		return err
	}
	if err := writeAtomic(s.Path(m.Name), m.Format()); err != nil {
		return err
	}
	return s.Refresh()
}

// ensure creates the store layout and its .gitignore.
func (s *Store) ensure() error {
	if err := os.MkdirAll(s.memDir(), 0o755); err != nil {
		return err
	}
	gi := filepath.Join(s.Dir, ".gitignore")
	if _, err := os.Stat(gi); errors.Is(err, fs.ErrNotExist) {
		return os.WriteFile(gi, []byte(".index/\n"), 0o644)
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { // CreateTemp uses 0600
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("write boot index: %w", err)
	}
	return nil
}
