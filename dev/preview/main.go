// Command preview serves the extension locally with no Twitch and no AudD
// account: a stand-in for the Twitch extension helper, mocks of the extension
// backend (with Sign in with AudD) and twitch.php, and a gallery of every view
// and state.
//
//	cd dev && go run ./preview            # http://localhost:8787/
//	go run ./preview -addr :9000 -before /path/to/old/frontend
//
// Pages take these URL parameters (see the gallery for examples):
//
//	theme=dark|light        Twitch theme (onContext)
//	channel=<scenario>      which songs the mock backend has (typical, just,
//	                        recent, stale, one, full, long, nocover, youtube,
//	                        legacy, empty, empty-prod, down, slow, flaky)
//	every=<seconds>         push a new song through PubSub this often
//	role=viewer|broadcaster|moderator
//	                        the role in the Twitch JWT
//	account=<scenario>      the AudD account twitch.php reports (none, active,
//	                        expiring, expired, unpaid, error-<code>, stuck,
//	                        down, slow, slowlink)
//	signin=nobackend|down|busy|noresult
//	                        make "Sign in with AudD" unavailable (see signin.go)
//	signin=linked           the backend links the account itself when the
//	                        sign-in finishes, so twitch.php isn't asked
//	platform=mobile         the Twitch mobile app
//	ch=<channel>&history&background&shadow&black-font
//	                        the hosted widget (panel.html only)
package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

//go:embed static
var staticFiles embed.FS

// The CSP Twitch applies to extension pages, with the allowlists pointing at
// this server (everything is same-origin here).
const twitchLikeCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; media-src 'self'; connect-src 'self'; font-src 'self'; " +
	"frame-src 'none'; object-src 'none'; base-uri 'self'"

const helperURL = "https://extension-files.twitch.tv/helper/v1/twitch-ext.min.js"

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "address to listen on")
	frontend := flag.String("frontend", "", "the extension's frontend folder (default: ../frontend next to this module)")
	before := flag.String("before", "", "optional: an older frontend folder to serve at /before/ for comparison")
	flag.Parse()

	if *frontend == "" {
		*frontend = findFrontend()
	}
	h := newHarness()
	mux := http.NewServeMux()
	h.routes(mux)
	mux.Handle("/ext/", http.StripPrefix("/ext/", extensionFiles(*frontend, false)))
	if *before != "" {
		mux.Handle("/before/", http.StripPrefix("/before/", extensionFiles(*before, true)))
	}
	static, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/dev/static/", http.StripPrefix("/dev/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, _ := staticFiles.ReadFile("static/gallery.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})

	srv := &http.Server{Addr: *addr, Handler: logRequests(mux), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("AudD extension preview on http://%s/ (frontend: %s)", *addr, *frontend)
	log.Fatal(srv.ListenAndServe())
}

func findFrontend() string {
	for _, p := range []string{"../frontend", "frontend", "../../frontend"} {
		if st, err := os.Stat(filepath.Join(p, "panel.html")); err == nil && !st.IsDir() {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	log.Fatal("can't find the frontend folder; pass -frontend")
	return ""
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("PREVIEW_LOG") != "" {
			log.Printf("%s %s", r.Method, r.URL.RequestURI())
		}
		next.ServeHTTP(w, r)
	})
}

// extensionFiles serves a frontend folder the way Twitch would host it, with
// the helper script swapped for the stand-in and endpoints pointed here.
// legacy rewrites the hard-coded URLs of the pre-0.1.0 frontend instead.
func extensionFiles(dir string, legacy bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean("/" + r.URL.Path)
		if name == "/" {
			name = "/panel.html"
		}
		if !legacy && name == "/js/endpoints.js" {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Write([]byte(devEndpoints(r.URL.Query())))
			return
		}
		// os.DirFS only opens paths inside dir.
		file := strings.TrimPrefix(name, "/")
		b, err := fs.ReadFile(os.DirFS(dir), file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		ctype := mime.TypeByExtension(filepath.Ext(file))
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-store")
		switch filepath.Ext(file) {
		case ".html":
			csp := twitchLikeCSP
			if legacy {
				// The old frontend loads Roboto from Google Fonts, which
				// Twitch allows.
				csp = strings.Replace(csp, "style-src 'self'", "style-src 'self' https://fonts.googleapis.com", 1)
				csp = strings.Replace(csp, "font-src 'self'", "font-src 'self' https://fonts.gstatic.com", 1)
			}
			w.Header().Set("Content-Security-Policy", csp)
			// Pass the page's query on to endpoints.js so a page can ask
			// for a failing sign-in step.
			q := ""
			if r.URL.RawQuery != "" {
				q = "?" + r.URL.RawQuery
			}
			b = bytes.Replace(b, []byte(`<script src="`+helperURL+`"></script>`),
				[]byte(`<script src="/dev/static/twitch-stub.js"></script>`), 1)
			b = bytes.Replace(b, []byte(helperURL), []byte("/dev/static/twitch-stub.js"), 1)
			b = bytes.Replace(b, []byte(`src="js/endpoints.js"`), []byte(`src="js/endpoints.js`+htmlAttr(q)+`"`), 1)
		case ".js":
			if legacy {
				b = bytes.ReplaceAll(b, []byte("https://api.audd.io/lastSong/"), []byte("/mock/lastSong/"))
				b = bytes.ReplaceAll(b, []byte("https://dashboard.audd.io/api/twitch.php"), []byte("/mock/twitch.php"))
			}
		}
		w.Write(b)
	})
}

func htmlAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// devEndpoints is js/endpoints.js with every address on this server.
func devEndpoints(q map[string][]string) string {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	lastSong := "/mock/lastSong/"
	switch get("signin") {
	case "nobackend":
		lastSong = "/mock/lastSong-old/"
	case "down", "busy", "noresult", "linked":
		lastSong = "/mock/lastSong-" + get("signin") + "/"
	}
	return fmt.Sprintf(`// Development endpoints served by dev/preview (not the production file).
window.AUDD_ENDPOINTS = {
  lastSong: %q,
  twitchPhp: '/mock/twitch.php',
  payments: location.origin + '/mock/payments/',
  dashboard: location.origin + '/mock/dashboard/',
  site: 'https://audd.io/',
  widget: location.origin + '/ext/panel.html',
  songLinkBase: location.origin + '/mock/lis.tn/',
  youtubeThumbBase: location.origin + '/mock/ytimg/'
};
`, lastSong)
}
