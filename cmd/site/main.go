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
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
	"contains_icon": func(ss []sitelib.Icon, s string) bool {
		for _, i := range ss {
			if i.Name == s && i.Enabled {
				return true
			}
		}
		return false
	},
	"trimPrefix": func(s, prefix string) string {
		return strings.TrimPrefix(prefix, s)
	},
}

var sizes = map[string]int{
	"1": 40,
	"2": 46,
	"3": 52,
	"4": 58,
	"5": 64,
	"6": 70,
	"7": 76,
	"8": 82,
	"9": 88,
}

var packParams = []string{
	"siren",
	"fanclub",
	"instagram",
	"twitter",
	"onlyfans",
	"amazon",
	"lovense",
	"pornhub",
	"dmca",
	"allmylinks",
	"onemylink",
	"fancentro",
	"manyvids",
	"frisk",
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

var chaturbateModelRegex = regexp.MustCompile(`^(?:https?://)?(?:www\.|ar\.|de\.|el\.|en\.|es\.|fr\.|hi\.|it\.|ja\.|ko\.|nl\.|pt\.|ru\.|tr\.|zh\.|m\.)?chaturbate\.com(?:/p|/b)?/([A-Za-z0-9\-_@]+)/?(?:\?.*)?$`)

func linf(format string, v ...interface{}) { log.Printf("[INFO] "+format, v...) }

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

func langs(url url.URL, ls ...string) map[string]ht.URL {
	for _, l := range ls {
		url.Host = strings.TrimPrefix(url.Host, l+".")
	}
	res := map[string]ht.URL{}
	res["en"] = ht.URL(url.String())
	host := url.Host
	for _, l := range ls {
		url.Host = l + "." + host
		res[l] = ht.URL(url.String())
	}
	return res
}

func (s *server) tparams(r *http.Request, more map[string]interface{}) map[string]interface{} {
	res := map[string]interface{}{}
	url := *r.URL
	res["full_path"] = url.String()
	url.Host = r.Host
	res["hostname"] = url.Hostname()
	res["base_domain"] = s.cfg.BaseDomain
	res["ru_domain"] = "ru." + s.cfg.BaseDomain
	res["lang"] = langs(url, "ru")
	if more != nil {
		for k, v := range more {
			res[k] = v
		}
	}
	return res
}

func (s *server) enIndexHandler(w http.ResponseWriter, r *http.Request) {
	t := parseHTMLTemplate("en/index.gohtml", "common/head.gohtml", "common/header.gohtml", "en/trans.gohtml", "common/footer.gohtml")
	checkErr(t.Execute(w, s.tparams(r, nil)))
}

func (s *server) ruIndexHandler(w http.ResponseWriter, r *http.Request) {
	t := parseHTMLTemplate("ru/index.gohtml", "common/head.gohtml", "common/header.gohtml", "ru/trans.gohtml", "common/footer.gohtml")
	checkErr(t.Execute(w, s.tparams(r, nil)))
}

func (s *server) enStreamerHandler(w http.ResponseWriter, r *http.Request) {
	t := parseHTMLTemplate("en/streamer.gohtml", "common/head.gohtml", "common/header.gohtml", "en/trans.gohtml", "common/footer.gohtml")
	checkErr(t.Execute(w, s.tparams(r, nil)))
}

func (s *server) ruStreamerHandler(w http.ResponseWriter, r *http.Request) {
	t := parseHTMLTemplate("ru/streamer.gohtml", "common/head.gohtml", "common/header.gohtml", "ru/trans.gohtml", "common/footer.gohtml")
	checkErr(t.Execute(w, s.tparams(r, nil)))
}

func (s *server) enChicHandler(w http.ResponseWriter, r *http.Request) {
	t := parseHTMLTemplate("en/chic.gohtml", "common/head.gohtml", "common/header-chic.gohtml", "en/trans.gohtml", "common/footer.gohtml")
	checkErr(t.Execute(w, s.tparams(r, map[string]interface{}{"packs": s.enabledPacks, "likes": s.likes()})))
}

func (s *server) ruChicHandler(w http.ResponseWriter, r *http.Request) {
	t := parseHTMLTemplate("ru/chic.gohtml", "common/head.gohtml", "common/header-chic.gohtml", "ru/trans.gohtml", "common/footer.gohtml")
	checkErr(t.Execute(w, s.tparams(r, map[string]interface{}{"packs": s.enabledPacks, "likes": s.likes()})))
}

func (s *server) enPackHandler(w http.ResponseWriter, r *http.Request) {
	pack := s.findPack(mux.Vars(r)["pack"])
	if pack == nil {
		notFoundError(w)
		return
	}
	paramDict := getParamDict(packParams, r)
	t := parseHTMLTemplate("en/pack.gohtml", "common/head.gohtml", "common/header-chic.gohtml", "en/trans.gohtml", "common/footer.gohtml")
	checkErr(t.Execute(w, s.tparams(r, map[string]interface{}{"pack": pack, "params": paramDict, "sizes": sizes, "likes": s.likesForPack(pack.Name)})))
}

func (s *server) ruPackHandler(w http.ResponseWriter, r *http.Request) {
	pack := s.findPack(mux.Vars(r)["pack"])
	if pack == nil {
		notFoundError(w)
		return
	}
	paramDict := getParamDict(packParams, r)
	t := parseHTMLTemplate("ru/pack.gohtml", "common/head.gohtml", "common/header-chic.gohtml", "ru/trans.gohtml", "common/footer.gohtml")
	checkErr(t.Execute(w, s.tparams(r, map[string]interface{}{"pack": pack, "params": paramDict, "sizes": sizes, "likes": s.likesForPack(pack.Name)})))
}

func (s *server) enBannerHandler(w http.ResponseWriter, r *http.Request) {
	pack := s.findPack(mux.Vars(r)["pack"])
	if pack == nil {
		notFoundError(w)
		return
	}
	t := parseHTMLTemplate("common/banner.gohtml", "common/head.gohtml")
	checkErr(t.Execute(w, s.tparams(r, map[string]interface{}{"pack": pack})))
}

func (s *server) enCodeHandler(w http.ResponseWriter, r *http.Request) {
	pack := s.findPack(mux.Vars(r)["pack"])
	if pack == nil {
		notFoundError(w)
		return
	}
	paramDict := getParamDict(packParams, r)
	siren := paramDict["siren"]
	if siren == "" {
		target := "/chic/p/" + pack.Name
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		return
	}
	m := chaturbateModelRegex.FindStringSubmatch(siren)
	if len(m) == 2 {
		paramDict["siren"] = m[1]
	}
	code := s.chaturbateCode(pack, paramDict)
	t := parseHTMLTemplate("en/code.gohtml", "common/head.gohtml", "common/header-chic.gohtml", "en/trans.gohtml", "common/footer.gohtml")
	checkErr(t.Execute(w, s.tparams(r, map[string]interface{}{"pack": pack, "params": paramDict, "code": code})))
}

func (s *server) ruCodeHandler(w http.ResponseWriter, r *http.Request) {
	pack := s.findPack(mux.Vars(r)["pack"])
	if pack == nil {
		notFoundError(w)
		return
	}
	paramDict := getParamDict(packParams, r)
	siren := paramDict["siren"]
	if siren == "" {
		target := "/chic/p/" + pack.Name
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		return
	}
	m := chaturbateModelRegex.FindStringSubmatch(siren)
	if len(m) == 2 {
		paramDict["siren"] = m[1]
	}
	code := s.chaturbateCode(pack, paramDict)
	t := parseHTMLTemplate("ru/code.gohtml", "common/head.gohtml", "common/header-chic.gohtml", "ru/trans.gohtml", "common/footer.gohtml")
	checkErr(t.Execute(w, s.tparams(r, map[string]interface{}{"pack": pack, "params": paramDict, "code": code})))
}

func (s *server) likeHandler(w http.ResponseWriter, r *http.Request) {
	pack := s.findPack(mux.Vars(r)["pack"])
	if pack == nil {
		notFoundError(w)
		return
	}
	body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1000))
	if err != nil {
		notFoundError(w)
		return
	}
	checkErr(r.Body.Close())
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

func (s server) likes() map[string]int {
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
	for _, pack := range s.cfg.Packs {
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
	size := sizes[params["size"]] * pack.Scale / 100
	margin := pack.Margin
	if margin == nil {
		var defaultMargin = 25
		margin = &defaultMargin
	}
	if params["siren"] != "" {
		checkErr(t.Execute(w, map[string]interface{}{
			"pack":     pack,
			"params":   params,
			"size":     size,
			"margin":   size * (*margin + 100 - pack.Scale) / 100,
			"base_url": s.cfg.BaseURL,
		}))
	}
	checkErr(w.Flush())
	m := minify.New()
	m.Add("text/html", &hmin.Minifier{KeepQuotes: true, KeepComments: true})
	str, err := m.String("text/html", b.String())
	if err != nil {
		panic(err)
	}
	return str
}

func cacheControlHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=7200")
		h.ServeHTTP(w, r)
	})
}

func (s server) likesForPack(pack string) int {
	return s.mustInt("select coalesce(sum(like) * 2 - count(*), 0) from likes where pack=?", pack)
}

func (s *server) iconsCount() int {
	count := 0
	for _, i := range s.cfg.Packs {
		count += len(i.Icons)
	}
	return count
}

func (s *server) fillEnabledPacks() {
	packs := make([]sitelib.Pack, 0, len(s.cfg.Packs))
	for _, pack := range s.cfg.Packs {
		if !pack.Disable {
			packs = append(packs, pack)
		}
	}
	s.enabledPacks = packs
}

func main() {
	linf("starting...")
	flag.Parse()
	if flag.NArg() != 1 {
		panic("usage: site <config>")
	}
	srv := &server{cfg: sitelib.ReadConfig(flag.Arg(0))}
	srv.fillEnabledPacks()
	db, err := sql.Open("sqlite3", srv.cfg.DBPath)
	checkErr(err)
	srv.db = db
	srv.createDatabase()
	fmt.Printf("%d packs loaded, %d icons\n", len(srv.cfg.Packs), srv.iconsCount())
	ruDomain := "ru." + srv.cfg.BaseDomain
	r := mux.NewRouter().StrictSlash(true)
	r.Handle("/", handlers.CompressHandler(http.HandlerFunc(srv.ruIndexHandler))).Host(ruDomain)
	r.Handle("/", handlers.CompressHandler(http.HandlerFunc(srv.enIndexHandler)))

	r.Handle("/streamer", handlers.CompressHandler(http.HandlerFunc(srv.ruStreamerHandler))).Host(ruDomain)
	r.Handle("/streamer", handlers.CompressHandler(http.HandlerFunc(srv.enStreamerHandler)))

	r.Handle("/chic", handlers.CompressHandler(http.HandlerFunc(srv.ruChicHandler))).Host(ruDomain)
	r.Handle("/chic", handlers.CompressHandler(http.HandlerFunc(srv.enChicHandler)))
	r.Handle("/chic/p/{pack}", handlers.CompressHandler(http.HandlerFunc(srv.ruPackHandler))).Host(ruDomain)
	r.Handle("/chic/p/{pack}", handlers.CompressHandler(http.HandlerFunc(srv.enPackHandler)))
	r.Handle("/chic/banner/{pack}", handlers.CompressHandler(http.HandlerFunc(srv.enBannerHandler)))
	r.Handle("/chic/code/{pack}", handlers.CompressHandler(http.HandlerFunc(srv.ruCodeHandler))).Host(ruDomain)
	r.Handle("/chic/code/{pack}", handlers.CompressHandler(http.HandlerFunc(srv.enCodeHandler)))
	r.HandleFunc("/chic/like/{pack}", srv.likeHandler)

	r.PathPrefix("/chic/i/").Handler(http.StripPrefix("/chic/i", cacheControlHandler(http.FileServer(http.Dir(srv.cfg.Files)))))
	r.PathPrefix("/icons/").Handler(http.StripPrefix("/icons", cacheControlHandler(http.FileServer(http.Dir("icons")))))
	r.PathPrefix("/node_modules/").Handler(http.StripPrefix("/node_modules", handlers.CompressHandler(http.FileServer(http.Dir("node_modules")))))
	r.PathPrefix("/wwwroot/").Handler(http.StripPrefix("/wwwroot", handlers.CompressHandler(http.FileServer(http.Dir("wwwroot")))))

	r.Handle("/ru", newRedirectSubdHandler("ru", "", http.StatusMovedPermanently))
	r.Handle("/ru.html", newRedirectSubdHandler("ru", "", http.StatusMovedPermanently))
	r.Handle("/streamer-ru", newRedirectSubdHandler("ru", "/streamer", http.StatusMovedPermanently))
	r.Handle("/model.html", http.RedirectHandler("/streamer", http.StatusMovedPermanently))
	r.Handle("/model-ru.html", newRedirectSubdHandler("ru", "/streamer", http.StatusMovedPermanently))

	checkErr(http.ListenAndServe(srv.cfg.ListenAddress, r))
}