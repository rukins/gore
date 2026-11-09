package handlers

import (
	"log"
	"net/http"

	"github.com/rukins/gore/internal/config"
	"github.com/rukins/gore/internal/utils"
)

// Logging wraps an http handler and returns a new handler that prints request log.
func LoggingInterceptor() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if config.Conf.GDPR.HideIP {
					log.Println(r.Method, r.URL.Path, r.URL.RawQuery)
				} else {
					log.Println(utils.ReadIP(r), r.Method, r.URL.Path, r.URL.RawQuery)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
