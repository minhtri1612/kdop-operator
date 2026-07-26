package tunnel

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// RunGateway starts a reverse proxy that routes by ?target=...
// Example: /?target=http://127.0.0.1:8090/  or  /?target=127.0.0.1:8090
func RunGateway(listenAddr string) error {
	log.Printf("Starting Tunnel Gateway on %s", listenAddr)

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			target := req.URL.Query().Get("target")
			if target == "" {
				log.Println("Error: missing target query parameter")
				return
			}

			targetURL, err := url.Parse(target)
			if err != nil || targetURL.Scheme == "" || targetURL.Host == "" {
				targetURL, err = url.Parse("http://" + target)
				if err != nil {
					log.Printf("Error parsing target URL %s: %v", target, err)
					return
				}
			}

			switch targetURL.Scheme {
			case "ws":
				targetURL.Scheme = "http"
			case "wss":
				targetURL.Scheme = "https"
			}

			log.Printf("Proxying request to %s", targetURL)

			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			if targetURL.Path != "" {
				req.URL.Path = targetURL.Path
			}

			targetQuery := targetURL.Query()
			if token := req.URL.Query().Get("token"); token != "" {
				targetQuery.Set("token", token)
			}
			req.URL.RawQuery = targetQuery.Encode()
			req.Host = targetURL.Host
		},
		FlushInterval: 100 * time.Millisecond,
	}

	mux := http.NewServeMux()
	mux.Handle("/", proxy)
	return http.ListenAndServe(listenAddr, mux)
}
