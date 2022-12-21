# python -m http.server --directory web

export GOOS=js
export GOARCH=wasm
go build -o ../docs/sqlfmt.wasm -v
