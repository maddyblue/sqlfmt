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
			fmt.Printf("HTTP listen on: http://%s/\n", spec.Addr)
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
		return []string{"error", err.Error()}
	}
	tabWidth, err := strconv.Atoi(r.FormValue("indent"))
	if err != nil {
		return []string{"error", err.Error()}
	}
	simplify, err := strconv.ParseBool(r.FormValue("simplify"))
	if err != nil {
		return []string{"error", err.Error()}
	}
	align, err := strconv.ParseBool(r.FormValue("align"))
	if err != nil {
		return []string{"error", err.Error()}
	}
	spaces, err := strconv.ParseBool(r.FormValue("spaces"))
	if err != nil {
		return []string{"error", err.Error()}
	}
	sl, err := parser.Parse(r.FormValue("sql"))
	if err != nil {
		return []string{"error", err.Error()}
	}

	pcfg := tree.DefaultPrettyCfg()
	pcfg.LineWidth = n
	pcfg.UseTabs = !spaces
	pcfg.TabWidth = tabWidth
	pcfg.Simplify = simplify
	pcfg.Align = align

	res := make([]string, len(sl))
	for i, s := range sl {
		res[i] = pcfg.Pretty(s)
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
	margin: 0;
	padding: 8px;
	color: #303030;
	font-size: 1rem;
	font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
}
input {
	margin: 0;
	padding: 0;
}
html, body {
	overflow-x: hidden;
}
a {
	color: #268bd2;
}
.full-width {
	margin: 10px -9999rem;
	padding: 0 9999rem;
	background: rgba(0, 0, 0, 0.05);
}
</style>
</head>
<body>
<h1>sequel  fumpt</h1>
<p>Type some SQL. Move the slider to set output width.</p>

<div style="display: flex; flex-wrap: wrap">
	<div style="flex: 1; margin-right: 4px">
		<textarea id="sql" style="box-sizing: border-box; width: 100%; height: 150px" onChange="range()" onInput="range()">SELECT count(*) count, winner, counter * 60 * 5 as counter FROM (SELECT winner, round((length / 60) / 5) as counter FROM players WHERE build = $1 AND (hero = $2 OR region = $3)) GROUP BY winner, counter</textarea>
		<input type="range" min="1" max="200" step="1" name="n" value="40" onChange="range()" onInput="range()" id="n" style="width: 100%">
	</div>
	<div style="width: 150px">
		<h4 style="margin: 0">options:</h4>
		<label title="tab/indent width" for="iw">tab width</label>
		<input type="number" min="1" max="16" step="1" name="iw" value="4" onChange="range()" onInput="range()" id="iw">
		<br><input type="checkbox" checked="1" onChange="range()" onInput="range()" id="simplify"><label for="simplify" title="simplify parentheses">simplify</label>
		<br><input type="checkbox" checked="0" onChange="range()" onInput="range()" id="spaces"><label for="spaces" title="use tabs instead of spaces">use tabs</label>
		<br><input type="checkbox" checked="0" onChange="range()" onInput="range()" id="align"><label for="align" title="align mode">align mode</label>
	</div>
</div>

target line width: <span id="nval"></span>, actual width: <span id="actual_width"></span> (num bytes: <span id="actual_bytes"></span>)
<br><button id="copy">copy to clipboard</button> <a href="" id="share">share</a>

<div class="full-width">
	<pre id="fmt" style="padding: 5px 0; overflow-x: auto"></pre>
</div>

by <a href="https://twitter.com/mjibson">@mjibson</a> <a href="https://github.com/mjibson/sqlfmt">github.com/mjibson/sqlfmt</a>
<script>
const textCopy = document.getElementById('text-copy');
const actualWidth = document.getElementById('actual_width');
const actualBytes = document.getElementById('actual_bytes');
const n = document.getElementById('n');
const iw = document.getElementById('iw');
const simplify = document.getElementById('simplify');
const align = document.getElementById('align');
const spaces = document.getElementById('spaces');
const fmt = document.getElementById('fmt');
const sqlEl = document.getElementById('sql');
const share = document.getElementById('share');
let fmtText;

document.getElementById('copy').addEventListener('click', ev => {
	copyTextToClipboard(fmtText);
});

// https://stackoverflow.com/questions/400212/how-do-i-copy-to-the-clipboard-in-javascript
function copyTextToClipboard(text) {
	const textArea = document.createElement('textarea');

	// Place in top-left corner of screen regardless of scroll position.
	textArea.style.position = 'fixed';
	textArea.style.top = 0;
	textArea.style.left = 0;

	// Ensure it has a small width and height. Setting to 1px / 1em
	// doesn't work as this gives a negative w/h on some browsers.
	textArea.style.width = '2em';
	textArea.style.height = '2em';

	// We don't need padding, reducing the size if it does flash render.
	textArea.style.padding = 0;

	// Clean up any borders.
	textArea.style.border = 'none';
	textArea.style.outline = 'none';
	textArea.style.boxShadow = 'none';

	// Avoid flash of white box if rendered for any reason.
	textArea.style.background = 'transparent';

	textArea.value = text;

	document.body.appendChild(textArea);
	textArea.focus();
	textArea.select();

	try {
		document.execCommand('copy');
	} catch (err) {
		console.log(err);
	}

	document.body.removeChild(textArea);
}

let working = false;
let pending = false;
function range() {
	if (working) {
		pending = true;
		return;
	}
	working = true;
	const v = n.value;
	document.getElementById('nval').innerText = v;
	const viw = iw.value;
	const sql = sqlEl.value;
	const spVal = spaces.checked ? 0 : 1;
	const simVal = simplify.checked ? 1 : 0;
	const alVal = align.checked ? 1 : 0;
	localStorage.setItem('sql', sql);
	localStorage.setItem('n', v);
	localStorage.setItem('iw', viw);
	localStorage.setItem('simplify', simVal);
	localStorage.setItem('align', alVal);
	localStorage.setItem('spaces', spVal);
	fmt.style["tab-size"] = viw;
	fmt.style["-moz-tab-size"] = viw;
	share.href = '/?n=' + v + '&indent=' + viw + '&spaces=' + spVal + '&simplify=' + simVal + '&align=' + alVal + '&sql=' + encodeURIComponent(b64EncodeUnicode(sql));
	fetch('/fmt?n=' + v + '&indent=' + viw + '&spaces=' + spVal + '&simplify=' + simVal + '&align=' + alVal + '&sql=' + encodeURIComponent(sql)).then(
		resp => {
			working = false;
			resp.json().then(data => {
				if (data.length === 2 && data[0].includes('error')) {
					fmt.innerText = data[1];
					actualWidth.innerText = '';
					actualBytes.innerText = '';
				} else {
					fmtText = data.map(d => d + ';').join('\n\n');
					tabSpaces = " ".repeat(viw);
					actualWidth.innerText = Math.max(...fmtText.split('\n').map(v => v.replace(/\t/g, tabSpaces).length));
					actualBytes.innerText = fmtText.length;
					hLine = "--";
					if (v > 2) {
						hLine = hLine + "-".repeat(v-2);
					}
					fmt.innerText = hLine + "\n\n" + fmtText;
				}
				if (pending) {
					range();
					pending = false;
				}
			}, console.log);
		},
		d => {
			working = false;
			console.log(d);
		}
	);
}

function b64EncodeUnicode(str) {
	// first we use encodeURIComponent to get percent-encoded UTF-8,
	// then we convert the percent encodings into raw bytes which
	// can be fed into btoa.
	return btoa(
		encodeURIComponent(str).replace(/%([0-9A-F]{2})/g, function toSolidBytes(
			match,
			p1
		) {
			return String.fromCharCode('0x' + p1);
		})
	);
}

function b64DecodeUnicode(str) {
	// Going backwards: from bytestream, to percent-encoding, to original string.
	return decodeURIComponent(
		atob(str)
			.split('')
			.map(function(c) {
				return '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2);
			})
			.join('')
	);
}

(() => {
	const search = new URLSearchParams(location.search);
	let sql = localStorage.getItem('sql') || null;
	let nVal = localStorage.getItem('n');
	let iwVal = localStorage.getItem('iw');
	let simVal = localStorage.getItem('simplify');
	let alVal = localStorage.getItem('align');
	let spVal = localStorage.getItem('spaces');
	if (location.search) {
		sql = b64DecodeUnicode(search.get('sql'));
		nVal = search.get('n');
		const clearSearch = () => {
			window.history.replaceState(null, '', '/');
			sqlEl.onkeydown = null;
			n.oninput = range;
			n.onchange = range;
		};
		sqlEl.onkeydown = clearSearch;
		n.oninput = () => {
			clearSearch()
			range();
		};
		n.onchange = n.oninput;
	}
	if (sql !== null) {
		sqlEl.value = sql;
	}
	if (nVal !== null && nVal > 0) {
		n.value = nVal;
	}
	if (iwVal !== null && iwVal > 0) {
		iw.value = iwVal;
	}
	if (simVal !== null) {
		simplify.checked = (simVal > 0);
	}
	if (alVal !== null) {
		align.checked = (alVal > 0);
	}
	if (spVal !== null) {
		spaces.checked = !(spVal > 0);
	}
})();

range();
</script>
</body>
</html>`
