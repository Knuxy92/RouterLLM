package util

import (
	"encoding/json"
	"net/http"
)

// WriteJSON marshals payload and writes it as the response body.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}
