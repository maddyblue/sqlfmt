package main

import (
	"crypto/tls"
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
	"github.com/cockroachdb/cockroach/pkg/util/pretty"
	"github.com/kelseyhightower/envconfig"
	"golang.org/x/crypto/acme/autocert"

	// Initialize the builtins.
	_ "github.com/cockroachdb/cockroach/pkg/sql/sem/builtins"
)

type Specification struct {
	Addr     string
	Redir    string
	Autocert []string
	Cache    string
}

func main() {
	var spec Specification
	err := envconfig.Process("sqlfmt", &spec)
	if err != nil {
		log.Fatal(err.Error())
	}
	fmt.Printf("SPEC: %#v\n", spec)
	if spec.Addr == "" {
		spec.Addr = ":80"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(Index))
	})
	mux.HandleFunc("/fmt", wrap(Fmt))
	srv := &http.Server{
		Addr:    spec.Addr,
		Handler: mux,
	}

	if len(spec.Autocert) > 0 {
		m := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(spec.Autocert...),
			Cache:      autocert.DirCache(spec.Cache),
		}
		tlsConfig := &tls.Config{GetCertificate: m.GetCertificate}
		go func() {
			log.Fatal(http.ListenAndServe(spec.Redir, m.HTTPHandler(nil)))
		}()
		srv.TLSConfig = tlsConfig
		go func() {
			log.Fatal(srv.ListenAndServeTLS("", ""))
		}()
	} else {
		go func() {
			fmt.Println("HTTP listen on addr:", spec.Addr)
			log.Fatal(srv.ListenAndServe())
		}()
	}

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
		res[i], _ = pretty.PrettyString(r.Context(), tree.Doc(s), n)
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
<link href="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAABmJLR0QA/wD/AP+gvaeTAAAACXBIWXMAAAsTAAALEwEAmpwYAAAAB3RJTUUH4gYRBwgDpCIYRAAAAB1pVFh0Q29tbWVudAAAAAAAQ3JlYXRlZCB3aXRoIEdJTVBkLmUHAAACiElEQVRYw+2XPWsyQRDHfX8Dq1iILyBBVDSVoFiLWogWJpAikkpiISJim0+gIgh+hBRBEBFCwEIURYQU6bS0CBYRJeALRBKCE3bhFu8xnt4ZnmsysLfc/2aOn3uzM6sAeDYBujw9PUE4HAaDwQBSqRSMRiNcX19Dp9PZClitVnB7ewsmkwlkMhme0T3SaS8WCMhgBKhWqyASiWgBTMGhUOhHP/QDOAGcnZ0Rx3w+D+/v7zAcDiEWi20FPzw8EN/z83NYLBZ4prTHx0f2AHK5nDgOBgPaw1wuR7u/uroivq1WC2vNZpNo0WiUPYDVaiWOTqcTGo0GrNfrH50tFgvxHY/HWHt9fSWazWZjD3B/fw9CoZAWgJKxUCjgz7FpKpWK+Hx+fmLt4+ODaGq1mj0AurTbbfB6vVsgaEWm0ylx3kxWapXQTGkSiYQbAGUvLy+QzWZBo9GQ4Hg8fvAKnJycHAdAWbfbJcFarZboZrOZMQccDgd7gFQqBV9fXzQRbS8qGBUbyi4vLxl3QSKRYA9AfetarQaz2Qze3t4gnU6TYLvdTpwrlQrRLy4utupAr9fjBrBroKQsl8vEGSUcStaffJPJ5M5KyFRlBYj65uYGUD1QKBS4F+h0OohEInh5/7XlcgmZTAb0ej3ZFaenpzCfz7kBHNPJUANyu92kCI1GI27d8BhDu8DlcsHd3R33dsz7eYBXgEOTZTOxgsEg7SV+v3+n794k5AKAav5kMsEaqohisfh3AA5YLjJKpRLWisXi3qLD9JwzgMfjwRq1Df87gFKphHq9jovXrwHsO5Ru6oFAAHdKn8/HDwA6MaEZnR94+QT9fh/Pz8/P/ACw+Q/wB/AHwAjAdzf8BhzwU5PzE5t1AAAAAElFTkSuQmCC" rel="icon" type="image/png">
<title>sequel  fumpt</title>
<style>
body {
	max-width: 38rem;
	margin-left: auto;
	margin-right: auto;
	color: #303030;
	font-size: 16px;
	font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
}
input {
	margin: 0;
	padding: 0;
}
</style>
</head>
<body>
<h1>sequel  fumpt</h1>
<p>Type some SQL into the box (multiple statements supported). Move the slider to adjust the desired max-width of the output. Partial SQL support only. <a href="https://github.com/mjibson/sqlfmt">code</a></p>
<textarea id="sql" style="width: 100%; height: 150px" onChange="range()" onInput="range()">SELECT count(*) count, winner, counter * 60 * 5 as counter FROM (SELECT winner, round(length / 60 / 5) as counter FROM players WHERE build = $1 AND (hero = $2 OR region = $3)) GROUP BY winner, counter</textarea>
<br><input type="range" min="1" max="200" step="1" name="n" value="40" onChange="range()" onInput="range()" id="n" style="width: 100%">
<br>width: <span id="nval"></span>
<br><pre id="fmt"></pre>
<pre id="width" style="position: absolute; visibility: hidden; height: auto; width: auto;">_</pre>
<script>
// Some hax to make the slider position the same width as the displayed text.
/* Disabled because with constrained scren width it's annoying.
const width = document.getElementById('width').clientWidth;
const n = document.getElementById('n');
const fmt = document.getElementById("fmt");
n.max = n.clientWidth / width;
*/

let working = false;
let pending = false;
function range() {
	if (working) {
		pending = true;
		return;
	}
	working = true;
	const v = n.value;
	document.getElementById("nval").innerText = v;
	const sql = document.getElementById("sql").value;
	fetch('/fmt?n=' + v + '&sql=' + encodeURIComponent(sql))
	.then(resp => {
		working = false;
		resp.json().then(data => {
			fmt.innerText = data.map(d => d + ';').join('\n\n');
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
