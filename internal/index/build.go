// Package index is the index plane: a single derived binary file holding
// the node table, the graph in CSR form (both directions), and BM25
// postings. It is mmap'd and read in place; nothing is parsed at startup
// beyond a few tiny string tables. Deleting it is always safe.
//
// Layout (all integers little-endian, every section 4-byte aligned):
//
//	header    128 bytes, see the h* offsets below
//	tables    edge types, memory types, clusters: u32 count, then u16-len strings
//	nodes     nodeSize bytes per node; real memories sorted by name, then
//	          phantoms (link targets with no file) sorted by name, so a node
//	          is a phantom exactly when its id >= the header's real count
//	strings   names and summaries
//	out/in    CSR: offsets u32[N+1], targets u32[E], edge types u8[E]
//	terms     offsets u32[T+1] into a sorted term blob
//	postings  offsets u32[T+1] into (doc u32, tf u32) pairs
package index

import (
	"bytes"
	"encoding/binary"
	"math"
	"sort"

	"github.com/tshubham2/code-mem/internal/memory"
)

const (
	magic   = "CMEM"
	Version = 1

	headerSize = 128
	nodeSize   = 20 // nameOff u32, sumOff u32, nameLen u16, sumLen u16, type u16, cluster u16, docLen u32
)

// Header field offsets.
const (
	hMagic     = 0
	hVersion   = 4
	hFinger    = 8
	hNodes     = 16
	hReal      = 20
	hEdges     = 24
	hTerms     = 28
	hPostings  = 32
	hAvgDocLen = 36
	hSections  = 40 // secCount u32 section offsets follow
)

// Section indexes into the header's offset table.
const (
	secTables = iota
	secNodes
	secStrings
	secOutOff
	secOutTgt
	secOutTyp
	secInOff
	secInTgt
	secInTyp
	secTermOff
	secTermBlob
	secPostOff
	secPostings
	secEnd
	secCount
)

// Field weights for BM25: the summary is the claim the agent searches for,
// the slug is the handle it already knows.
const (
	weightName    = 2
	weightSummary = 3
	weightBody    = 1
)

type node struct {
	name, summary string
	typ, cluster  uint16
	docLen        uint32
}

type edge struct {
	from, to uint32
	typ      uint8
}

// Build encodes mems (which must already be valid and uniquely named) into
// an index file stamped with fingerprint.
func Build(mems []*memory.Memory, fingerprint uint64) []byte {
	mems = append([]*memory.Memory(nil), mems...)
	sort.Slice(mems, func(i, j int) bool { return mems[i].Name < mems[j].Name })

	types, clusters, etypes := newTable(), newTable(), newTable()
	ids := map[string]uint32{}
	var nodes []node
	for i, m := range mems {
		ids[m.Name] = uint32(i)
		nodes = append(nodes, node{
			name: m.Name, summary: m.Summary,
			typ: types.id(m.Type), cluster: clusters.id(m.Cluster),
		})
	}

	// Phantoms: link targets that have no file yet.
	var phantoms []string
	for _, m := range mems {
		for _, e := range m.Edges() {
			if _, ok := ids[e.Target]; !ok {
				ids[e.Target] = math.MaxUint32
				phantoms = append(phantoms, e.Target)
			}
		}
	}
	sort.Strings(phantoms)
	for _, p := range phantoms {
		ids[p] = uint32(len(nodes))
		nodes = append(nodes, node{name: p})
	}

	var edges []edge
	for i, m := range mems {
		for _, e := range m.Edges() {
			t := etypes.id(e.Type)
			if t > math.MaxUint8 {
				// Edge types are stored in one byte. Past 256 distinct types
				// the taxonomy has failed anyway; fold the rest together.
				t = etypes.id("related")
			}
			edges = append(edges, edge{uint32(i), ids[e.Target], uint8(t)})
		}
	}

	// Postings.
	postings := map[string][]posting{}
	var totalLen uint64
	for i, m := range mems {
		tf := map[string]uint32{}
		var n uint32
		add := func(text string, w uint32) {
			for _, t := range Tokenize(text) {
				tf[t] += w
				n += w
			}
		}
		add(m.Name, weightName)
		add(m.Summary, weightSummary)
		add(m.Cluster+" "+m.Type, weightBody)
		add(m.Body, weightBody)
		nodes[i].docLen = n
		totalLen += uint64(n)
		for t, f := range tf {
			postings[t] = append(postings[t], posting{uint32(i), f})
		}
	}
	terms := make([]string, 0, len(postings))
	for t := range postings {
		terms = append(terms, t)
	}
	sort.Strings(terms)
	avg := float32(0)
	if len(mems) > 0 {
		avg = float32(totalLen) / float32(len(mems))
	}

	// Encode.
	var w writer
	w.buf.Write(make([]byte, headerSize))
	var sec [secCount]uint32

	sec[secTables] = w.mark()
	for _, t := range []*table{etypes, types, clusters} {
		w.u32(uint32(len(t.names)))
		for _, s := range t.names {
			w.u16(uint16(len(s)))
			w.buf.WriteString(s)
		}
	}

	// Strings first so node records can point into them.
	var strs bytes.Buffer
	nameOff := make([]uint32, len(nodes))
	sumOff := make([]uint32, len(nodes))
	for i, n := range nodes {
		nameOff[i] = uint32(strs.Len())
		strs.WriteString(n.name)
		sumOff[i] = uint32(strs.Len())
		strs.WriteString(truncate(n.summary, math.MaxUint16))
	}

	sec[secNodes] = w.mark()
	for i, n := range nodes {
		w.u32(nameOff[i])
		w.u32(sumOff[i])
		w.u16(uint16(len(n.name)))
		w.u16(uint16(len(truncate(n.summary, math.MaxUint16))))
		w.u16(n.typ)
		w.u16(n.cluster)
		w.u32(n.docLen)
	}
	sec[secStrings] = w.mark()
	w.buf.Write(strs.Bytes())

	writeCSR := func(offSec, tgtSec, typSec int, key func(edge) uint32, val func(edge) uint32) {
		sorted := append([]edge(nil), edges...)
		sort.SliceStable(sorted, func(i, j int) bool { return key(sorted[i]) < key(sorted[j]) })
		sec[offSec] = w.mark()
		k := 0
		for n := 0; n <= len(nodes); n++ {
			for k < len(sorted) && int(key(sorted[k])) < n {
				k++
			}
			w.u32(uint32(k))
		}
		sec[tgtSec] = w.mark()
		for _, e := range sorted {
			w.u32(val(e))
		}
		sec[typSec] = w.mark()
		for _, e := range sorted {
			w.buf.WriteByte(e.typ)
		}
	}
	writeCSR(secOutOff, secOutTgt, secOutTyp, func(e edge) uint32 { return e.from }, func(e edge) uint32 { return e.to })
	writeCSR(secInOff, secInTgt, secInTyp, func(e edge) uint32 { return e.to }, func(e edge) uint32 { return e.from })

	sec[secTermOff] = w.mark()
	off := uint32(0)
	for _, t := range terms {
		w.u32(off)
		off += uint32(len(t))
	}
	w.u32(off)
	sec[secTermBlob] = w.mark()
	for _, t := range terms {
		w.buf.WriteString(t)
	}

	sec[secPostOff] = w.mark()
	off = 0
	for _, t := range terms {
		w.u32(off)
		off += uint32(len(postings[t]))
	}
	w.u32(off)
	sec[secPostings] = w.mark()
	for _, t := range terms {
		ps := postings[t]
		sort.Slice(ps, func(i, j int) bool { return ps[i].doc < ps[j].doc })
		for _, p := range ps {
			w.u32(p.doc)
			w.u32(p.tf)
		}
	}
	sec[secEnd] = w.mark()

	out := w.buf.Bytes()
	le := binary.LittleEndian
	copy(out[hMagic:], magic)
	le.PutUint32(out[hVersion:], Version)
	le.PutUint64(out[hFinger:], fingerprint)
	le.PutUint32(out[hNodes:], uint32(len(nodes)))
	le.PutUint32(out[hReal:], uint32(len(mems)))
	le.PutUint32(out[hEdges:], uint32(len(edges)))
	le.PutUint32(out[hTerms:], uint32(len(terms)))
	le.PutUint32(out[hPostings:], off)
	le.PutUint32(out[hAvgDocLen:], math.Float32bits(avg))
	for i, s := range sec {
		le.PutUint32(out[hSections+4*i:], s)
	}
	return out
}

type posting struct{ doc, tf uint32 }

type table struct {
	names []string
	ids   map[string]uint16
}

func newTable() *table { return &table{ids: map[string]uint16{}} }

func (t *table) id(s string) uint16 {
	if id, ok := t.ids[s]; ok {
		return id
	}
	id := uint16(len(t.names))
	t.ids[s] = id
	t.names = append(t.names, s)
	return id
}

type writer struct{ buf bytes.Buffer }

// mark pads to 4-byte alignment and returns the current offset.
func (w *writer) mark() uint32 {
	for w.buf.Len()%4 != 0 {
		w.buf.WriteByte(0)
	}
	return uint32(w.buf.Len())
}

func (w *writer) u32(v uint32) { w.buf.Write(binary.LittleEndian.AppendUint32(nil, v)) }
func (w *writer) u16(v uint16) { w.buf.Write(binary.LittleEndian.AppendUint16(nil, v)) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
