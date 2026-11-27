package httpx

import (
	"net/http"
)

func QueryDefault(r *http.Request, key, fallback string) string {
	if values := r.URL.Query()[key]; len(values) != 0 {
		return values[0]
	}
	return fallback
}
