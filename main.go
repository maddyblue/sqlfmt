package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"

	"github.com/cockroachdb/cockroach/pkg/sql/parser"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/kelseyhightower/envconfig"
	"github.com/mjibson/sqlfmt/pretty"
)

type Specification struct {
	Addr string
}

func main() {
	var spec Specification
	err := envconfig.Process("sqlfmt", &spec)
	if err != nil {
		log.Fatal(err.Error())
	}
	if spec.Addr == "" {
		spec.Addr = ":80"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(Index))
	})
	http.HandleFunc("/fmt", wrap(Fmt))
	srv := &http.Server{
		Addr: spec.Addr,
	}
	go func() {
		fmt.Printf("listening on http://%s\n", spec.Addr)
		log.Fatal(srv.ListenAndServe())
	}()
	c := make(chan os.Signal, 1)
	signal.Notify(c)
	<-c
	srv.Close()
}

func wrap(f func(http.ResponseWriter, *http.Request) interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res := f(w, r)
		w.Header().Add("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(res); err != nil {
			log.Print(err)
		}
	}
}

var cache = struct {
	sync.Mutex
	m map[string][]string
}{
	m: make(map[string][]string),
}

func Fmt(w http.ResponseWriter, r *http.Request) interface{} {
	cache.Lock()
	hit, ok := cache.m[r.URL.RawQuery]
	cache.Unlock()
	if ok {
		return hit
	}

	n, err := strconv.Atoi(r.FormValue("n"))
	if err != nil {
		return err
	}
	sl, err := parser.Parse(r.FormValue("sql"))
	if err != nil {
		return []string{err.Error()}
	}
	res := make([]string, len(sl))
	for i, s := range sl {
		res[i], _ = pretty.PrettyString(tree.Doc(s), n)
	}
	cache.Lock()
	if len(cache.m) > 10000 {
		cache.m = make(map[string][]string)
	}
	cache.m[r.URL.RawQuery] = res
	cache.Unlock()
	return res
}

const Index = `<!DOCTYPE html>
<html>
<head>
<meta http-equiv="Content-Type" content="text/html; charset=utf-8">
<title>sqlfmt</title>
</head>
<body>
<p>Type some SQL into the box (multiple statements supported). Move the slider to adjust the desired max-width of the output. Partial SQL support only. <a href="https://github.com/mjibson/sqlfmt">code</a></p>
<textarea id="sql" style="width: 100%; max-width: 600px; height: 150px" onChange="range()" onInput="range()">SELECT count(*) count, winner, counter * 60 * 5 as counter FROM (SELECT winner, round(length / 60 / 5) as counter FROM players WHERE build = $1 AND (hero = $2 OR region = $3)) GROUP BY winner, counter</textarea>
<br><input type="range" min="1" max="200" step="1" name="n" value="20" onChange="range()" onInput="range()" id="n" style="width: 100%">
<br>width: <span id="nval"></span>
<br><pre id="fmt"></pre>
<pre id="width" style="position: absolute; visibility: hidden; height: auto; width: auto;">_</pre>
<script>
// Some hax to make the slider position the same width as the displayed text.
const width = document.getElementById('width').clientWidth;
document.getElementById('n').max = window.innerWidth / width;

let working = false;
let pending = false;
function range() {
	if (working) {
		pending = true;
		return;
	}
	working = true;
	const n = document.getElementById("n").value;
	document.getElementById("nval").innerText = n;
	const sql = document.getElementById("sql").value;
	fetch('/fmt?n=' + n + '&sql=' + encodeURIComponent(sql))
	.then(resp => {
		working = false;
		resp.json().then(data => {
			const fmt = document.getElementById("fmt");
			fmt.innerText = data.map(d => d + ';').join('\n\n') + '\n\n' + '#'.repeat(n);
			if (pending) {
				range();
				pending = false;
			}
		}, console.log);
	}, d => {
		working = false;
		console.log(d);
	});
}

range();

document.addEventListener('keydown', e => {
	const code = e.keyCode;
	let n = document.getElementById("n");
	switch (code) {
	case 37:
		n.value--;
		range();
		break;
	case 39:
		n.value++;
		range();
		break;
	}
});
</script>
</body>
</html>`
