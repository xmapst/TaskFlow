package httpx

import (
	"encoding/json/v2"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/xmapst/taskflow/api"
)

// Send encodes a response as JSON or YAML according to the Accept header.
func Send(w http.ResponseWriter, r *http.Request, v any) {
	contentType := "application/json"
	w.Header().Add("Vary", "Accept")
	for _, field := range r.Header.Values("Accept") {
		for item := range strings.SplitSeq(field, ",") {
			kind, params, err := mime.ParseMediaType(strings.TrimSpace(item))
			if err != nil {
				continue
			}
			if q, ok := params["q"]; ok {
				value, err := strconv.ParseFloat(q, 64)
				if err != nil || !(value > 0 && value <= 1) {
					continue
				}
			}
			switch kind {
			case "application/yaml", "application/json", "application/*", "*/*":
				if kind == "application/yaml" {
					contentType = "application/yaml"
				}
				goto encode
			}
		}
	}
encode:
	var data []byte
	var err error
	if contentType == "application/yaml" {
		data, err = yaml.Marshal(v)
	} else {
		data, err = json.Marshal(v, api.JSONDuration)
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to encode response", slog.Any("error", err))
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType+"; charset=utf-8")
	_, _ = w.Write(data)
}
