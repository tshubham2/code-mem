package store

import (
	"fmt"
	"os"
	"testing"
)

// BenchmarkRefreshUnchanged measures the per-tool-call freshness check:
// one stat per memory file, no reads, index already current.
func BenchmarkRefreshUnchanged(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			dir := b.TempDir()
			os.MkdirAll(dir+"/memories", 0o755)
			for i := range n {
				os.WriteFile(fmt.Sprintf("%s/memories/m%d.md", dir, i),
					[]byte(fmt.Sprintf("---\ntype: bug\nsummary: s%d\ncluster: c\n---\n", i)), 0o644)
			}
			st := &Store{Dir: dir, Label: "b"}
			if err := st.Refresh(); err != nil {
				b.Fatal(err)
			}
			defer st.Close()
			b.ResetTimer()
			for range b.N {
				st.Refresh()
			}
		})
	}
}
