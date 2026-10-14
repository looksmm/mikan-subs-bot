package tgbot

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"subsbot/internal/billing"
	"subsbot/internal/domain"
	"subsbot/internal/events"
	"subsbot/internal/tgate"
)

// The bot's screens as an addon see the panel over the Panel API: POST /panel/v2/bot/<method>
// calls one method of Backend or Shop ("Shop.<method>") with its arguments as a JSON array
// (the context left out) and answers its results the same
// way, or an error by its code. RPCHandler is the panel's side, Remote the addon's: the
// screens run unchanged on either (docs/pipeline/addons.md, section 13, PR 10).

// rpcMaxBody bounds a call and its answer.
const rpcMaxBody = 4 << 20

// rpcErrors are the errors the screens tell apart (errors.Is): they cross by name and
// come back as themselves.
var rpcErrors = map[string]error{
	"no_rows":           sql.ErrNoRows,
	"not_found":         domain.ErrNotFound,
	"link_limit":        ErrLinkLimit,
	"not_channel":       ErrNotChannel,
	"link_expired":      tgate.ErrLinkExpired,
	"link_invalid":      tgate.ErrLinkInvalid,
	"unbind_off":        domain.ErrUnbindOff,
	"unbind_cooldown":   domain.ErrUnbindCooldown,
	"reissue_off":       domain.ErrReissueOff,
	"reissue_cooldown":  domain.ErrReissueCooldown,
	"free_active":       billing.ErrFreeActive,
	"free_channel":      billing.ErrFreeChannel,
	"free_off":          billing.ErrFreeOff,
	"not_for_sale":      billing.ErrNotForSale,
	"not_yours":         billing.ErrNotYours,
	"too_many":          billing.ErrTooMany,
	"too_many_subs":     billing.ErrTooManySubs,
	"trial_off":         billing.ErrTrialOff,
	"trial_used":        billing.ErrTrialUsed,
	"context_canceled":  context.Canceled,
	"deadline_exceeded": context.DeadlineExceeded,
}

// rpcError is an error that crossed: its code (a key of rpcErrors, or "error") and text.
type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

// Is lets errors.Is find the sentinel the code names.
func (e *rpcError) Is(target error) bool {
	s, ok := rpcErrors[e.Code]
	return ok && s == target
}

func codeOf(err error) rpcError {
	for code, s := range rpcErrors {
		if errors.Is(err, s) {
			return rpcError{Code: code, Message: err.Error()}
		}
	}
	return rpcError{Code: "error", Message: err.Error()}
}

var (
	backendType = reflect.TypeFor[Backend]()
	shopType    = reflect.TypeFor[Shop]()
	errorType   = reflect.TypeFor[error]()
	contextType = reflect.TypeFor[context.Context]()
)

// RPCHandler serves the calls of Remote on b: only the methods of Backend and Shop are
// reachable, whatever else b has (Local's notices and broadcast targets are the panel's).
func RPCHandler(b Backend) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("method")
		var fn reflect.Value
		switch {
		case strings.HasPrefix(name, "Shop."):
			if m, ok := shopType.MethodByName(strings.TrimPrefix(name, "Shop.")); ok {
				fn = reflect.ValueOf(b.Shop()).MethodByName(m.Name)
			}
		case name != "Shop":
			if m, ok := backendType.MethodByName(name); ok {
				fn = reflect.ValueOf(b).MethodByName(m.Name)
			}
		}
		if !fn.IsValid() {
			rpcWrite(w, http.StatusNotFound, map[string]any{"error": rpcError{Code: "no_method", Message: "no method " + name}})
			return
		}
		var raw []json.RawMessage
		if err := json.NewDecoder(io.LimitReader(r.Body, rpcMaxBody)).Decode(&raw); err != nil {
			rpcWrite(w, http.StatusBadRequest, map[string]any{"error": rpcError{Code: "bad_request", Message: "the body is a JSON array of the arguments"}})
			return
		}
		t := fn.Type()
		args := []reflect.Value{}
		first := 0
		if t.NumIn() > 0 && t.In(0) == contextType {
			args, first = append(args, reflect.ValueOf(r.Context())), 1
		}
		if len(raw) != t.NumIn()-first {
			rpcWrite(w, http.StatusBadRequest, map[string]any{"error": rpcError{Code: "bad_request", Message: fmt.Sprintf("%s takes %d arguments", name, t.NumIn()-first)}})
			return
		}
		for i, a := range raw {
			v := reflect.New(t.In(first + i))
			if err := json.Unmarshal(a, v.Interface()); err != nil {
				rpcWrite(w, http.StatusBadRequest, map[string]any{"error": rpcError{Code: "bad_request", Message: fmt.Sprintf("argument %d: %v", i+1, err)}})
				return
			}
			args = append(args, v.Elem())
		}
		out := fn.Call(args)
		results := []any{}
		for i, v := range out {
			if t.Out(i) == errorType {
				if !v.IsNil() {
					rpcWrite(w, http.StatusOK, map[string]any{"error": codeOf(v.Interface().(error))})
					return
				}
				continue
			}
			results = append(results, v.Interface())
		}
		rpcWrite(w, http.StatusOK, map[string]any{"results": results})
	})
}

func rpcWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Remote is Backend over the Panel API: base is MIKAN_PANEL_URL, token MIKAN_PANEL_TOKEN.
type Remote struct {
	base, token string
	hc          *http.Client
	shop        Shop
}

// NewRemote: hc nil is a client with a minute's timeout.
func NewRemote(base, token string, hc *http.Client) *Remote {
	if hc == nil {
		hc = &http.Client{Timeout: time.Minute}
	}
	r := &Remote{base: strings.TrimRight(base, "/"), token: token, hc: hc}
	r.shop = remoteShop{r}
	return r
}

// call runs method with args and reads its results into outs.
func (r *Remote) call(ctx context.Context, method string, args []any, outs ...any) error {
	if args == nil {
		args = []any{}
	}
	body, err := json.Marshal(args)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/bot/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.hc.Do(req)
	if err != nil {
		return fmt.Errorf("panel: %s: %w", method, err)
	}
	defer resp.Body.Close()
	var ans struct {
		Results []json.RawMessage `json:"results"`
		Error   *rpcError         `json:"error"`
		Code    string            `json:"code"` // the Panel API's own refusal
		Message string            `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, rpcMaxBody)).Decode(&ans); err != nil {
		return fmt.Errorf("panel: %s: HTTP %d", method, resp.StatusCode)
	}
	switch {
	case ans.Error != nil:
		return ans.Error
	case ans.Code == "not_talking":
		return fmt.Errorf("panel: %s: %w", method, ErrNotTalking)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("panel: %s: %s (%s)", method, ans.Code, ans.Message)
	case len(ans.Results) != len(outs):
		return fmt.Errorf("panel: %s gave %d results, want %d", method, len(ans.Results), len(outs))
	}
	for i, o := range outs {
		if err := json.Unmarshal(ans.Results[i], o); err != nil {
			return fmt.Errorf("panel: %s: result %d: %w", method, i+1, err)
		}
	}
	return nil
}

// Events reads the panel's feed of the events of the given types, waiting up to wait for
// one to come (GET /events).
func (r *Remote) Events(ctx context.Context, wait time.Duration, types []string) ([]events.Event, error) {
	q := url.Values{"wait": {strconv.Itoa(int(wait / time.Second))}, "types": {strings.Join(types, ",")}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+"/events?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("events: HTTP %d", resp.StatusCode)
	}
	var feed struct {
		Events []events.Event `json:"events"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, rpcMaxBody)).Decode(&feed); err != nil {
		return nil, err
	}
	return feed.Events, nil
}

// Ack tells the panel an event was handled, so that the feed does not hand it out again
// (POST /events/ack).
func (r *Remote) Ack(ctx context.Context, id int64) error {
	body, _ := json.Marshal(map[string]int64{"id": id})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/events/ack", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return errors.New("ack: HTTP " + resp.Status)
	}
	return nil
}

var _ Shop = remoteShop{}
