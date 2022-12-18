# python -m http.server --directory web

export GOOS=js
export GOARCH=wasm
go build -o web/sqlfmt.wasm -v
