package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tshubham2/code-mem/internal/memory"
)

// Set is the stores visible from one working directory, in precedence
// order: the repo store first, then the global one. A slug defined in an
// earlier store shadows the same slug in a later one, and links resolve
// across stores by slug.
type Set struct {
	Stores []*Store
}

// Config locates the stores. Empty fields fall back to the environment
// (CODE_MEM_DIR, CODE_MEM_GLOBAL) and then to discovery.
type Config struct {
	Cwd       string // where to start looking for a repo store
	RepoDir   string // explicit repo .mem directory
	GlobalDir string // explicit global store; default ~/.mem
	NoGlobal  bool
}

// Open discovers the stores for cfg and refreshes their indexes.
func Open(cfg Config) (*Set, error) {
	home, _ := os.UserHomeDir()
	if cfg.GlobalDir == "" {
		cfg.GlobalDir = os.Getenv("CODE_MEM_GLOBAL")
	}
	if cfg.GlobalDir == "" && home != "" {
		cfg.GlobalDir = filepath.Join(home, ".mem")
	}
	if cfg.RepoDir == "" {
		cfg.RepoDir = os.Getenv("CODE_MEM_DIR")
	}
	if cfg.RepoDir == "" {
		cwd := cfg.Cwd
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		cfg.RepoDir = findRepoStore(cwd, home, cfg.GlobalDir)
	}

	set := &Set{}
	if cfg.RepoDir != "" {
		abs, err := filepath.Abs(cfg.RepoDir)
		if err != nil {
			return nil, err
		}
		set.Stores = append(set.Stores, &Store{Dir: abs, Label: filepath.Base(filepath.Dir(abs))})
	}
	if !cfg.NoGlobal && cfg.GlobalDir != "" && !samePath(cfg.GlobalDir, cfg.RepoDir) {
		set.Stores = append(set.Stores, &Store{Dir: cfg.GlobalDir, Label: "global", Global: true})
	}
	if len(set.Stores) == 0 {
		return nil, errors.New("no store: not inside a repo and no global store configured")
	}
	return set, set.Refresh()
}

// findRepoStore walks up from dir to the nearest .mem directory, stopping
// at the home directory. Without one, it proposes <git root>/.mem, which is
// created on the first save.
func findRepoStore(dir, home, global string) string {
	dir, _ = filepath.Abs(dir)
	for d := dir; ; d = filepath.Dir(d) {
		if home != "" && samePath(d, home) {
			break
		}
		c := filepath.Join(d, ".mem")
		if isDir(c) && !samePath(c, global) {
			return c
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	for d := dir; ; d = filepath.Dir(d) {
		if exists(filepath.Join(d, ".git")) {
			return filepath.Join(d, ".mem")
		}
		if filepath.Dir(d) == d || (home != "" && samePath(d, home)) {
			return ""
		}
	}
}

func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }
func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, _ = filepath.Abs(a)
	b, _ = filepath.Abs(b)
	return filepath.Clean(a) == filepath.Clean(b)
}

// Refresh brings every store's index up to date with its files.
func (s *Set) Refresh() error {
	for _, st := range s.Stores {
		if err := st.Refresh(); err != nil {
			return fmt.Errorf("%s: %w", st.Label, err)
		}
	}
	return nil
}

// Close releases every store.
func (s *Set) Close() {
	for _, st := range s.Stores {
		st.Close()
	}
}

// Primary is where new memories go: the repo store if there is one.
func (s *Set) Primary() *Store { return s.Stores[0] }

// Store returns the store with the given label ("global" or the repo name).
func (s *Set) Store(label string) (*Store, error) {
	for _, st := range s.Stores {
		if st.Label == label || (label == "repo" && !st.Global) {
			return st, nil
		}
	}
	return nil, fmt.Errorf("no store %q", label)
}

// Resolve finds the store that defines slug, honouring shadowing.
func (s *Set) Resolve(slug string) (*Store, uint32, bool) {
	for _, st := range s.Stores {
		if id, ok := st.idx.Lookup(slug); ok && !st.idx.Phantom(id) {
			return st, id, true
		}
	}
	return nil, 0, false
}

// shadowed reports whether slug is defined by a store before position i.
func (s *Set) shadowed(i int, slug string) bool {
	for _, st := range s.Stores[:i] {
		if st.Has(slug) {
			return true
		}
	}
	return false
}

// Ref is a memory as seen through the set: its slug plus, when it exists,
// where it lives and its summary fields.
type Ref struct {
	Slug    string
	Store   *Store // nil when no file exists (a dangling link target)
	Type    string
	Cluster string
	Summary string
}

// Ref resolves slug to a Ref, which is a phantom if nothing defines it.
func (s *Set) Ref(slug string) Ref {
	st, id, ok := s.Resolve(slug)
	if !ok {
		return Ref{Slug: slug}
	}
	x := st.idx
	return Ref{Slug: slug, Store: st, Type: x.Type(id), Cluster: x.Cluster(id), Summary: x.Summary(id)}
}

// Hit is a search result.
type Hit struct {
	Ref
	Score float64
}

// Search runs BM25 in every store and merges the results. Scores come from
// per-store statistics, which is close enough for ranking a handful of hits.
func (s *Set) Search(query string, k int, typ, cluster string) []Hit {
	var hits []Hit
	for i, st := range s.Stores {
		x := st.idx
		keep := func(id uint32) bool {
			return (typ == "" || x.Type(id) == typ) &&
				(cluster == "" || x.Cluster(id) == cluster) &&
				!s.shadowed(i, x.Name(id))
		}
		for _, h := range x.Search(query, k, keep) {
			hits = append(hits, Hit{s.Ref(x.Name(h.ID)), h.Score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}

// Neighbor is a memory adjacent to another, with every edge type joining
// them in each direction.
type Neighbor struct {
	Ref
	Out []string // edge types from the subject to this neighbor
	In  []string // edge types from this neighbor to the subject
}

// Neighbors returns the memories one hop from slug in either direction,
// outbound edges first, in authored order.
func (s *Set) Neighbors(slug string) []Neighbor {
	var order []string
	by := map[string]*Neighbor{}
	get := func(name string) *Neighbor {
		n := by[name]
		if n == nil {
			n = &Neighbor{Ref: Ref{Slug: name}}
			by[name] = n
			order = append(order, name)
		}
		return n
	}
	if st, id, ok := s.Resolve(slug); ok {
		out := st.idx.Out(id)
		for i := range out.Len() {
			to, typ := out.At(i)
			n := get(st.idx.Name(to))
			n.Out = appendUnique(n.Out, typ)
		}
	}
	for i, st := range s.Stores {
		id, ok := st.idx.Lookup(slug)
		if !ok {
			continue
		}
		in := st.idx.In(id)
		for j := range in.Len() {
			from, typ := in.At(j)
			name := st.idx.Name(from)
			if s.shadowed(i, name) {
				continue // that version of the source is hidden
			}
			n := get(name)
			n.In = appendUnique(n.In, typ)
		}
	}
	out := make([]Neighbor, 0, len(order))
	for _, name := range order {
		n := by[name]
		n.Ref = s.Ref(name)
		out = append(out, *n)
	}
	return out
}

func appendUnique(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

// Clusters returns every cluster in use across stores, sorted.
func (s *Set) Clusters() []string {
	seen := map[string]bool{}
	var out []string
	for _, st := range s.Stores {
		for _, c := range st.idx.Clusters() {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Rename moves memory old to new in whichever store defines it and
// rewrites every reference to it. References are rewritten in that store
// and in every store that takes precedence over it (they may link to it);
// stores below it cannot see it, so they are left alone. It returns the
// number of other files changed.
func (s *Set) Rename(old, new string) (int, error) {
	if !memory.ValidSlug(new) {
		return 0, fmt.Errorf("invalid slug %q", new)
	}
	st, _, ok := s.Resolve(old)
	if !ok {
		return 0, fmt.Errorf("no memory %q", old)
	}
	if _, err := os.Stat(st.Path(new)); err == nil {
		return 0, fmt.Errorf("%q already exists", new)
	}
	pos := 0
	for i, x := range s.Stores {
		if x == st {
			pos = i
		}
	}
	if err := os.Rename(st.Path(old), st.Path(new)); err != nil {
		return 0, err
	}
	changed := 0
	for _, x := range s.Stores[:pos+1] {
		files, err := x.list()
		if err != nil {
			return changed, err
		}
		for _, f := range files {
			p := filepath.Join(x.memDir(), f.name)
			data, err := os.ReadFile(p)
			if err != nil {
				return changed, err
			}
			out, did := memory.RewriteLinks(data, old, new)
			if !did {
				continue
			}
			if err := writeAtomic(p, out); err != nil {
				return changed, err
			}
			if f.name != new+".md" {
				changed++
			}
		}
	}
	return changed, s.Refresh()
}

// Issue is something mem_check reports.
type Issue struct {
	Store *Store
	Kind  string // "invalid", "dangling", "shadowed", "bad-link"
	Name  string // the memory the issue is in
	Note  string
}

// Check scans every store fully, reporting invalid files, links to
// memories that do not exist (legal, but worth seeing), malformed
// wikilinks, and slugs that shadow a global memory.
func (s *Set) Check() ([]Issue, error) {
	var issues []Issue
	for i, st := range s.Stores {
		mems, probs, err := st.Scan()
		if err != nil {
			return nil, err
		}
		for _, p := range probs {
			issues = append(issues, Issue{st, "invalid", p.Name, p.Err.Error()})
		}
		for _, m := range mems {
			if s.shadowed(i, m.Name) {
				issues = append(issues, Issue{st, "shadowed", m.Name, "a repo memory with the same slug hides this one"})
			}
			for _, e := range m.Edges() {
				if r := s.Ref(e.Target); r.Store == nil {
					issues = append(issues, Issue{st, "dangling", m.Name, e.Type + " → " + e.Target})
				}
			}
			for _, l := range memory.Links(m.Body) {
				if !memory.ValidSlug(l) {
					issues = append(issues, Issue{st, "bad-link", m.Name, "[[" + l + "]] is not a slug"})
				}
			}
		}
	}
	return issues, nil
}

// ReadBody returns the body of a memory from its file.
func (s *Set) ReadBody(r Ref) (string, error) {
	if r.Store == nil {
		return "", fmt.Errorf("no memory %q", r.Slug)
	}
	data, err := os.ReadFile(r.Store.Path(r.Slug))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%q was removed; reindexing", r.Slug)
	}
	if err != nil {
		return "", err
	}
	return memory.Body(data), nil
}

// Describe names the stores for humans.
func (s *Set) Describe() string {
	var parts []string
	for _, st := range s.Stores {
		parts = append(parts, fmt.Sprintf("%s (%s)", st.Label, st.Dir))
	}
	return strings.Join(parts, ", ")
}
