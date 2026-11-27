package middleware

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// SameOrigin protects the unauthenticated local API, including upload handlers.
// Non-browser clients without Origin remain supported. Forwarded headers are not
// trusted: reverse proxies must preserve Host and the externally visible scheme.
func SameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsSameOrigin(r) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "cross-origin request denied"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// IsSameOrigin applies the shared origin policy without writing a response.
func IsSameOrigin(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	}
	if len(origins) != 1 {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	origin, err := url.Parse(origins[0])
	return err == nil && origin.Scheme == scheme &&
		origin.User == nil && origin.Host != "" && origin.Path == "" &&
		origin.RawQuery == "" && !origin.ForceQuery && origin.Fragment == "" &&
		strings.EqualFold(originAuthority(origin.Host, scheme), originAuthority(r.Host, scheme))
}

func originAuthority(host, scheme string) string {
	u := &url.URL{Host: host}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}
