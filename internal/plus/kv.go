package plus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// KV is the addon's store in the panel (Panel API /kv, permission kv): values in the panel's
// database and its backups, each with a version a write may be conditioned on.
type KV struct {
	base, token string
	hc          *http.Client
}

// NewKV: base is MIKAN_PANEL_URL (…/panel/v2), token the addon's panel token.
func NewKV(base, token string, hc *http.Client) *KV {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &KV{base: strings.TrimRight(base, "/"), token: token, hc: hc}
}

var (
	// ErrNotFound: no live value under the key.
	ErrNotFound = errors.New("kv: not found")
	// ErrConflict: the value is not the version the write was conditioned on, or a write
	// that wanted no value found one.
	ErrConflict = errors.New("kv: conflict")
)

func (k *KV) do(ctx context.Context, method, key string, q url.Values, body []byte, hdr map[string]string) (*http.Response, []byte, error) {
	u := k.base + "/kv/" + key
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+k.token)
	for h, v := range hdr {
		req.Header.Set(h, v)
	}
	resp, err := k.hc.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("kv %s %s: %w", method, key, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return resp, b, nil
	case http.StatusNotFound:
		return resp, nil, ErrNotFound
	case http.StatusConflict:
		return resp, nil, ErrConflict
	}
	return resp, nil, fmt.Errorf("kv %s %s: HTTP %d: %s", method, key, resp.StatusCode, strings.TrimSpace(string(b)))
}

// Get is the value under key and its version.
func (k *KV) Get(ctx context.Context, key string) ([]byte, int64, error) {
	resp, b, err := k.do(ctx, http.MethodGet, key, nil, nil, nil)
	if err != nil {
		return nil, 0, err
	}
	v, _ := strconv.ParseInt(strings.Trim(resp.Header.Get("ETag"), `"`), 10, 64)
	return b, v, nil
}

// Put writes value under key. ifVersion > 0 writes only over that version, ifVersion < 0
// only where no value is; 0 writes in any case. ttl 0 keeps it for good.
func (k *KV) Put(ctx context.Context, key string, value []byte, ifVersion int64, ttl time.Duration) (int64, error) {
	q := url.Values{}
	hdr := map[string]string{"Content-Type": "application/octet-stream"}
	switch {
	case ifVersion > 0:
		hdr["If-Match"] = `"` + strconv.FormatInt(ifVersion, 10) + `"`
	case ifVersion < 0:
		q.Set("if_absent", "1")
	}
	if ttl > 0 {
		q.Set("ttl", strconv.FormatInt(int64(ttl/time.Second), 10))
	}
	_, b, err := k.do(ctx, http.MethodPut, key, q, value, hdr)
	if err != nil {
		return 0, err
	}
	var out struct {
		Version int64 `json:"version"`
	}
	_ = json.Unmarshal(b, &out)
	return out.Version, nil
}

// Delete removes the value under key; a key with no value is no error.
func (k *KV) Delete(ctx context.Context, key string) error {
	_, _, err := k.do(ctx, http.MethodDelete, key, nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Keys lists the keys under prefix, all of them, in order.
func (k *KV) Keys(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	after := ""
	for {
		q := url.Values{"prefix": {prefix}, "limit": {"1000"}}
		if after != "" {
			q.Set("after", after)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.base+"/kv?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+k.token)
		resp, err := k.hc.Do(req)
		if err != nil {
			return nil, err
		}
		var page struct {
			Keys []struct {
				Key string `json:"key"`
			} `json:"keys"`
			Next string `json:"next"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&page)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("kv list: HTTP %d", resp.StatusCode)
		}
		if err != nil {
			return nil, err
		}
		for _, x := range page.Keys {
			out = append(out, x.Key)
		}
		if page.Next == "" {
			return out, nil
		}
		after = page.Next
	}
}

// Update reads the JSON value under key into a fresh *T (zero when absent), lets change
// alter it and writes it back only over the version it read, again from the start when
// another writer came first. change returning false leaves the value as it is.
func Update[T any](ctx context.Context, k *KV, key string, change func(v *T) bool) (T, error) {
	for range 8 {
		var v T
		b, ver, err := k.Get(ctx, key)
		switch {
		case errors.Is(err, ErrNotFound):
			ver = -1
		case err != nil:
			return v, err
		default:
			if err := json.Unmarshal(b, &v); err != nil {
				return v, fmt.Errorf("kv %s: %w", key, err)
			}
		}
		if !change(&v) {
			return v, nil
		}
		nb, err := json.Marshal(v)
		if err != nil {
			return v, err
		}
		if _, err := k.Put(ctx, key, nb, ver, 0); errors.Is(err, ErrConflict) {
			continue
		} else if err != nil {
			return v, err
		}
		return v, nil
	}
	var zero T
	return zero, fmt.Errorf("kv %s: too many writers at once", key)
}
