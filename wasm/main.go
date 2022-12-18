package main

import (
	"syscall/js"

	"github.com/cockroachdb/cockroachdb-parser/pkg/sql/sem/tree"
	"github.com/mjibson/sqlfmt"
)

func main() {
	js.Global().Set("FmtSQL", FmtSQL())
	select {}
}

func FmtSQL() js.Func {
	jsonFunc := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) != 1 {
			return "Invalid no of arguments passed"
		}
		input := args[0].String()

		cfg := tree.DefaultPrettyCfg()
		pretty, err := sqlfmt.FmtSQL(cfg, []string{input})
		if err != nil {
			return err.Error()
		}
		return pretty
	})
	return jsonFunc
}
