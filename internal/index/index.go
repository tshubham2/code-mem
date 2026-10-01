package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
)

var le = binary.LittleEndian

// ErrStale means the file exists but was written by another format version.
var ErrStale = errors.New("index format version mismatch")

// Index is an open, read-only view of an index file. All lookups read the
// mapped bytes in place.
type Index struct {
	b       []byte
	unmap   func() error
	sec     [secCount]uint32
	n, real uint32
	avgLen  float32

	etypes, types, clusters []string
}

// Open maps the index at path.
func Open(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() < headerSize {
		return nil, fmt.Errorf("%s: truncated index", path)
	}
	b, unmap, err := mapFile(f, int(st.Size()))
	if err != nil {
		return nil, err
	}
	x, err := decode(b)
	if err != nil {
		unmap()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	x.unmap = unmap
	return x, nil
}

// Decode wraps an in-memory index, as returned by Build.
func Decode(b []byte) (*Index, error) { return decode(b) }

func decode(b []byte) (*Index, error) {
	if len(b) < headerSize || string(b[hMagic:hMagic+4]) != magic {
		return nil, errors.New("not a code-mem index")
	}
	if le.Uint32(b[hVersion:]) != Version {
		return nil, ErrStale
	}
	x := &Index{b: b, unmap: func() error { return nil }}
	for i := range x.sec {
		x.sec[i] = le.Uint32(b[hSections+4*i:])
	}
	if int(x.sec[secEnd]) != len(b) {
		return nil, errors.New("index size does not match header")
	}
	x.n = le.Uint32(b[hNodes:])
	x.real = le.Uint32(b[hReal:])
	x.avgLen = math.Float32frombits(le.Uint32(b[hAvgDocLen:]))

	p := x.sec[secTables]
	for _, dst := range []*[]string{&x.etypes, &x.types, &x.clusters} {
		count := le.Uint32(b[p:])
		p += 4
		for range count {
			l := uint32(le.Uint16(b[p:]))
			*dst = append(*dst, string(b[p+2:p+2+l]))
			p += 2 + l
		}
	}
	return x, nil
}

// Close unmaps the file. The Index must not be used afterwards.
func (x *Index) Close() error { return x.unmap() }

// Fingerprint identifies the source files the index was built from.
func (x *Index) Fingerprint() uint64 { return le.Uint64(x.b[hFinger:]) }

// Len is the number of nodes, phantoms included.
func (x *Index) Len() int { return int(x.n) }

// Real is the number of nodes backed by a memory file. Ids [0, Real) are
// real memories; ids [Real, Len) are phantoms.
func (x *Index) Real() int { return int(x.real) }

// Phantom reports whether id is a link target with no file.
func (x *Index) Phantom(id uint32) bool { return id >= x.real }

// Clusters returns every cluster name in the index.
func (x *Index) Clusters() []string { return x.clusters }

func (x *Index) rec(id uint32) []byte {
	o := x.sec[secNodes] + id*nodeSize
	return x.b[o : o+nodeSize]
}

func (x *Index) str(off, l uint32) string {
	base := x.sec[secStrings]
	return string(x.b[base+off : base+off+l])
}

// Name returns the slug of node id.
func (x *Index) Name(id uint32) string {
	r := x.rec(id)
	return x.str(le.Uint32(r[0:]), uint32(le.Uint16(r[8:])))
}

func (x *Index) nameEq(id uint32) []byte {
	r := x.rec(id)
	base := x.sec[secStrings] + le.Uint32(r[0:])
	return x.b[base : base+uint32(le.Uint16(r[8:]))]
}

// Summary returns the one-line summary of node id ("" for phantoms).
func (x *Index) Summary(id uint32) string {
	r := x.rec(id)
	return x.str(le.Uint32(r[4:]), uint32(le.Uint16(r[10:])))
}

// Type returns the memory type of node id ("" for phantoms).
func (x *Index) Type(id uint32) string {
	if x.Phantom(id) {
		return ""
	}
	return x.types[le.Uint16(x.rec(id)[12:])]
}

// Cluster returns the cluster of node id ("" for phantoms).
func (x *Index) Cluster(id uint32) string {
	if x.Phantom(id) {
		return ""
	}
	return x.clusters[le.Uint16(x.rec(id)[14:])]
}

func (x *Index) docLen(id uint32) uint32 { return le.Uint32(x.rec(id)[16:]) }

// Lookup finds a node by slug, real or phantom.
func (x *Index) Lookup(name string) (uint32, bool) {
	find := func(lo, hi uint32) (uint32, bool) {
		i := lo + uint32(sort.Search(int(hi-lo), func(i int) bool {
			return string(x.nameEq(lo+uint32(i))) >= name
		}))
		return i, i < hi && string(x.nameEq(i)) == name
	}
	if id, ok := find(0, x.real); ok {
		return id, true
	}
	return find(x.real, x.n)
}

// Edges is a zero-copy view of one node's adjacency list.
type Edges struct {
	x        *Index
	tgt, typ uint32 // section bases
	lo, hi   uint32
}

// Len is the number of edges.
func (e Edges) Len() int { return int(e.hi - e.lo) }

// At returns the other endpoint and the edge type of edge i.
func (e Edges) At(i int) (uint32, string) {
	k := e.lo + uint32(i)
	return le.Uint32(e.x.b[e.tgt+4*k:]), e.x.etypes[e.x.b[e.typ+k]]
}

func (x *Index) adj(off, tgt, typ int, id uint32) Edges {
	o := x.sec[off]
	return Edges{x, x.sec[tgt], x.sec[typ], le.Uint32(x.b[o+4*id:]), le.Uint32(x.b[o+4*id+4:])}
}

// Out returns the edges leaving id: one reslice of the CSR arrays.
func (x *Index) Out(id uint32) Edges { return x.adj(secOutOff, secOutTgt, secOutTyp, id) }

// In returns the edges arriving at id; At yields the source node.
func (x *Index) In(id uint32) Edges { return x.adj(secInOff, secInTgt, secInTyp, id) }

// Hit is one search result.
type Hit struct {
	ID    uint32
	Score float64
}

// BM25 parameters: the textbook defaults.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

func (x *Index) term(i uint32) []byte {
	o := x.sec[secTermOff]
	a, b := le.Uint32(x.b[o+4*i:]), le.Uint32(x.b[o+4*i+4:])
	base := x.sec[secTermBlob]
	return x.b[base+a : base+b]
}

func (x *Index) findTerm(t string) (uint32, bool) {
	nt := le.Uint32(x.b[hTerms:])
	i := uint32(sort.Search(int(nt), func(i int) bool { return string(x.term(uint32(i))) >= t }))
	return i, i < nt && string(x.term(i)) == t
}

// Search ranks real memories against query with BM25 and returns the top
// k for which keep returns true (keep may be nil).
func (x *Index) Search(query string, k int, keep func(id uint32) bool) []Hit {
	if x.real == 0 || k <= 0 {
		return nil
	}
	scores := map[uint32]float64{}
	seen := map[string]bool{}
	N := float64(x.real)
	for _, t := range Tokenize(query) {
		if seen[t] {
			continue
		}
		seen[t] = true
		ti, ok := x.findTerm(t)
		if !ok {
			continue
		}
		po := x.sec[secPostOff]
		lo, hi := le.Uint32(x.b[po+4*ti:]), le.Uint32(x.b[po+4*ti+4:])
		df := float64(hi - lo)
		idf := math.Log(1 + (N-df+0.5)/(df+0.5))
		base := x.sec[secPostings]
		for p := lo; p < hi; p++ {
			doc := le.Uint32(x.b[base+8*p:])
			tf := float64(le.Uint32(x.b[base+8*p+4:]))
			norm := 1 - bm25B + bm25B*float64(x.docLen(doc))/float64(x.avgLen)
			scores[doc] += idf * tf * (bm25K1 + 1) / (tf + bm25K1*norm)
		}
	}
	hits := make([]Hit, 0, len(scores))
	for id, s := range scores {
		if keep == nil || keep(id) {
			hits = append(hits, Hit{id, s})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}
