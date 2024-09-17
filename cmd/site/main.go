package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	ht "html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aohorodnyk/mimeheader"
	"github.com/bcmk/siren/lib"
	"github.com/bcmk/siren/sitelib"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/tdewolff/minify/v2"
	hmin "github.com/tdewolff/minify/v2/html"

	_ "github.com/mattn/go-sqlite3"
)

type server struct {
	cfg          sitelib.Config
	enabledPacks []sitelib.Pack
	db           *sql.DB
	packs        []sitelib.Pack

	enIndexTemplate    *ht.Template
	ruIndexTemplate    *ht.Template
	enStreamerTemplate *ht.Template
	ruStreamerTemplate *ht.Template
	enChicTemplate     *ht.Template
	ruChicTemplate     *ht.Template
	enPackTemplate     *ht.Template
	ruPackTemplate     *ht.Template
	enCodeTemplate     *ht.Template
	ruCodeTemplate     *ht.Template

	css string
}

type likeForPack struct {
	Pack string `yaml:"pack"`
	Like bool   `yaml:"like"`
}

var funcMap = ht.FuncMap{
	"inc": func(i int) int {
		return i + 1
	},
	"map": func(values ...interface{}) (map[string]interface{}, error) {
		if len(values)%2 != 0 {
			return nil, errors.New("invalid map call")
		}
		dict := make(map[string]interface{}, len(values)/2)
		for i := 0; i < len(values); i += 2 {
			key, ok := values[i].(string)
			if !ok {
				return nil, errors.New("map keys must be strings")
			}
			dict[key] = values[i+1]
		}
		return dict, nil
	},
	"enhance": func(m1, m2 map[string]interface{}) map[string]interface{} {
		dict := map[string]interface{}{}
		for k, v := range m1 {
			dict[k] = v
		}
		for k, v := range m2 {
			dict[k] = v
		}
		return dict
	},
	"raw_html": func(h string) ht.HTML {
		return ht.HTML(h)
	},
	"mul_div": func(x, y, z int) int {
		return x * y / z
	},
	"add": func(x, y int) int {
		return x + y
	},
	"sub": func(x, y int) int {
		return x - y
	},
	"div": func(x, y int) int {
		return x / y
	},
	"trimPrefix": func(s, prefix string) string {
		return strings.TrimPrefix(prefix, s)
	},
	"versioned": func(pack *sitelib.Pack, name string) string {
		if pack.Version < 2 {
			return name
		}
		return name + ".v" + strconv.Itoa(pack.Version)
	},
}

var packParams = []string{
	"siren",
	"fanclub",
	"instagram",
	"twitter",
	"onlyfans",
	"amazon",
	"lovense",
	"gift",
	"pornhub",
	"dmca",
	"allmylinks",
	"onemylink",
	"linktree",
	"fancentro",
	"frisk",
	"fansly",
	"avn",
	"mail",
	"snapchat",
	"telegram",
	"whatsapp",
	"youtube",
	"tiktok",
	"reddit",
	"twitch",
	"discord",
	"size",
}

var chaturbateModelRegex = regexp.MustCompile(`^(?:https?://)?(?:www\.|ar\.|de\.|el\.|en\.|es\.|fr\.|hi\.|it\.|ja\.|ko\.|nl\.|pt\.|ru\.|tr\.|zh\.|m\.)?chaturbate\.com(?:/p|/b)?/([A-Za-z0-9\-_@]+)/?(?:\?.*)?$|^([A-Za-z0-9\-_@]+)$`)

func linf(format string, v ...interface{}) { log.Printf("[INFO] "+format, v...) }
func ldbg(format string, v ...interface{}) { log.Printf("[DBG] "+format, v...) }

var checkErr = lib.CheckErr

func notFoundError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusNotFound)
	_, _ = fmt.Fprint(w, "404 not found")
}

func parseHTMLTemplate(filenames ...string) *ht.Template {
	var relative []string
	for _, f := range filenames {
		relative = append(relative, "pages/"+f)
	}
	t, err := ht.New(filepath.Base(filenames[0])).Funcs(funcMap).ParseFiles(relative...)
	checkErr(err)
	return t
}

func langs(url url.URL, baseDomain string, ls map[string]string) map[string]ht.URL {
	res := map[string]ht.URL{}
	port := url.Port()
	if port != "" {
		port = ":" + port
	}
	for l, pref := range ls {
		url.Host = pref + baseDomain + port
		res[l] = ht.URL(url.String())
	}
	return res
}

func getLangBaseURL(url url.URL, baseDomain string, baseURL string) string {
	if !strings.HasSuffix(url.Hostname(), baseDomain) {
		return baseURL
	}
	return "https://" + url.Host
}

func (s *server) tparams(r *http.Request, more map[string]interface{}) map[string]interface{} {
	res := map[string]interface{}{}
	res["css"] = ht.CSS(s.css)
	urlCopy := *r.URL
	res["full_path"] = urlCopy.String()
	urlCopy.Host = r.Host
	res["base_url"] = ht.URL(s.cfg.BaseURL)
	res["lang_base_url"] = ht.URL(getLangBaseURL(urlCopy, s.cfg.BaseDomain, s.cfg.BaseURL))
	res["hostname"] = urlCopy.Hostname()
	res["base_domain"] = s.cfg.BaseDomain
	res["ru_domain"] = "ru." + s.cfg.BaseDomain
	res["lang"] = langs(urlCopy, s.cfg.BaseDomain, map[string]string{"en": "", "ru": "ru."})
	res["version"] = lib.Version
	for k, v := range more {
		res[k] = v
	}
	ah := mimeheader.ParseAcceptHeader(r.Header.Get("Accept"))
	imgExts := map[string]string{}
	imgExts["svg"] = "svgz"
	if ah.Match("image/webp") {
		imgExts["png"] = "webp"
	} else {
		imgExts["png"] = "svgz"
	}
	res["img_exts"] = imgExts
	return res
}

func (s *server) enIndexHandler(w http.ResponseWriter, r *http.Request) {
	checkErr(s.enIndexTemplate.Execute(w, s.tparams(r, nil)))
}

func (s *server) ruIndexHandler(w http.ResponseWriter, r *http.Request) {
	checkErr(s.ruIndexTemplate.Execute(w, s.tparams(r, nil)))
}

func (s *server) enStreamerHandler(w http.ResponseWriter, r *http.Request) {
	checkErr(s.enStreamerTemplate.Execute(w, s.tparams(r, nil)))
}

func (s *server) ruStreamerHandler(w http.ResponseWriter, r *http.Request) {
	checkErr(s.ruStreamerTemplate.Execute(w, s.tparams(r, nil)))
}

func (s *server) enChicHandler(w http.ResponseWriter, r *http.Request) {
	checkErr(s.enChicTemplate.Execute(w, s.tparams(r, map[string]interface{}{"packs": s.enabledPacks, "likes": s.likes()})))
}

func (s *server) ruChicHandler(w http.ResponseWriter, r *http.Request) {
	checkErr(s.ruChicTemplate.Execute(w, s.tparams(r, map[string]interface{}{"packs": s.enabledPacks, "likes": s.likes()})))
}

func (s *server) packHandler(w http.ResponseWriter, r *http.Request, t *ht.Template) {
	pack := s.findPack(mux.Vars(r)["pack"])
	if pack == nil {
		notFoundError(w)
		return
	}
	sirenError := false
	paramDict := getParamDict(packParams, r)
	siren := paramDict["siren"]
	if siren != "" && checkSirenParam(siren) == "" {
		sirenError = true
	}
	checkErr(t.Execute(w, s.tparams(r, map[string]interface{}{"pack": pack, "params": paramDict, "likes": s.likesForPack(pack.Name), "siren_error": sirenError})))
}

func (s *server) enPackHandler(w http.ResponseWriter, r *http.Request) {
	s.packHandler(w, r, s.enPackTemplate)
}

func (s *server) ruPackHandler(w http.ResponseWriter, r *http.Request) {
	s.packHandler(w, r, s.ruPackTemplate)
}

func checkSirenParam(siren string) string {
	m := chaturbateModelRegex.FindStringSubmatch(siren)
	if len(m) == 3 {
		siren = m[1]
		if siren == "" {
			siren = m[2]
		}
	}
	if siren == "in" || siren == "p" || siren == "b" || siren == "affiliates" || siren == "external_link" {
		return ""
	}
	return siren
}

func (s *server) codeHandler(w http.ResponseWriter, r *http.Request, t *ht.Template) {
	pack := s.findPack(mux.Vars(r)["pack"])
	if pack == nil {
		notFoundError(w)
		return
	}
	paramDict := getParamDict(packParams, r)
	siren := checkSirenParam(paramDict["siren"])
	paramDict["siren"] = siren
	if siren == "" {
		target := "/chic/p/" + pack.Name
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		return
	}
	code := s.chaturbateCode(pack, paramDict)
	checkErr(t.Execute(w, s.tparams(r, map[string]interface{}{"pack": pack, "params": paramDict, "code": code})))
}

func (s *server) enCodeHandler(w http.ResponseWriter, r *http.Request) {
	s.codeHandler(w, r, s.enCodeTemplate)
}

func (s *server) ruCodeHandler(w http.ResponseWriter, r *http.Request) {
	s.codeHandler(w, r, s.ruCodeTemplate)
}

func (s *server) testHandler(w http.ResponseWriter, r *http.Request) {
	pack := s.findPack(mux.Vars(r)["pack"])
	if pack == nil {
		notFoundError(w)
		return
	}
	paramDict := getParamDict(packParams, r)
	code := s.chaturbateCode(pack, paramDict)
	_, _ = w.Write([]byte(code))
}

func (s *server) likeHandler(w http.ResponseWriter, r *http.Request) {
	pack := s.findPack(mux.Vars(r)["pack"])
	if pack == nil {
		notFoundError(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1000))
	if err != nil {
		notFoundError(w)
		return
	}
	lib.CloseBody(r.Body)
	var like likeForPack
	if err := json.Unmarshal(body, &like); err != nil {
		notFoundError(w)
		return
	}
	if like.Pack != pack.Name {
		notFoundError(w)
		return
	}
	ip := r.Header.Get("X-Forwarded-For")
	s.mustExec(`
		insert into likes (address, pack, like, timestamp) values (?, ?, ?, ?)
		on conflict(address, pack) do update set like=excluded.like, timestamp=excluded.timestamp`,
		ip,
		like.Pack,
		like.Like,
		int32(time.Now().Unix()),
	)
}

func (s *server) likes() map[string]int {
	query := s.mustQuery("select pack, sum(like) * 2 - count(*) from likes group by pack")
	defer func() { checkErr(query.Close()) }()
	results := map[string]int{}
	for query.Next() {
		var pack string
		var count int
		checkErr(query.Scan(&pack, &count))
		results[pack] = count
	}
	return results
}

func (s *server) findPack(name string) *sitelib.Pack {
	for _, pack := range s.packs {
		if pack.Name == name {
			return &pack
		}
	}
	return nil
}

func (s *server) chaturbateCode(pack *sitelib.Pack, params map[string]string) string {
	t := parseHTMLTemplate("common/links.gohtml")
	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	size := 62 * pack.Scale / 100
	hgap := pack.HGap
	if hgap == nil {
		defaultGap := 25
		hgap = &defaultGap
	}
	checkErr(t.Execute(w, map[string]interface{}{
		"pack":     pack,
		"params":   params,
		"size":     size,
		"hgap":     size * (*hgap + 100 - pack.Scale) / 100,
		"base_url": s.cfg.BaseURL,
	}))
	checkErr(w.Flush())
	m := minify.New()
	m.Add("text/html", &hmin.Minifier{KeepQuotes: true, KeepComments: true})
	str, err := m.String("text/html", b.String())
	if err != nil {
		panic(err)
	}
	return str
}

func cacheControlHandler(h http.Handler, mins int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%d", mins*60))
		h.ServeHTTP(w, r)
	})
}

func (s *server) measure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		h.ServeHTTP(w, r)
		elapsed := time.Since(now).Milliseconds()
		if s.cfg.Debug {
			ldbg("performance for %s: %dms", r.URL.Path, elapsed)
		}
	})
}

func (s *server) likesForPack(pack string) int {
	return s.mustInt("select coalesce(sum(like) * 2 - count(*), 0) from likes where pack=?", pack)
}

func (s *server) iconsCount() int {
	count := 0
	for _, i := range s.packs {
		count += len(i.Icons)
	}
	return count
}

func (s *server) fillTemplates() {
	common := []string{"common/head.gohtml", "common/header.gohtml", "common/footer.gohtml", "common/header-icon.gohtml"}
	s.enIndexTemplate = parseHTMLTemplate(append([]string{"en/index.gohtml", "en/trans.gohtml"}, common...)...)
	s.ruIndexTemplate = parseHTMLTemplate(append([]string{"ru/index.gohtml", "ru/trans.gohtml"}, common...)...)
	s.enStreamerTemplate = parseHTMLTemplate(append([]string{"en/streamer.gohtml", "en/trans.gohtml"}, common...)...)
	s.ruStreamerTemplate = parseHTMLTemplate(append([]string{"ru/streamer.gohtml", "ru/trans.gohtml"}, common...)...)

	chic := []string{"common/head.gohtml", "common/header.gohtml", "common/footer.gohtml", "common/cpix.gohtml"}
	s.enChicTemplate = parseHTMLTemplate(append([]string{"common/chic.gohtml", "en/chic.gohtml", "en/trans.gohtml"}, chic...)...)
	s.ruChicTemplate = parseHTMLTemplate(append([]string{"common/chic.gohtml", "ru/chic.gohtml", "ru/trans.gohtml"}, chic...)...)
	s.enPackTemplate = parseHTMLTemplate(append([]string{"en/pack.gohtml", "en/trans.gohtml", "common/twitter.gohtml"}, chic...)...)
	s.ruPackTemplate = parseHTMLTemplate(append([]string{"ru/pack.gohtml", "ru/trans.gohtml", "common/twitter.gohtml"}, chic...)...)
	s.enCodeTemplate = parseHTMLTemplate(append([]string{"en/code.gohtml", "en/trans.gohtml", "common/twitter.gohtml"}, chic...)...)
	s.ruCodeTemplate = parseHTMLTemplate(append([]string{"ru/code.gohtml", "ru/trans.gohtml", "common/twitter.gohtml"}, chic...)...)
}

func (s *server) fillEnabledPacks() {
	packs := make([]sitelib.Pack, 0, len(s.packs))
	for _, pack := range s.packs {
		if !pack.Disable {
			packs = append(packs, pack)
		}
	}
	s.enabledPacks = packs
}

func (s *server) fillCSS() {
	bs, err := os.ReadFile("wwwroot/styles.css")
	checkErr(err)
	s.css = string(bs)
}

func main() {
	linf("starting...")
	flag.Parse()
	if flag.NArg() != 1 {
		panic("usage: site <config>")
	}
	srv := &server{cfg: sitelib.ReadConfig(flag.Arg(0))}
	srv.packs = sitelib.ParsePacks(srv.cfg.Files)
	if len(srv.packs) > 2 {
		srv.packs = append([]sitelib.Pack{srv.packs[len(srv.packs)-1]}, srv.packs[:len(srv.packs)-1]...)
	}
	srv.fillTemplates()
	srv.fillEnabledPacks()
	srv.fillCSS()
	db, err := sql.Open("sqlite3", srv.cfg.DBPath)
	checkErr(err)
	srv.db = db
	srv.createDatabase()
	fmt.Printf("%d packs loaded, %d icons\n", len(srv.packs), srv.iconsCount())
	ruDomain := "ru." + srv.cfg.BaseDomain
	r := mux.NewRouter().StrictSlash(true)
	r.Handle("/", srv.measure(handlers.CompressHandler(http.HandlerFunc(srv.ruIndexHandler)))).Host(ruDomain)
	r.Handle("/", srv.measure(handlers.CompressHandler(http.HandlerFunc(srv.enIndexHandler))))

	r.Handle("/streamer", srv.measure(handlers.CompressHandler(http.HandlerFunc(srv.ruStreamerHandler)))).Host(ruDomain)
	r.Handle("/streamer", srv.measure(handlers.CompressHandler(http.HandlerFunc(srv.enStreamerHandler))))

	r.Handle("/chic", srv.measure(handlers.CompressHandler(http.HandlerFunc(srv.ruChicHandler)))).Host(ruDomain)
	r.Handle("/chic", srv.measure(handlers.CompressHandler(http.HandlerFunc(srv.enChicHandler))))
	r.Handle("/chic/p/{pack}", srv.measure(handlers.CompressHandler(http.HandlerFunc(srv.ruPackHandler)))).Host(ruDomain)
	r.Handle("/chic/p/{pack}", srv.measure(handlers.CompressHandler(http.HandlerFunc(srv.enPackHandler))))
	r.Handle("/chic/code/{pack}", srv.measure(handlers.CompressHandler(http.HandlerFunc(srv.ruCodeHandler)))).Host(ruDomain)
	r.Handle("/chic/code/{pack}", srv.measure(handlers.CompressHandler(http.HandlerFunc(srv.enCodeHandler))))
	r.Handle("/chic/test/{pack}", srv.measure(handlers.CompressHandler(http.HandlerFunc(srv.testHandler))))
	r.Handle("/chic/like/{pack}", srv.measure(http.HandlerFunc(srv.likeHandler)))

	svgzHeaders := func(h http.Handler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, ".svgz") {
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Content-Type", "image/svg+xml")
			}
			h.ServeHTTP(w, r)
		}
	}
	r.PathPrefix("/chic/i/{pack}/{file:.*\\.(?:png|svg|svgz|webp|jpg)}").Handler(http.StripPrefix("/chic/i", svgzHeaders(cacheControlHandler(http.FileServer(http.Dir(srv.cfg.Files)), 120))))
	r.PathPrefix("/icons/").Handler(http.StripPrefix("/icons", cacheControlHandler(http.FileServer(http.Dir("icons")), 120)))
	r.PathPrefix("/node_modules/").Handler(http.StripPrefix("/node_modules", cacheControlHandler(handlers.CompressHandler(http.FileServer(http.Dir("node_modules"))), 120)))

	r.Handle("/ru", newRedirectSubdHandler("ru", "", http.StatusMovedPermanently))
	r.Handle("/ru.html", newRedirectSubdHandler("ru", "", http.StatusMovedPermanently))
	r.Handle("/streamer-ru", newRedirectSubdHandler("ru", "/streamer", http.StatusMovedPermanently))
	r.Handle("/model.html", http.RedirectHandler("/streamer", http.StatusMovedPermanently))
	r.Handle("/model-ru.html", newRedirectSubdHandler("ru", "/streamer", http.StatusMovedPermanently))

	checkErr(http.ListenAndServe(srv.cfg.ListenAddress, r))
}
