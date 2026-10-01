// Command code-mem is a file-based, graph-linked memory store for coding
// agents. `code-mem serve` speaks MCP on stdio; the other subcommands do
// the same things from a terminal.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tshubham2/code-mem/internal/mcp"
	"github.com/tshubham2/code-mem/internal/render"
	"github.com/tshubham2/code-mem/internal/store"
)

const usage = `usage: code-mem [flags] <command> [args]

commands:
  serve                     run the MCP server on stdin/stdout
  search <query...>         BM25 search  (-type, -cluster, -n)
  get <slug>                one memory and its neighbors  (-hops)
  boot                      print the boot index for every store
  check                     report invalid files and dangling links
  rename <from> <to>        rename a memory and rewrite links to it
  index                     force a rebuild of every index

flags:
  -dir <path>       repo store (default: nearest .mem/, else <git root>/.mem)
  -global <path>    global store (default: $CODE_MEM_GLOBAL or ~/.mem)
  -no-global        ignore the global store
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "code-mem:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("code-mem", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	var cfg store.Config
	fs.StringVar(&cfg.RepoDir, "dir", "", "")
	fs.StringVar(&cfg.GlobalDir, "global", "", "")
	fs.BoolVar(&cfg.NoGlobal, "no-global", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return fmt.Errorf("missing command")
	}
	cmd, rest := fs.Arg(0), fs.Args()[1:]

	set, err := store.Open(cfg)
	if err != nil {
		return err
	}
	defer set.Close()

	switch cmd {
	case "serve":
		return (&mcp.Server{Set: set}).Serve(os.Stdin, os.Stdout)

	case "search":
		sf := flag.NewFlagSet("search", flag.ContinueOnError)
		typ := sf.String("type", "", "only this memory type")
		cluster := sf.String("cluster", "", "only this cluster")
		n := sf.Int("n", 5, "max hits")
		words, err := parseInterspersed(sf, rest)
		if err != nil {
			return err
		}
		if len(words) == 0 {
			return fmt.Errorf("search: missing query")
		}
		fmt.Print(mcp.Search(set, strings.Join(words, " "), *typ, *cluster, *n))

	case "get":
		gf := flag.NewFlagSet("get", flag.ContinueOnError)
		hops := gf.Int("hops", 1, "graph distance to include")
		slugs, err := parseInterspersed(gf, rest)
		if err != nil {
			return err
		}
		if len(slugs) != 1 {
			return fmt.Errorf("get: want exactly one slug")
		}
		out, err := render.Get(set, slugs[0], *hops)
		if err != nil {
			return err
		}
		fmt.Print(out)

	case "boot":
		fmt.Print(render.Boot(set))

	case "check":
		issues, err := set.Check()
		if err != nil {
			return err
		}
		fmt.Print(render.Check(issues))
		for _, is := range issues {
			if is.Kind == "invalid" || is.Kind == "bad-link" {
				os.Exit(1)
			}
		}

	case "rename":
		if len(rest) != 2 {
			return fmt.Errorf("rename: want <from> <to>")
		}
		n, err := set.Rename(rest[0], rest[1])
		if err != nil {
			return err
		}
		fmt.Printf("renamed %s → %s; rewrote links in %d other files\n", rest[0], rest[1], n)

	case "index":
		for _, st := range set.Stores {
			if err := st.Rebuild(); err != nil {
				return fmt.Errorf("%s: %w", st.Label, err)
			}
			x := st.Index()
			fmt.Printf("%s: %d memories, %d link targets not written yet (%s)\n",
				st.Label, x.Real(), x.Len()-x.Real(), st.Dir)
		}

	default:
		fs.Usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
	return nil
}

// parseInterspersed parses flags wherever they appear among positional
// arguments, so `search redis lag -n 3` works as well as `search -n 3 redis lag`.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
