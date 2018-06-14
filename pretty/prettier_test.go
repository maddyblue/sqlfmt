package pretty_test

import (
	"fmt"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/sql/parser"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/mjibson/sqlfmt/pretty"
)

func TestPrettier(t *testing.T) {
	const sql = `SELECT count(*) count, winner, counter * 60 * 5 as counter FROM (SELECT winner, round(length / 60 / 5) as counter FROM players WHERE build = $1 AND (hero = $2 OR region = $3)) GROUP BY winner, counter`

	stmt, err := parser.ParseOne(sql)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pretty.PrettyString(tree.Doc(stmt), 20)

	fmt.Println(got)
}
