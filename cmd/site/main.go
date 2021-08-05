package main

import (
	"errors"
	"flag"
	"fmt"
	ht "html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"
)

type server struct {
	cfg config
}

type config struct {
	ListenAddress string `yaml:"listen_address"`
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
}

func linf(format string, v ...interface{}) { log.Printf("[INFO] "+format, v...) }

func checkErr(err error) {
	if err != nil {
		panic(err)
	}
}

func readConfig(path string) config {
	file, err := os.Open(filepath.Clean(path))
	checkErr(err)
	defer func() { checkErr(file.Close()) }()
	decoder := yaml.NewDecoder(file)
	parsed := config{}
	err = decoder.Decode(&parsed)
	checkErr(err)
	return parsed
}

func notFoundError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusNotFound)
	_, _ = fmt.Fprint(w, "404 not found")
}

func parseHTMLTemplate(filenames ...string) *ht.Template {
	t, err := ht.New(filenames[0]).Funcs(funcMap).ParseFiles(filenames...)
	checkErr(err)
	return t
}

func (s *server) indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		notFoundError(w)
		return
	}
	t := parseHTMLTemplate("index.gohtml", "head.gohtml", "header.gohtml", "header-en.gohtml", "footer.gohtml")
	checkErr(t.Execute(w, nil))
}

func (s *server) streamerHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/streamer" {
		notFoundError(w)
		return
	}
	t := parseHTMLTemplate("streamer.gohtml", "head.gohtml", "header.gohtml", "header-en.gohtml", "footer.gohtml")
	checkErr(t.Execute(w, nil))
}

func (s *server) indexRuHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ru" {
		notFoundError(w)
		return
	}
	t := parseHTMLTemplate("ru.gohtml", "head.gohtml", "header.gohtml", "header-ru.gohtml", "footer.gohtml")
	checkErr(t.Execute(w, nil))
}

func (s *server) streamerRuHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/streamer-ru" {
		notFoundError(w)
		return
	}
	t := parseHTMLTemplate("streamer-ru.gohtml", "head.gohtml", "header.gohtml", "header-ru.gohtml", "footer.gohtml")
	checkErr(t.Execute(w, nil))
}

func cacheControlHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=7200")
		h.ServeHTTP(w, r)
	})
}

func main() {
	linf("starting...")
	flag.Parse()
	if flag.NArg() != 1 {
		panic("usage: site <config>")
	}
	srv := &server{cfg: readConfig(flag.Arg(0))}
	r := mux.NewRouter().StrictSlash(true)
	r.Handle("/", handlers.CompressHandler(http.HandlerFunc(srv.indexHandler)))
	r.Handle("/ru", handlers.CompressHandler(http.HandlerFunc(srv.indexRuHandler)))
	r.Handle("/ru.html", http.RedirectHandler("/ru", 301))
	r.Handle("/streamer", handlers.CompressHandler(http.HandlerFunc(srv.streamerHandler)))
	r.Handle("/model.html", http.RedirectHandler("/streamer", 301))
	r.Handle("/streamer-ru", handlers.CompressHandler(http.HandlerFunc(srv.streamerRuHandler)))
	r.Handle("/model-ru.html", http.RedirectHandler("/streamer-ru", 301))
	r.PathPrefix("/icons/").Handler(http.StripPrefix("/icons", cacheControlHandler(http.FileServer(http.Dir("icons")))))
	r.PathPrefix("/social/").Handler(http.StripPrefix("/social", cacheControlHandler(http.FileServer(http.Dir("social")))))
	r.PathPrefix("/node_modules/").Handler(http.StripPrefix("/node_modules", handlers.CompressHandler(http.FileServer(http.Dir("node_modules")))))
	r.PathPrefix("/wwwroot/").Handler(http.StripPrefix("/wwwroot", handlers.CompressHandler(http.FileServer(http.Dir("wwwroot")))))
	checkErr(http.ListenAndServe(srv.cfg.ListenAddress, r))
}
