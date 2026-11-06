package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// routeName maps a CR kind to its lower-case plural path segment.
// "ModelProfile" -> "modelprofiles", "ReferenceRepo" -> "referencerepos".
func routeName(kind string) string {
	return strings.ToLower(kind) + "s"
}

// writeJSON serializes v as the response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a small JSON error envelope.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
