// Package plustest is a stand-in for the panel's store for addons (Panel API /kv), for
// tests: the same answers, versions and conditions, in memory.
package plustest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// KV is the panel's /kv for one addon.
type KV struct {
	Token string

	mu   sync.Mutex
	vals map[string][]byte
	vers map[string]int64
	// Puts counts the writes that went through.
	Puts int
}

// NewKV serves the store under /panel/v2/kv; the URL to give the client is srv.URL+"/panel/v2".
func NewKV(token string) (*KV, *httptest.Server) {
	k := &KV{Token: token, vals: map[string][]byte{}, vers: map[string]int64{}}
	return k, httptest.NewServer(k)
}

// Value is what is stored under key, nil when nothing.
func (k *KV) Value(key string) []byte {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.vals[key]
}

// Set stores a value as another writer would.
func (k *KV) Set(key string, v []byte) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.vals[key] = v
	k.vers[key]++
}

func fail(w http.ResponseWriter, code int, c string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": c}})
}

func (k *KV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+k.Token {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if r.URL.Path == "/panel/v2/kv" && r.Method == http.MethodGet {
		prefix := r.URL.Query().Get("prefix")
		var keys []string
		for key := range k.vals {
			if strings.HasPrefix(key, prefix) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		out := struct {
			Keys []map[string]any `json:"keys"`
			Next string           `json:"next"`
		}{Keys: []map[string]any{}}
		for _, key := range keys {
			out.Keys = append(out.Keys, map[string]any{"key": key, "version": k.vers[key]})
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	key, ok := strings.CutPrefix(r.URL.Path, "/panel/v2/kv/")
	if !ok || key == "" {
		fail(w, http.StatusNotFound, "not_found")
		return
	}
	v, live := k.vals[key]
	switch r.Method {
	case http.MethodGet:
		if !live {
			fail(w, http.StatusNotFound, "not_found")
			return
		}
		w.Header().Set("ETag", `"`+strconv.FormatInt(k.vers[key], 10)+`"`)
		_, _ = w.Write(v)
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		if m := r.Header.Get("If-Match"); m != "" {
			want, _ := strconv.ParseInt(strings.Trim(m, `"`), 10, 64)
			if !live || k.vers[key] != want {
				fail(w, http.StatusConflict, "conflict")
				return
			}
		}
		if r.URL.Query().Get("if_absent") == "1" && live {
			fail(w, http.StatusConflict, "conflict")
			return
		}
		k.vals[key] = body
		k.vers[key]++
		k.Puts++
		w.Header().Set("ETag", `"`+strconv.FormatInt(k.vers[key], 10)+`"`)
		_ = json.NewEncoder(w).Encode(map[string]int64{"version": k.vers[key]})
	case http.MethodDelete:
		delete(k.vals, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		fail(w, http.StatusMethodNotAllowed, "method")
	}
}
