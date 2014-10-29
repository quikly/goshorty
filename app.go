package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/gorilla/mux"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Settings struct {
	RedisUrl       string
	RedisPrefix    string
	RestrictDomain string
	Redirect404    string
	UrlLength      int
}

type ApiAddRequest struct {
	LongUrl string
}

func ApiAddHandler(resp http.ResponseWriter, req *http.Request) {
	body, err := ioutil.ReadAll(req.Body);
	if err != nil {
		RenderJsonError(resp, req, err.Error(), http.StatusInternalServerError)
		return
	}

	var message ApiAddRequest
	dec := json.NewDecoder(strings.NewReader(string(body)))
	for {
		if err := dec.Decode(&message); err == io.EOF {
			break
		} else if err != nil {
			RenderJsonError(resp, req, err.Error(), http.StatusBadRequest)
			return
		}
	}

	if message.LongUrl == "" {
		RenderJsonError(resp, req, "No URL to shorten", http.StatusBadRequest)
		return
	}

	gosUrl, err := NewUrl(message.LongUrl)
	if err != nil {
		RenderJsonError(resp, req, err.Error(), http.StatusBadRequest)
		return
	}

	shortUrl, err := router.Get("redirect").URL("id", gosUrl.Id)
	if err != nil {
		RenderJsonError(resp, req, err.Error(), http.StatusBadRequest)
		return
	}

	json := fmt.Sprintf("{\"id\":\"http://%s%s\",\"longUrl\":\"%s\"}", req.Host, shortUrl, gosUrl.Destination)
	resp.Write([]byte(json))
}

func AddHandler(resp http.ResponseWriter, req *http.Request) {
	gosUrl, err := NewUrl(req.FormValue("url"))
	if err != nil {
		Render(resp, req, "home", map[string]string{"error": err.Error()})
		return
	}

	statsUrl, err := router.Get("stats").URL("id", gosUrl.Id)
	if err != nil {
		RenderError(resp, req, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(resp, req, statsUrl.String(), http.StatusFound)
}

func RedirectHandler(resp http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	gosUrl, err := GetUrl(vars["id"])
	if err != nil {
		RenderError(resp, req, err.Error(), http.StatusInternalServerError)
		return
	} else if gosUrl == nil {
		if settings.Redirect404 != "" {
			originalUrl, err := router.Get("redirect").URL("id", vars["id"])
			if err != nil {
				RenderError(resp, req, err.Error(), http.StatusInternalServerError)
				return
			}
			url404 := strings.Replace(settings.Redirect404, "$gosURL", url.QueryEscape(fmt.Sprintf("http://%s%s", req.Host, originalUrl.String())), 1)
			http.Redirect(resp, req, url404, http.StatusTemporaryRedirect)
			return
		}
		RenderError(resp, req, "No URL was found with that goshorty code", http.StatusNotFound)
		return
	}

	request, _ := requestParser.Parse(req)
	go gosUrl.Hit(request)
	http.Redirect(resp, req, gosUrl.Destination, http.StatusMovedPermanently)
}

func StatHandler(resp http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)

	if req.Header.Get("X-Requested-With") == "" {
		statsUrl, err := router.Get("stats").URL("id", vars["id"])
		if err != nil {
			RenderError(resp, req, err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(resp, req, statsUrl.String(), http.StatusFound)
		return
	}

	gosUrl, err := GetUrl(vars["id"])
	if err != nil {
		RenderError(resp, req, err.Error(), http.StatusInternalServerError)
		return
	} else if gosUrl == nil {
		RenderError(resp, req, "No URL was found with that goshorty code", http.StatusNotFound)
		return
	}

	var body []byte

	switch {
	case vars["what"] == "sources":
		stats, err := gosUrl.Sources(false)
		if err == nil {
			body, err = json.Marshal(stats)
		}
	default:
		stats, err := gosUrl.Stats(vars["what"])
		if err == nil {
			body, err = json.Marshal(stats)
		}
	}

	if err != nil {
		body = []byte(fmt.Sprintf("{\"error\":\"%s\"}", err.Error()))
	}

	resp.Header().Set("Content-Type", "application/json")
	resp.Write(body)
}

func StatsHandler(resp http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	gosUrl, err := GetUrl(vars["id"])
	if err != nil {
		RenderError(resp, req, err.Error(), http.StatusInternalServerError)
		return
	} else if gosUrl == nil {
		RenderError(resp, req, "No URL was found with that goshorty code", http.StatusNotFound)
		return
	}

	hits, err := gosUrl.Hits()
	if err != nil {
		RenderError(resp, req, err.Error(), http.StatusInternalServerError)
		return
	}

	Render(resp, req, "stats", map[string]string{
		"id":   gosUrl.Id,
		"url":  gosUrl.Destination,
		"when": relativeTime(time.Now().Sub(gosUrl.Created)),
		"hits": fmt.Sprintf("%d", hits),
	})
}

func HomeHandler(resp http.ResponseWriter, req *http.Request) {
	Render(resp, req, "home", nil)
}

func relativeTime(duration time.Duration) string {
	hours := int64(math.Abs(duration.Hours()))
	minutes := int64(math.Abs(duration.Minutes()))
	when := ""
	switch {
	case hours >= (365 * 24):
		when = "Over an year ago"
	case hours > (30 * 24):
		when = fmt.Sprintf("%d months ago", int64(hours/(30*24)))
	case hours == (30 * 24):
		when = "a month ago"
	case hours > 24:
		when = fmt.Sprintf("%d days ago", int64(hours/24))
	case hours == 24:
		when = "yesterday"
	case hours >= 2:
		when = fmt.Sprintf("%d hours ago", hours)
	case hours > 1:
		when = "over an hour ago"
	case hours == 1:
		when = "an hour ago"
	case minutes >= 2:
		when = fmt.Sprintf("%d minutes ago", minutes)
	case minutes > 1:
		when = "a minute ago"
	default:
		when = "just now"
	}
	return when
}

var (
	router        = mux.NewRouter()
	settings      = new(Settings)
	requestParser *RequestParser
)

func main() {
	var (
		geoDb       string
		redisUrl    string
		redisPrefix string
		regex       string
		port        int
	)

	flag.StringVar(&redisUrl, "redis_url", "", "Redis url (leave empty for localhost)")
	flag.StringVar(&redisPrefix, "redis_prefix", "goshorty:", "Redis prefix to use")
	flag.StringVar(&settings.RestrictDomain, "domain", "", "Restrict destination URLs to a single domain")
	flag.StringVar(&settings.Redirect404, "redirect_404", "", "Restrict destination URLs to a single domain")
	flag.IntVar(&settings.UrlLength, "length", 5, "How many characters should the short code have")
	flag.StringVar(&regex, "regex", "[A-Za-z0-9]{%d}", "Regular expression to match route for accessing a short code. %d is replaced with <length> setting")
	flag.IntVar(&port, "port", 8080, "Port where server is listening on")
	flag.StringVar(&geoDb, "geo_db", "./GeoIP.dat", "Location to the MaxMind GeoIP country database file")

	flag.Parse()

	var err error
	requestParser, err = NewRequestParser(geoDb)
	if err != nil {
		panic(err)
	}

	regex = fmt.Sprintf(regex, settings.UrlLength)

  url, err := url.Parse(redisUrl)
	if err != nil {
		panic(err)
	}

  settings.RedisUrl = url.Host
	settings.RedisPrefix = redisPrefix

	router.HandleFunc("/api/v1/url", ApiAddHandler).Methods("POST").Name("add")
	router.HandleFunc("/add", AddHandler).Methods("POST").Name("add")
	router.HandleFunc("/{id:"+regex+"}+/{what:(hour|day|week|month|year|all|sources)}", StatHandler).Name("stat")
	router.HandleFunc("/{id:"+regex+"}+", StatsHandler).Name("stats")
	router.HandleFunc("/{id:"+regex+"}", RedirectHandler).Name("redirect")
	router.HandleFunc("/", HomeHandler).Name("home")
	for _, dir := range []string{"css", "js", "img"} {
		router.PathPrefix("/" + dir + "/").Handler(http.StripPrefix("/"+dir+"/", http.FileServer(http.Dir("assets/"+dir))))
	}

	fmt.Println(fmt.Sprintf("Server is listening on port %d", port))
	err = http.ListenAndServe(fmt.Sprintf(":%d", port), router)
	if err != nil {
		panic(err)
	}
}
