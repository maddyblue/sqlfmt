package pretty_test

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/sql/parser"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/mjibson/sqlfmt/pretty"
	"golang.org/x/sync/errgroup"
)

var (
	flagWrite   = flag.Bool("write", false, "write test results to output")
	flagTimeout = flag.Duration("pretty-timeout", 0, "execution timeout per Pretty invocation")
)

func TestPrettier(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("testdata", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		m := m
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			sql, err := ioutil.ReadFile(m)
			if err != nil {
				t.Fatal(err)
			}
			stmt, err := parser.ParseOne(string(sql))
			if err != nil {
				t.Fatal(err)
			}
			doc := tree.Doc(stmt)

			res := make([]string, len(sql)+10)
			work := make(chan int, len(res))
			for i := range res {
				work <- i + 1
			}
			close(work)
			ctx := context.Background()
			g, ctx := errgroup.WithContext(ctx)
			worker := func() error {
				for i := range work {
					pCtx := ctx
					var cancel func()
					if *flagTimeout != 0 {
						pCtx, cancel = context.WithTimeout(pCtx, *flagTimeout)
					}
					s, err := pretty.PrettyString(pCtx, doc, i)
					if err != nil {
						return err
					}
					res[i-1] = s
					if cancel != nil {
						cancel()
					}
				}
				return nil
			}
			for i := 0; i < runtime.NumCPU(); i++ {
				g.Go(worker)
			}
			if err := g.Wait(); err != nil {
				t.Fatal(err)
			}
			var sb strings.Builder
			for i, s := range res {
				if i == 0 || s != res[i-1] {
					fmt.Fprintf(&sb, "%d:\n%s\n%s\n\n", i+1, strings.Repeat("-", i+1), s)
				}
			}
			got := strings.TrimSpace(sb.String())

			ext := filepath.Ext(m)
			outfile := m[:len(m)-len(ext)] + ".golden"

			if *flagWrite {
				if err := ioutil.WriteFile(outfile, []byte(got), 0666); err != nil {
					t.Fatal(err)
				}
				return
			}

			expect, err := ioutil.ReadFile(outfile)
			if err != nil {
				t.Fatal(err)
			}
			if string(expect) != got {
				t.Fatalf("unexpected:\n%s", got)
			}
		})
	}
}
