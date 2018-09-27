package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

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
	DBCache  string
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

	base := template.Must(template.New("base").Parse(Base))
	index := template.Must(template.Must(base.Clone()).Parse(Index))
	about := template.Must(template.Must(base.Clone()).Parse(About))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := index.Execute(w, nil); err != nil {
			fmt.Println(err)
			http.Error(w, err.Error(), 500)
		}
	})
	mux.HandleFunc("/about", func(w http.ResponseWriter, r *http.Request) {
		if err := about.Execute(w, nil); err != nil {
			fmt.Println(err)
			http.Error(w, err.Error(), 500)
		}
	})
	mux.HandleFunc("/fmt", wrap(Fmt))
	srv := &http.Server{
		Addr:           spec.Addr,
		Handler:        mux,
		MaxHeaderBytes: (1 << 10) * 20, // 20KB
	}

	if len(spec.Autocert) > 0 {
		db, err := sql.Open("postgres", spec.DBCache)
		if err != nil {
			log.Fatal(err.Error())
		}
		defer db.Close()

		m := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(spec.Autocert...),
			Cache:      dbCache{db},
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
	signal.Notify(c, os.Interrupt, os.Kill, os.Signal(syscall.SIGHUP), os.Signal(syscall.SIGTERM))
	sig := <-c
	fmt.Println("closing server: got signal", sig)
	srv.Close()
	fmt.Println("closed server")
}

const autocertPrefix = "autocert-"

type dbCache struct {
	*sql.DB
}

func (db dbCache) Get(ctx context.Context, key string) ([]byte, error) {
	var data []byte
	if err := db.QueryRowContext(ctx, "SELECT s FROM config WHERE key = $1", autocertPrefix+key).Scan(&data); err == sql.ErrNoRows {
		return nil, autocert.ErrCacheMiss
	} else if err != nil {
		return nil, err
	}
	return data, nil
}

func (db dbCache) Put(ctx context.Context, key string, data []byte) error {
	_, err := db.ExecContext(ctx, "UPSERT INTO config (key, s) VALUES ($1, $2)", autocertPrefix+key, data)
	return err
}

func (db dbCache) Delete(ctx context.Context, key string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM config WHERE key = $1", autocertPrefix+key)
	return err
}

func wrap(f func(http.ResponseWriter, *http.Request) []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res := f(w, r)
		if r.FormValue("json") == "" {
			w.Header().Add("Content-Type", "text/plain")
			fmt.Fprintln(w, strings.Join(res, ";\n"))
		} else {
			w.Header().Add("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(res); err != nil {
				log.Print(err)
			}
		}
	}
}

var cache = struct {
	sync.RWMutex
	m map[string][]string
}{
	m: make(map[string][]string),
}

func parseBool(val string) (bool, error) {
	switch val {
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return strconv.ParseBool(val)
	}
}

func Fmt(w http.ResponseWriter, r *http.Request) []string {
	cache.RLock()
	hit, ok := cache.m[r.URL.RawQuery]
	cache.RUnlock()
	if ok {
		return hit
	}

	res := fmtsql(r)
	cache.Lock()
	if len(cache.m) > 10000 {
		for k := range cache.m {
			delete(cache.m, k)
		}
	}
	cache.m[r.URL.RawQuery] = res
	cache.Unlock()
	return res
}

func fmtsql(r *http.Request) []string {
	sql := r.FormValue("sql")
	trimmed := strings.Join(strings.Fields(sql), " ")
	if len(trimmed) > 100 {
		trimmed = trimmed[:100]
	}

	n, err := strconv.Atoi(r.FormValue("n"))
	if err != nil {
		return []string{"error", err.Error()}
	}
	log.Printf("fmt (sqln: %d, n: %d): %s", len(sql), n, trimmed)
	tabWidth, err := strconv.Atoi(r.FormValue("indent"))
	if err != nil {
		return []string{"error", err.Error()}
	}
	simplify, err := parseBool(r.FormValue("simplify"))
	if err != nil {
		return []string{"error", err.Error()}
	}
	align, err := strconv.Atoi(r.FormValue("align"))
	if err != nil {
		return []string{"error", err.Error()}
	}
	spaces, err := parseBool(r.FormValue("spaces"))
	if err != nil {
		return []string{"error", err.Error()}
	}
	sl, err := parser.Parse(sql)
	if err != nil {
		return []string{"error", err.Error()}
	}

	pcfg := tree.DefaultPrettyCfg()
	pcfg.LineWidth = n
	pcfg.UseTabs = !spaces
	pcfg.TabWidth = tabWidth
	pcfg.Simplify = simplify
	pcfg.Align = tree.PrettyAlignMode(align)

	res := make([]string, len(sl))
	for i, s := range sl {
		res[i] = pcfg.Pretty(s)
	}
	return res
}

const (
	Base = `<!DOCTYPE html>
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
.jsonly {
	display: none;
}
</style>
</head>
<body>
<h1>sequel  fumpt</h1>
{{block "content" .}}{{end}}
</body>
</html>`

	About = `{{define "content"}}
<p>
sqlfmt is an online SQL formatter.
It is <a href="https://youtu.be/ytEkHepK08c?t=1m38s">pronounced</a> sequel <a href="https://twitter.com/rob_pike/status/1034957357854257152">fumpt</a>.
Its purpose is to beautifully format SQL statements.
</p>

<h2>Features</h2>

<ul>
	<li>Understands the PostgreSQL dialect (and any CockroachDB extensions).</li>
	<li>Attempts to use available horizontal space in the best way possible.</li>
	<li>Always maintains visual alignment regardless of your editor configuration to use spaces or tabs at any tab width.</li>
</ul>

<h2>Usage</h2>

<p>There is a box in which to paste or type SQL statements. Multiple statements are supported by separating them with a semicolon (<code>;</code>). The slider below the box controls the desired maximum line width in characters. Various options on the side control tab/indentation width, the use of spaces or tabs, simplification, and alignment modes. Simplification causes the formatter to remove unneeded parentheses and words when the meaning will be the same without them.</p>

<p>There are four alignment modes. The default, <code>no</code>, uses left alignment. <code>partial</code> right aligns keywords at the width of the longest keyword at the beginning of all lines immediately below. <code>full</code> is the same as <code>partial</code> but the keywords <code>AND</code> and <code>OR</code> are deindented, in a style similar to the sqlite tests. <code>other</code> is like <code>partial</code> but instead of deindenting <code>AND</code> and <code>OR</code>, their arguments are instead indented.</p>

<h3>no:</h3>
<pre>
SELECT
    a
FROM
    t
WHERE
    c
    AND b
    OR d
</pre>

<h3>partial:</h3>
<pre>
SELECT a
  FROM t
 WHERE c
       AND b
       OR d
</pre>

<h3>full:</h3>
<pre>
SELECT a
  FROM t
 WHERE c
   AND b
    OR d
</pre>

<h3>other:</h3>
<pre>
SELECT a
  FROM t
 WHERE    c
          AND
            b
       OR d
</pre>

<hr>
<a href="/">index</a>
<br>by <a href="https://twitter.com/mjibson">@mjibson</a>
<br>code: <a href="https://github.com/mjibson/sqlfmt">github.com/mjibson/sqlfmt</a>
{{end}}`

	Index = `{{define "content"}}
<p>Type some SQL. Move the slider to set output width.</p>

<form name="theform" method="get" action="/fmt">
<div style="display: flex; flex-wrap: wrap">
	<div style="flex: 1; margin-right: 4px">
		<textarea id="sql" name="sql" style="box-sizing: border-box; width: 100%; height: 200px" onChange="range()" onInput="range()"></textarea>
		<input type="range" min="1" max="200" step="1" name="n" value="40" onChange="range()" onInput="range()" id="n" style="width: 100%">
	</div>
	<div style="width: 150px">
		<h4 style="margin: 0">options:</h4>
		<label title="tab/indent width" for="iw">tab width</label>
		<input type="number" min="1" max="16" step="1" name="indent" value="4" onChange="range()" onInput="range()" id="indent">
		<br><input type="checkbox" checked="1" onChange="range()" onInput="range()" name="simplify" id="simplify"><label for="simplify" title="simplify parentheses">simplify</label>
		<br><input type="checkbox" checked="0" onChange="range()" onInput="range()" name="spaces" id="spaces"><label for="spaces" title="use spaces instead of tabs">use spaces</label>
		<br>alignment mode:
		<br><input type="radio" name="align" value="0" onChange="range()" onInput="range()" id="align1" checked><label for="align1">no</label>
		<input type="radio" name="align" value="2" onChange="range()" onInput="range()" id="align2"><label for="align2">full</label>
		<br><input type="radio" name="align" value="1" onChange="range()" onInput="range()" id="align3"><label for="align3">partial</label>
		<input type="radio" name="align" value="3" onChange="range()" onInput="range()" id="align4"><label for="align4">other</label>
		<span class="jsonly"><br><button type="button" onClick="resetVals()" id="reset">reset to defaults</button></span>
	</div>
</div>

target line width: <span id="nval"></span>, actual width: <span id="actual_width"></span> (num bytes: <span id="actual_bytes"></span>)
<br><input type="submit" id="submitButton">
</form>

<span class="jsonly"><button id="copy">copy to clipboard</button> <a href="" id="share">share</a></span>

<div class="full-width">
	<pre id="fmt" style="padding: 5px 0; overflow-x: auto"></pre>
</div>

<a href="/about">about</a>
<script>
const textCopy = document.getElementById('text-copy');
const actualWidth = document.getElementById('actual_width');
const actualBytes = document.getElementById('actual_bytes');
const n = document.getElementById('n');
const iw = document.getElementById('indent');
const simplify = document.getElementById('simplify');
const align = document.theform.align;
const spaces = document.getElementById('spaces');
const fmt = document.getElementById('fmt');
const sqlEl = document.getElementById('sql');
const share = document.getElementById('share');
const reset = document.getElementById('reset');

document.getElementById('submitButton').style.display = 'none';
Object.values(document.getElementsByClassName('jsonly')).forEach(v => v.style.display = 'inline');

let fmtText;

document.getElementById('copy').addEventListener('click', ev => {
	copyTextToClipboard(fmtText);
});

function resetVals() {
	localStorage.clear();
	reloadVals();
	range();
}

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
	const spVal = spaces.checked ? 1 : 0;
	const simVal = simplify.checked ? 1 : 0;
	const alVal = align.value;
	localStorage.setItem('sql', sql);
	localStorage.setItem('n', v);
	localStorage.setItem('iw', viw);
	localStorage.setItem('simplify', simVal);
	localStorage.setItem('align', alVal);
	localStorage.setItem('spaces', spVal);
	fmt.style["tab-size"] = viw;
	fmt.style["-moz-tab-size"] = viw;
	share.href = '/?n=' + v + '&indent=' + viw + '&spaces=' + spVal + '&simplify=' + simVal + '&align=' + alVal + '&sql=' + encodeURIComponent(b64EncodeUnicode(sql));
	fetch('/fmt?json=1&n=' + v + '&indent=' + viw + '&spaces=' + spVal + '&simplify=' + simVal + '&align=' + alVal + '&sql=' + encodeURIComponent(sql)).then(
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

function reloadVals() {
	// Load initial defaults from storage.
	let sql = localStorage.getItem('sql');
	let nVal = localStorage.getItem('n');
	let iwVal = localStorage.getItem('iw');
	let simVal = localStorage.getItem('simplify');
	let alVal = localStorage.getItem('align');
	let spVal = localStorage.getItem('spaces');

	// Load predefined defaults, for each value that didn't have a default in storage.
	if (sql === null) {
		sql = "SELECT count(*) count, winner, counter * (60 * 5) as counter FROM (SELECT winner, round(length / (60 * 5)) as counter FROM players WHERE build = $1 AND (hero = $2 OR region = $3)) GROUP BY winner, counter;\n"+
				"INSERT INTO players(build, hero, region, winner, length) VALUES ($1, $2, $3, $4, $5);\n"+
				"INSERT INTO players SELECT players_copy ORDER BY length;\n"+
				"UPDATE players SET count = 0 WHERE build = $1 AND (hero = $2 OR region = $3) LIMIT 1;"
	}
	if (nVal === null) { nVal = 60; }
	if (iwVal === null) { iwVal = 4; }
	if (simVal === null) { simVal = 1; }
	if (alVal === null) { alVal = 0; }
	if (spVal === null) { spVal = 0; }

	// Override any value from the URL.
	if (location.search) {
		const search = new URLSearchParams(location.search);
		if (search.has('sql'))      { sql = b64DecodeUnicode(search.get('sql'));	}
		if (search.has('n'))        { nVal = search.get('n'); }
		if (search.has('indent'))   { iwVal = search.get('indent'); }
		if (search.has('align'))    { alVal = search.get('align'); }
		if (search.has('simplify')) { simVal = search.get('simplify'); }
		if (search.has('spaces'))   { spVal = search.get('spaces'); }
	}

	// Populate the form.
	sqlEl.value = sql;
	n.value = nVal;
	iw.value = iwVal;
	simplify.checked = !!simVal;
	align.value = alVal;
	spaces.checked = !!spVal;
}

reloadVals();

(() => {
	if (location.search) {
		const clearSearch = () => {
			window.history.replaceState(null, '', '/');
			sqlEl.onkeydown = null;
			n.oninput = n.onchange = range;
			iw.oninput = iw.onchange = range;
			simplify.oninput = simplify.onchange = range;
			align.oninput = simplify.oninput = range;
			spaces.oninput = spaces.oninput = range;
			reset.onclick = resetVals;
		};
		sqlEl.onkeydown = clearSearch;
		n.oninput = n.onchange = () => {
			clearSearch();
			range();
		};
		iw.oninput = iw.onchange = n.oninput;
		simplify.oninput = simplify.onchange = n.oninput;
		align.oninput = simplify.oninput = n.oninput;
		spaces.oninput = spaces.oninput = n.oninput;
		reset.onclick = () => {
			clearSearch();
			resetVals();
		};
	}
})();

range();
</script>
{{end}}`
)
