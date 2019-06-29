package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode"

	"github.com/cockroachdb/cockroach/pkg/sql/parser"
	_ "github.com/cockroachdb/cockroach/pkg/sql/sem/builtins"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/kelseyhightower/envconfig"
	"github.com/pkg/errors"
	flag "github.com/spf13/pflag"
	"golang.org/x/crypto/acme/autocert"
)

type Specification struct {
	Addr     string
	Redir    string
	Autocert []string
	DirCache string
}

var (
	prettyCfg      = tree.DefaultPrettyCfg()
	flagExpanded   = flag.Bool("expanded", false, "use a verbose, expansive format")
	flagPrintWidth = flag.Int("print-width", 60, "line length where sqlfmt will try to wrap")
	flagUseSpaces  = flag.Bool("use-spaces", false, "indent with spaces instead of tabs")
	flagTabWidth   = flag.Int("tab-width", 4, "number of spaces per indentation level")
	flagStmts      = flag.StringArray("stmt", nil, "instead of reading from stdin, specify statements as arguments")
	flagHelp       = flag.BoolP("help", "h", false, "display help")
	flagVersion    = flag.BoolP("version", "v", false, "display version")
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	flag.Parse()
	if *flagHelp {
		flag.Usage()
		fmt.Printf(`

%s runs in one of two modes.

1) It takes in SQL statements from stdin or the --stmt arguments
and formats them to stdout. This mode is enabled if the webserver is
unconfigured.

2) It runs a webserver on a specified address. This is configured by
setting the SQLFMT_ADDR env variable to a bindable address (like ":8080"):

SQLFMT_ADDR=":8080" %[1]s
`, os.Args[0])
		return
	}
	if *flagVersion {
		fmt.Printf("sqlfmt %s\n", version)
		return
	}

	var spec Specification
	err := envconfig.Process("sqlfmt", &spec)
	if err != nil {
		log.Fatal(err.Error())
	}
	if spec.Addr != "" {
		serveHTTP(spec)
		return
	}

	if err := runCmd(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runCmd() error {
	if *flagPrintWidth < 1 {
		return errors.Errorf("line length must be > 0: %d", *flagPrintWidth)
	}
	if *flagTabWidth < 1 {
		return errors.Errorf("tab width must be > 0: %d", *flagTabWidth)
	}

	sl := *flagStmts
	if len(sl) == 0 {
		in, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		sl = append(sl, string(in))
	}

	cfg := tree.DefaultPrettyCfg()
	cfg.UseTabs = !*flagUseSpaces
	cfg.LineWidth = *flagPrintWidth
	cfg.TabWidth = *flagTabWidth
	if *flagExpanded {
		cfg.Simplify = false
		cfg.Align = tree.PrettyNoAlign
	} else {
		cfg.Simplify = false
		cfg.Align = tree.PrettyAlignAndDeindent
	}

	res, err := fmtsql(cfg, sl)
	if err != nil {
		return err
	}
	fmt.Println(res)
	return nil
}

var (
	ignoreComments = regexp.MustCompile(`^--.*\s*`)
)

func fmtsql(cfg tree.PrettyCfg, stmts []string) (string, error) {
	var prettied strings.Builder
	for _, stmt := range stmts {
		for len(stmt) > 0 {
			stmt = strings.TrimSpace(stmt)
			hasContent := false
			// Trim comments, preserving whitespace after them.
			for {
				found := ignoreComments.FindString(stmt)
				if found == "" {
					break
				}
				// Remove trailing whitespace but keep up to 2 newlines.
				prettied.WriteString(strings.TrimRightFunc(found, unicode.IsSpace))
				newlines := strings.Count(found, "\n")
				if newlines > 2 {
					newlines = 2
				}
				prettied.WriteString(strings.Repeat("\n", newlines))
				stmt = stmt[len(found):]
				hasContent = true
			}
			// Split by semicolons
			next := stmt
			if pos, _ := parser.SplitFirstStatement(stmt); pos > 0 {
				next = stmt[:pos]
				stmt = stmt[pos:]
			} else {
				stmt = ""
			}
			// This should only return 0 or 1 responses.
			allParsed, err := parser.Parse(next)
			if err != nil {
				return "", err
			}
			for _, parsed := range allParsed {
				prettied.WriteString(cfg.Pretty(parsed.AST))
				prettied.WriteString(";\n")
				hasContent = true
			}
			if hasContent {
				prettied.WriteString("\n")
			}
		}
	}

	return strings.TrimRightFunc(prettied.String(), unicode.IsSpace), nil
}

func serveHTTP(spec Specification) {
	fmt.Printf("SPEC: %#v\n", spec)
	base := template.Must(template.New("base").Parse(Base))
	index := template.Must(template.Must(base.Clone()).Parse(Index))
	about := template.Must(template.Must(base.Clone()).Parse(About))
	editor := template.Must(template.Must(base.Clone()).Parse(Editor))

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
	mux.HandleFunc("/editor", func(w http.ResponseWriter, r *http.Request) {
		if err := editor.Execute(w, nil); err != nil {
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
		m := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(spec.Autocert...),
			Cache:      autocert.DirCache(spec.DirCache),
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

func wrap(f func(http.ResponseWriter, *http.Request) fmtResponse) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res := f(w, r)
		if r.FormValue("json") == "" {
			w.Header().Add("Content-Type", "text/plain")
			w.Write([]byte(res.Data))
		} else {
			w.Header().Add("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(res); err != nil {
				log.Print(err)
			}
		}
	}
}

type fmtResponse struct {
	Data  string
	Error bool
}

var cache = struct {
	sync.RWMutex
	m map[string]fmtResponse
}{
	m: make(map[string]fmtResponse),
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

func Fmt(w http.ResponseWriter, r *http.Request) fmtResponse {
	cache.RLock()
	hit, ok := cache.m[r.URL.RawQuery]
	cache.RUnlock()
	if ok {
		return hit
	}

	res, err := fmtSQLRequest(r)
	response := fmtResponse{
		Data:  res,
		Error: err != nil,
	}
	if err != nil {
		response.Data = err.Error()
	}
	cache.Lock()
	if len(cache.m) > 10000 {
		for k := range cache.m {
			delete(cache.m, k)
		}
	}
	cache.m[r.URL.RawQuery] = response
	cache.Unlock()
	return response
}

func fmtSQLRequest(r *http.Request) (string, error) {
	sql := r.FormValue("sql")
	trimmed := strings.Join(strings.Fields(sql), " ")
	if len(trimmed) > 100 {
		trimmed = fmt.Sprintf("%s...", trimmed[:100])
	}

	n, err := strconv.Atoi(r.FormValue("n"))
	if err != nil {
		return "", err
	}
	log.Printf("fmt (sqln: %d, n: %d): %s", len(sql), n, trimmed)
	tabWidth, err := strconv.Atoi(r.FormValue("indent"))
	if err != nil {
		return "", err
	}
	expanded, err := parseBool(r.FormValue("expanded"))
	if err != nil {
		return "", err
	}
	spaces, err := parseBool(r.FormValue("spaces"))
	if err != nil {
		return "", err
	}

	pcfg := tree.DefaultPrettyCfg()
	pcfg.LineWidth = n
	pcfg.UseTabs = !spaces
	pcfg.TabWidth = tabWidth
	pcfg.Case = strings.ToUpper

	if expanded {
		pcfg.Simplify = false
		pcfg.Align = tree.PrettyNoAlign
	} else {
		pcfg.Simplify = true
		pcfg.Align = tree.PrettyAlignAndDeindent
	}
	return fmtsql(pcfg, []string{sql})
}

const (
	Base = `<!DOCTYPE html>
<html>
<head>
<meta http-equiv="Content-Type" content="text/html; charset=utf-8">
<link href="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAABmJLR0QA/wD/AP+gvaeTAAAACXBIWXMAAAsTAAALEwEAmpwYAAAAB3RJTUUH4gYRBwgDpCIYRAAAAB1pVFh0Q29tbWVudAAAAAAAQ3JlYXRlZCB3aXRoIEdJTVBkLmUHAAACiElEQVRYw+2XPWsyQRDHfX8Dq1iILyBBVDSVoFiLWogWJpAikkpiISJim0+gIgh+hBRBEBFCwEIURYQU6bS0CBYRJeALRBKCE3bhFu8xnt4ZnmsysLfc/2aOn3uzM6sAeDYBujw9PUE4HAaDwQBSqRSMRiNcX19Dp9PZClitVnB7ewsmkwlkMhme0T3SaS8WCMhgBKhWqyASiWgBTMGhUOhHP/QDOAGcnZ0Rx3w+D+/v7zAcDiEWi20FPzw8EN/z83NYLBZ4prTHx0f2AHK5nDgOBgPaw1wuR7u/uroivq1WC2vNZpNo0WiUPYDVaiWOTqcTGo0GrNfrH50tFgvxHY/HWHt9fSWazWZjD3B/fw9CoZAWgJKxUCjgz7FpKpWK+Hx+fmLt4+ODaGq1mj0AurTbbfB6vVsgaEWm0ylx3kxWapXQTGkSiYQbAGUvLy+QzWZBo9GQ4Hg8fvAKnJycHAdAWbfbJcFarZboZrOZMQccDgd7gFQqBV9fXzQRbS8qGBUbyi4vLxl3QSKRYA9AfetarQaz2Qze3t4gnU6TYLvdTpwrlQrRLy4utupAr9fjBrBroKQsl8vEGSUcStaffJPJ5M5KyFRlBYj65uYGUD1QKBS4F+h0OohEInh5/7XlcgmZTAb0ej3ZFaenpzCfz7kBHNPJUANyu92kCI1GI27d8BhDu8DlcsHd3R33dsz7eYBXgEOTZTOxgsEg7SV+v3+n794k5AKAav5kMsEaqohisfh3AA5YLjJKpRLWisXi3qLD9JwzgMfjwRq1Df87gFKphHq9jovXrwHsO5Ru6oFAAHdKn8/HDwA6MaEZnR94+QT9fh/Pz8/P/ACw+Q/wB/AHwAjAdzf8BhzwU5PzE5t1AAAAAElFTkSuQmCC" rel="icon" type="image/png">
<title>sequel  fumpt</title>
<style>
:root {
  --primary: #6200ee;
  --variant: #3700b3;
  --secondary: #03dac6;
  --secondary-variant: #018786;
  --background: #ffffff;
  --surface: #ffffff;
  --error: #b00020;
  --on-primary: #ffffff;
  --on-secondary: #000000;
  --on-background: #000000;
  --on-surface: #000000;
  --on-error: #ffffff;
  --dp00: #ffffff;
  --dp01: #f2f2f2;
  --dp02: #ededed;
  --dp03: #ebebeb;
  --dp04: #e8e8e8;
  --dp06: #e3e3e3;
  --dp08: #e0e0e0;
  --dp12: #dbdbdb;
  --dp16: #d9d9d9;
  --dp24: #d6d6d6;
  --emph-high: #212121;
  --emph-medium: #666666;
  --disabled: #9e9e9e;
}
@media (prefers-color-scheme: dark) {
  :root {
    --primary: #bb86fc;
    --variant: #3700b3;
    --secondary: #03dac6;
    --secondary-variant: #03dac6;
    --background: #121212;
    --surface: #121212;
    --error: #cf6679;
    --on-primary: #000000;
    --on-secondary: #000000;
    --on-background: #ffffff;
    --on-surface: #ffffff;
    --on-error: #000000;
    --dp00: #121212;
    --dp01: #1e1e1e;
    --dp02: #232323;
    --dp03: #252525;
    --dp04: #272727;
    --dp06: #2c2c2c;
    --dp08: #2e2e2e;
    --dp12: #333333;
    --dp16: #363636;
    --dp24: #383838;
    --emph-high: #e0e0e0;
    --emph-medium: #a0a0a0;
    --disabled: #6c6c6c;
  }
}
body {
  color: var(--emph-high);
  background-color: var(--background);
}
</style>
<style>
body {
	margin: 0;
	padding: 8px;
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
	color: var(--primary);
}
.full-width {
	margin: 10px -9999rem;
	padding: 0 9999rem;
	background: var(--dp04);
}
.jsonly {
	display: none;
}
textarea {
	color: var(--emph-high);
	background: var(--surface);
}
</style>
</head>
<body>
{{block "content" .}}{{end}}
</body>
</html>`

	Editor = `{{define "content"}}
<h1>editor configuration</h1>
sqlfmt is available as a <a href="https://github.com/mjibson/sqlfmt/releases/latest">standalone binary</a>. You can configure your editor to run it on .sql files on save, or over selected text.

<hr>
<a href="/">index</a>
<a href="/about">about</a>
{{end}}`

	About = `{{define "content"}}
<h1>about</h1>
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

<p>There is a box in which to paste or type SQL statements. Multiple statements are supported by separating them with a semicolon (<code>;</code>). The slider below the box controls the desired maximum line width in characters. Various options on the side control tab/indentation width, and the use of spaces or tabs.</p>

<p>There are two formatting modes. The default "compact" format, simplifies expressions and collapses clauses using alignment to clarify between clauses.
The "expanded" format avoids simplifying expressions and reveals the full structure of SQL queries.</p>

<h3>compact:</h3>
<pre>
SELECT a
  FROM t
 WHERE c
   AND b
    OR d
</pre>

<h3>expanded:</h3>
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

<h2>Background</h2>

sqlfmt was inspired by <a href="https://prettier.io/">prettier</a>. It is based on <a href="http://homepages.inf.ed.ac.uk/wadler/papers/prettier/prettier.pdf">a paper</a> describing a layout algorithm. A <a href="https://www.cockroachlabs.com/blog/sql-fmt-online-sql-formatter/">blog post</a> describes a bit more.

<hr>
<a href="/">index</a>
<a href="/editor">editor config</a>
<br>by <a href="https://twitter.com/mjibson">@mjibson</a>
<br>code: <a href="https://github.com/mjibson/sqlfmt">github.com/mjibson/sqlfmt</a>
{{end}}`

	Index = `{{define "content"}}
<h1>sequel fumpt</h1>
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
		<br><input type="checkbox" checked="0" onChange="range()" onInput="range()" name="spaces" id="spaces"><label for="spaces" title="use spaces instead of tabs">use spaces</label>
		<br><input type="checkbox" checked="1" onChange="range()" onInput="range()" name="expanded" id="expanded"><label for="expanded" title="use expanded mode">expanded</label>
		<span class="jsonly"><br><button type="button" onClick="resetVals()" id="reset">reset to defaults</button></span>
		<span class="jsonly"><br><button type="button" onClick="clearSQL()" id="clear">clear</button></span>
		<span class="jsonly"><br><input type="checkbox" onChange="autoPaste()" onInput="autoPaste()" name="paste" id="paste"><label for="paste" title="pastes from clipboard on load">auto paste</label></span>
	</div>
</div>

target line width: <span id="nval"></span>, actual width: <span id="actual_width"></span> (num bytes: <span id="actual_bytes"></span>)
<br><input type="submit" id="submitButton">
</form>

<span class="jsonly"><button id="copy">copy to clipboard</button> <a href="" id="share">share</a></span>

<div class="full-width">
	<pre id="fmt" style="padding: 5px 0; overflow-x: auto"></pre>
</div>

<a href="/editor">editor config</a>
<a href="/about">about</a>
<script>
const textCopy = document.getElementById('text-copy');
const actualWidth = document.getElementById('actual_width');
const actualBytes = document.getElementById('actual_bytes');
const n = document.getElementById('n');
const iw = document.getElementById('indent');
const expanded = document.getElementById('expanded');
const spaces = document.getElementById('spaces');
const fmt = document.getElementById('fmt');
const sqlEl = document.getElementById('sql');
const share = document.getElementById('share');
const reset = document.getElementById('reset');
const pasteEl = document.getElementById('paste');

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

function clearSQL() {
	sqlEl.value = '';
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
	const expVal = expanded.checked ? 1 : 0;
	localStorage.setItem('sql', sql);
	localStorage.setItem('n', v);
	localStorage.setItem('iw', viw);
	localStorage.setItem('expanded', expVal);
	localStorage.setItem('spaces', spVal);
	fmt.style["tab-size"] = viw;
	fmt.style["-moz-tab-size"] = viw;
	share.href = '/?n=' + v + '&indent=' + viw + '&spaces=' + spVal + '&expanded=' + expVal + '&sql=' + encodeURIComponent(b64EncodeUnicode(sql));
	fetch('/fmt?json=1&n=' + v + '&indent=' + viw + '&spaces=' + spVal + '&expanded=' + expVal + '&sql=' + encodeURIComponent(sql)).then(
		resp => {
			working = false;
			resp.json().then(data => {
				if (data.Error) {
					fmt.innerText = data.Data;
					actualWidth.innerText = '';
					actualBytes.innerText = '';
				} else {
					fmtText = data.Data
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

let search;
if (location.search) {
	search = new URLSearchParams(location.search);
}

function reloadVals() {
	// Load initial defaults from storage.
	let sql = localStorage.getItem('sql');
	let nVal = localStorage.getItem('n');
	let iwVal = localStorage.getItem('iw');
	let expVal = localStorage.getItem('expanded');
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
	if (expVal === null) { expVal = 1; }
	if (spVal === null) { spVal = 0; }

	// Override any value from the URL.
	if (search) {
		if (search.has('sql'))      { sql = b64DecodeUnicode(search.get('sql'));	}
		if (search.has('n'))        { nVal = search.get('n'); }
		if (search.has('indent'))   { iwVal = search.get('indent'); }
		if (search.has('expanded')) { simVal = search.get('expanded'); }
		if (search.has('spaces'))   { spVal = search.get('spaces'); }
	}

	// Populate the form.
	sqlEl.value = sql;
	n.value = nVal;
	iw.value = iwVal;
	expanded.checked = !!expVal;
	spaces.checked = !!spVal;
}

reloadVals();

pasteEl.checked = localStorage.getItem('paste') === '1';
function autoPaste() {
	const p = pasteEl.checked ? 1 : 0;
	localStorage.setItem('paste', p);
	if (p) {
		navigator.clipboard.readText().then(clipText => {
			sqlEl.value = clipText;
			range();
		});
	}
}

if (!search || !search.has('sql')) {
	autoPaste();
}

(() => {
	if (location.search) {
		const clearSearch = () => {
			window.history.replaceState(null, '', '/');
			sqlEl.onkeydown = null;
			n.oninput = n.onchange = range;
			iw.oninput = iw.onchange = range;
			expanded.oninput = expanded.onchange = range;
			spaces.oninput = spaces.oninput = range;
			reset.onclick = resetVals;
		};
		sqlEl.onkeydown = clearSearch;
		n.oninput = n.onchange = () => {
			clearSearch();
			range();
		};
		iw.oninput = iw.onchange = n.oninput;
		expanded.oninput = expanded.onchange = n.oninput;
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
