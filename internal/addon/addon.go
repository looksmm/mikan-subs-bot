// Package addon is the bot's side of mikan's protocol 2 towards the panel: the panel calls
// /v2/* with the addon's token, shows /ui/* in a sandboxed frame of the admin panel and
// forwards the frame's calls to POST /v2/ui/rpc. That is the addon's settings page.
package addon

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"subsbot/internal/plus"
)

//go:embed ui
var uiFiles embed.FS

// Deps is what the page shows and changes.
type Deps struct {
	ID      string // the addon's id in the host's state
	State   string // the host's state file: the token the panel calls with
	Token   string // or that token as the environment gives it (a container)
	Version string
	Set     *plus.Store
	Log     *slog.Logger
	// MenuButtons are the admin's own menu buttons in the panel (links and pages), to dress
	// as well.
	MenuButtons func(ctx context.Context) []plus.Button
	// Refused is what Telegram said the last time it turned down a dressed message.
	Refused func() (string, time.Time)
	// Texts are the bot's texts the page may change, with the built-in ones.
	Texts func() []plus.TextEntry
	// Saved runs after a save: the bot sets up its chats again (the command's text).
	Saved func(ctx context.Context)
	// AboutText is «О сервисе»'s built-in text.
	AboutText string

	// The admin's menu, the automatic messages, the bot's chats and the broadcasts.
	Menu  *plus.Doc[plus.Menu]
	Auto  *plus.Doc[plus.Auto]
	Chats *plus.Chats
	Users *plus.Users
	Bot   Broadcaster
	Jobs  *plus.Broadcasts
	// Vars are the variables of the admin's messages.
	Vars any
	// Stats are the counts of the admin's messages; BuiltinDefaults the standard texts of the
	// bot's own messages; PanelNotify which reminders the panel sends.
	Stats           *plus.Stats
	BuiltinDefaults func(ctx context.Context) map[string]string
	PanelNotify     func(ctx context.Context) map[string]bool
}

// Broadcaster is the bot's side of the broadcasts and of the test messages.
type Broadcaster interface {
	StartBroadcast(ctx context.Context, j plus.Broadcast) (*plus.Broadcast, error)
	CancelBroadcast(ctx context.Context) (*plus.Broadcast, error)
	AudienceCounts() (map[string]int, bool)
	TestMessage(ctx context.Context, chat int64, m plus.Message) error
	// Holds says whether the panel gave the bot to this addon.
	Holds(ctx context.Context) bool
}

// Serve answers the panel on listen until ctx ends.
func Serve(ctx context.Context, listen string, d Deps) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: Handler(d), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Handler is the addon's HTTP side, behind the panel's token.
func Handler(d Deps) http.Handler {
	tok := &tokenCache{path: d.State, id: d.ID, fixed: d.Token}
	files, _ := fs.Sub(uiFiles, "ui")
	etags := map[string]string{}
	_ = fs.WalkDir(files, ".", func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			b, _ := fs.ReadFile(files, p)
			sum := sha256.Sum256(b)
			etags[p] = `"` + hex.EncodeToString(sum[:8]) + `"`
		}
		return nil
	})
	name := map[string]string{"ru": "Бот подписчиков", "en": "Subscribers' bot"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": d.ID, "protocol": 2, "version": d.Version, "kind": "bot", "mode": "http",
			"name": name, "description": map[string]string{
				"ru": "Бот подписчиков mikan со своим меню: разделы и кнопки по вашему дереву, рассылка всем, кто запускал бота, автосообщения с кнопками, иконки и цветные кнопки, свои тексты.",
				"en": "mikan's subscribers' bot with a menu of your own: screens and buttons as your tree, broadcasts to everyone who started the bot, automatic messages with buttons, icons and coloured buttons, your own texts.",
			},
			"ui": map[string]any{"entry": "/ui/index.html", "title": name},
		})
	})
	mux.HandleFunc("GET /v2/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /ui/{file...}", func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		if file == "" {
			file = "index.html"
		}
		file = path.Clean(file)
		b, err := fs.ReadFile(files, file)
		if err != nil {
			writeJSON(w, http.StatusNotFound, apiError{Code: "not_found"})
			return
		}
		tag := etags[file]
		w.Header().Set("ETag", tag)
		w.Header().Set("Cache-Control", "no-cache")
		if r.Header.Get("If-None-Match") == tag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		ct := mime.TypeByExtension(path.Ext(file))
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write(b)
	})
	mux.HandleFunc("POST /v2/ui/rpc", func(w http.ResponseWriter, r *http.Request) { rpc(w, r, d) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !tok.matches(got) {
			writeJSON(w, http.StatusUnauthorized, apiError{Code: "unauthorized", Message: "bad token"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type rpcCall struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
}

// statusView is what the page shows.
type statusView struct {
	Version  string           `json:"version"`
	Loaded   bool             `json:"loaded"` // the settings were read from the panel
	Settings *plus.Settings   `json:"settings"`
	Defaults plus.Settings    `json:"defaults"`
	Buttons  []plus.Button    `json:"buttons"` // the bot's own, then the admin's menu buttons
	Styles   []string         `json:"styles"`
	Refused  *refusedView     `json:"refused,omitempty"`
	Texts    []plus.TextEntry `json:"texts"`
	About    string           `json:"about_default"`

	Menu      *plus.Menu      `json:"menu"`
	Auto      *plus.Auto      `json:"auto"`
	Doings    []plus.Doing    `json:"doings"`
	Triggers  []plus.Trigger  `json:"triggers"`
	Audiences []plus.Audience `json:"audiences"`
	Vars      any             `json:"vars"`
	Chats     chatsView       `json:"chats"`
	// Missing are the permissions the extras need and the admin did not give.
	Missing []string `json:"missing"`
	// Holder: the panel gave the bot to this addon; until then nothing reaches Telegram.
	Holder bool `json:"holder"`

	Builtins        []plus.BuiltinMsg     `json:"builtins"`
	BuiltinDefaults map[string]string     `json:"builtin_defaults"`
	PanelNotify     map[string]bool       `json:"panel_notify"`
	Stats           map[string]plus.Count `json:"stats"`
	// Tariffs are the tariffs the panel's subscribers have, for the rules' condition.
	Tariffs []string `json:"tariffs"`
}

type chatsView struct {
	Loaded  bool `json:"loaded"`
	Total   int  `json:"total"`
	Blocked int  `json:"blocked"`
}

// needs are the permissions the page's parts need, besides the bot's own.
var needs = []string{"kv", "users:all", "events", "ui"}

type refusedView struct {
	Text string `json:"text"`
	At   int64  `json:"at"`
}

func status(ctx context.Context, d Deps) statusView {
	v := statusView{Version: d.Version, Loaded: d.Set.Loaded(), Settings: d.Set.Get(), Defaults: plus.Default(),
		Buttons: append([]plus.Button(nil), plus.Buttons...), Styles: plus.Styles}
	if d.MenuButtons != nil {
		mctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		v.Buttons = append(v.Buttons, d.MenuButtons(mctx)...)
		cancel()
	}
	v.About = d.AboutText
	v.Texts = []plus.TextEntry{}
	if d.Texts != nil {
		v.Texts = d.Texts()
	}
	if d.Refused != nil {
		if text, at := d.Refused(); text != "" {
			v.Refused = &refusedView{Text: text, At: at.Unix()}
		}
	}
	v.Doings, v.Triggers, v.Audiences, v.Vars = plus.Doings, plus.Triggers, plus.Audiences, d.Vars
	v.Builtins, v.Stats, v.Tariffs = plus.Builtins, d.Stats.All(), []string{}
	if d.BuiltinDefaults != nil {
		bctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		v.BuiltinDefaults, v.PanelNotify = d.BuiltinDefaults(bctx), d.PanelNotify(bctx)
		cancel()
	}
	if d.Users != nil {
		if byTg, _, err := d.Users.Snapshot(); err == nil {
			seen := map[string]bool{}
			for _, subs := range byTg {
				for _, s := range subs {
					if s.TariffName != "" && !seen[s.TariffName] {
						seen[s.TariffName] = true
						v.Tariffs = append(v.Tariffs, s.TariffName)
					}
				}
			}
			slices.Sort(v.Tariffs)
		}
	}
	v.Holder = true
	if d.Bot != nil {
		hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		v.Holder = d.Bot.Holds(hctx)
		cancel()
	}
	v.Missing = []string{}
	if d.Menu != nil {
		v.Menu = d.Menu.Get()
	}
	if d.Auto != nil {
		v.Auto = d.Auto.Get()
	}
	if d.Chats != nil {
		v.Chats.Loaded = d.Chats.Loaded()
		v.Chats.Total, v.Chats.Blocked = d.Chats.Counts()
	}
	if d.Users != nil {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if self, err := d.Users.Self(sctx); err == nil {
			for _, p := range needs {
				if !slices.Contains(self.Permissions, p) {
					v.Missing = append(v.Missing, p)
				}
			}
		}
		cancel()
	}
	return v
}

// broadcastView is the broadcasts' part of the page.
type broadcastView struct {
	Job     *plus.Broadcast  `json:"job"`
	History []plus.Broadcast `json:"history"`
	Counts  map[string]int   `json:"counts"`
	UsersOK bool             `json:"users_ok"`
}

func broadcastStatus(ctx context.Context, d Deps) (broadcastView, error) {
	v := broadcastView{History: []plus.Broadcast{}, Counts: map[string]int{}}
	if d.Jobs == nil || d.Bot == nil {
		return v, nil
	}
	j, err := d.Jobs.Job(ctx)
	if err != nil {
		return v, err
	}
	v.Job = j
	if h, err := d.Jobs.History(ctx); err == nil {
		v.History = h
	}
	v.Counts, v.UsersOK = d.Bot.AudienceCounts()
	return v, nil
}

// rpc answers the page: its status, and a save of the settings.
func rpc(w http.ResponseWriter, r *http.Request, d Deps) {
	var c rpcCall
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&c); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Code: "bad_request", Message: "не JSON"})
		return
	}
	switch c.Method + " " + c.Path {
	case "GET /status":
		writeJSON(w, http.StatusOK, status(r.Context(), d))
	case "PUT /settings":
		var next plus.Settings
		if err := json.Unmarshal(c.Body, &next); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Code: "bad_request", Message: "настройки не прочитались"})
			return
		}
		if _, err := d.Set.Save(r.Context(), next); err != nil {
			var fe *plus.FieldError
			if errors.As(err, &fe) {
				writeJSON(w, http.StatusUnprocessableEntity, apiError{Code: "invalid", Message: fe.Message, Field: fe.Field})
				return
			}
			d.Log.Warn("save settings", "err", err)
			writeJSON(w, http.StatusInternalServerError, apiError{Code: "save_failed", Message: "не удалось записать настройки в панель"})
			return
		}
		d.Log.Info("settings saved")
		if d.Saved != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				d.Saved(ctx)
			}()
		}
		writeJSON(w, http.StatusOK, status(r.Context(), d))
	case "PUT /menu":
		var next plus.Menu
		if !decodeBody(w, c.Body, &next) || d.Menu == nil {
			return
		}
		if _, err := d.Menu.Save(r.Context(), next, func(m *plus.Menu, t int64) { m.Saved = t }); err != nil {
			saveFailed(w, d, "menu", err)
			return
		}
		d.Log.Info("menu saved")
		writeJSON(w, http.StatusOK, status(r.Context(), d))
	case "PUT /auto":
		var next plus.Auto
		if !decodeBody(w, c.Body, &next) || d.Auto == nil {
			return
		}
		if _, err := d.Auto.Save(r.Context(), next, func(a *plus.Auto, t int64) { a.Saved = t }); err != nil {
			saveFailed(w, d, "auto", err)
			return
		}
		d.Log.Info("automatic messages saved")
		writeJSON(w, http.StatusOK, status(r.Context(), d))
	case "GET /broadcast":
		v, err := broadcastStatus(r.Context(), d)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Code: "panel", Message: "панель не ответила: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, v)
	case "POST /broadcast":
		var j plus.Broadcast
		if !decodeBody(w, c.Body, &j) || d.Bot == nil {
			return
		}
		if _, err := d.Bot.StartBroadcast(r.Context(), j); err != nil {
			broadcastFailed(w, d, err)
			return
		}
		v, _ := broadcastStatus(r.Context(), d)
		writeJSON(w, http.StatusOK, v)
	case "POST /broadcast/cancel":
		if d.Bot == nil {
			return
		}
		if _, err := d.Bot.CancelBroadcast(r.Context()); err != nil {
			broadcastFailed(w, d, err)
			return
		}
		v, _ := broadcastStatus(r.Context(), d)
		writeJSON(w, http.StatusOK, v)
	case "POST /chats/import":
		var in struct {
			IDs []int64 `json:"ids"`
		}
		if !decodeBody(w, c.Body, &in) || d.Chats == nil {
			return
		}
		if len(in.IDs) > maxImport {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Code: "invalid", Message: "за один раз — не больше 10 000 номеров"})
			return
		}
		if !d.Chats.Loaded() {
			writeJSON(w, http.StatusServiceUnavailable, apiError{Code: "loading", Message: "список чатов ещё загружается из панели, попробуйте через минуту"})
			return
		}
		added := d.Chats.Seed(in.IDs)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			d.Chats.Flush(ctx)
		}()
		total, blocked := d.Chats.Counts()
		d.Log.Info("chats imported", "sent", len(in.IDs), "added", added, "total", total)
		writeJSON(w, http.StatusOK, map[string]int{"added": added, "total": total, "blocked": blocked})
	case "GET /export":
		writeJSON(w, http.StatusOK, exportOf(d))
	case "POST /import":
		var in bundle
		if !decodeBody(w, c.Body, &in) {
			return
		}
		if err := importBundle(r.Context(), d, in); err != nil {
			saveFailed(w, d, "import", err)
			return
		}
		d.Log.Info("settings imported")
		writeJSON(w, http.StatusOK, status(r.Context(), d))
	case "POST /test":
		var in struct {
			Chat    int64        `json:"chat"`
			Message plus.Message `json:"message"`
		}
		if !decodeBody(w, c.Body, &in) || d.Bot == nil {
			return
		}
		in.Message.Normalize()
		if err := in.Message.Valid("message", "сообщение"); err != nil {
			saveFailed(w, d, "test", err)
			return
		}
		if in.Chat <= 0 {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Code: "invalid", Field: "test_chat", Message: "укажите свой Telegram ID"})
			return
		}
		tctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := d.Bot.TestMessage(tctx, in.Chat, in.Message); err != nil {
			if err.Error() == "not_holder" {
				writeJSON(w, http.StatusConflict, apiError{Code: "not_holder", Message: notHolderText})
				return
			}
			writeJSON(w, http.StatusBadGateway, apiError{Code: "telegram", Message: telegramText(err)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeJSON(w, http.StatusNotFound, apiError{Code: "not_found", Message: c.Method + " " + c.Path})
	}
}

// maxImport bounds one call of the chats' import; the page sends a long list in parts.
const maxImport = 10000

// bundle is everything the admin set on the page, to move to another panel or to keep: the
// panel deletes an addon's store with the addon.
type bundle struct {
	Kind     string         `json:"kind"`
	Settings *plus.Settings `json:"settings"`
	Menu     *plus.Menu     `json:"menu"`
	Auto     *plus.Auto     `json:"auto"`
}

const bundleKind = "mikan-subs-bot-settings"

func exportOf(d Deps) bundle {
	b := bundle{Kind: bundleKind, Settings: d.Set.Get()}
	if d.Menu != nil {
		b.Menu = d.Menu.Get()
	}
	if d.Auto != nil {
		b.Auto = d.Auto.Get()
	}
	return b
}

// importBundle checks all of in before it writes any of it, so a bad part leaves everything
// as it was.
func importBundle(ctx context.Context, d Deps, in bundle) error {
	if in.Kind != bundleKind {
		return &plus.FieldError{Field: "import", Message: "это не настройки бота: вставьте текст из «Выгрузить»"}
	}
	if in.Settings != nil {
		s := *in.Settings
		s.Normalize()
		if err := s.Valid(); err != nil {
			return err
		}
	}
	if in.Menu != nil && d.Menu != nil {
		m := *in.Menu
		m.Normalize()
		if err := m.Valid(); err != nil {
			return err
		}
	}
	if in.Auto != nil && d.Auto != nil {
		a := *in.Auto
		a.Normalize()
		if err := a.Valid(); err != nil {
			return err
		}
	}
	if in.Settings != nil {
		if _, err := d.Set.Save(ctx, *in.Settings); err != nil {
			return err
		}
	}
	if in.Menu != nil && d.Menu != nil {
		if _, err := d.Menu.Save(ctx, *in.Menu, func(m *plus.Menu, t int64) { m.Saved = t }); err != nil {
			return err
		}
	}
	if in.Auto != nil && d.Auto != nil {
		// Imported rules start now: they never act on what happened before the import.
		a := *in.Auto
		for i := range a.Rules {
			a.Rules[i].Created = 0
		}
		if _, err := d.Auto.Save(ctx, a, func(x *plus.Auto, t int64) { x.Saved = t }); err != nil {
			return err
		}
	}
	return nil
}

// decodeBody reads the page's body into v, or answers that it does not read.
func decodeBody(w http.ResponseWriter, body json.RawMessage, v any) bool {
	if err := json.Unmarshal(body, v); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Code: "bad_request", Message: "не прочиталось: " + err.Error()})
		return false
	}
	return true
}

// saveFailed answers a save that did not go: what the admin must change, or that the panel
// did not take it.
func saveFailed(w http.ResponseWriter, d Deps, what string, err error) {
	var fe *plus.FieldError
	if errors.As(err, &fe) {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Code: "invalid", Message: fe.Message, Field: fe.Field})
		return
	}
	d.Log.Warn("save", "what", what, "err", err)
	writeJSON(w, http.StatusInternalServerError, apiError{Code: "save_failed", Message: "не удалось записать в панель"})
}

func broadcastFailed(w http.ResponseWriter, d Deps, err error) {
	var fe *plus.FieldError
	switch {
	case errors.As(err, &fe):
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Code: "invalid", Message: fe.Message, Field: fe.Field})
	case err.Error() == "broadcast_busy":
		writeJSON(w, http.StatusConflict, apiError{Code: "busy", Message: "уже идёт рассылка: дождитесь её конца или остановите"})
	case err.Error() == "broadcast_users":
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Code: "users", Message: "чтобы делить получателей по подписке, дайте аддону право «Все пользователи»"})
	case err.Error() == "not_holder":
		writeJSON(w, http.StatusConflict, apiError{Code: "not_holder", Message: notHolderText})
	case err.Error() == "broadcast_empty":
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Code: "empty", Message: "в этой группе сейчас никого нет"})
	default:
		d.Log.Warn("broadcast", "err", err)
		writeJSON(w, http.StatusInternalServerError, apiError{Code: "failed", Message: "не получилось: " + err.Error()})
	}
}

// notHolderText: the admin has not given the bot to this addon yet.
const notHolderText = "бот сейчас у другого аддона: в «Аддоны» нажмите у этого аддона «Отдать бота», тогда он сможет писать в Telegram"

// telegramText is what Telegram said, in words the admin understands.
func telegramText(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "403"):
		return "Telegram не доставил: этот аккаунт не запускал бота или заблокировал его"
	case strings.Contains(s, "chat not found"):
		return "Telegram не знает такой чат: проверьте ID и что вы запускали бота"
	case strings.Contains(s, "wrong file") || strings.Contains(s, "failed to get HTTP URL content") || strings.Contains(s, "wrong type of the web page"):
		return "Telegram не смог загрузить картинку по этому адресу"
	}
	return "Telegram ответил: " + s
}

// tokenCache is the token the panel calls the addon with: the host writes it to its state
// when the admin installs the addon, which may be after this process started.
type tokenCache struct {
	path, id string
	fixed    string // a token given at start, never read from the state
	mu       sync.Mutex
	token    string
	mod      time.Time
	checked  time.Time
}

func (t *tokenCache) matches(got string) bool {
	if t.fixed != "" {
		return eq(got, t.fixed)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	since := time.Since(t.checked)
	if since > 2*time.Second || (!eq(got, t.token) && since > 200*time.Millisecond) {
		t.reload()
	}
	return t.token != "" && eq(got, t.token)
}

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (t *tokenCache) reload() {
	t.checked = time.Now()
	fi, err := os.Stat(t.path)
	if err != nil {
		t.token = ""
		return
	}
	if fi.ModTime().Equal(t.mod) {
		return
	}
	f, err := os.Open(t.path)
	if err != nil {
		return
	}
	defer f.Close()
	var st struct {
		Adapters map[string]struct {
			Token  string `json:"token"`
			Source string `json:"source"`
		} `json:"adapters"`
	}
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&st) != nil {
		return
	}
	// Only an addon installed as this process: a container under that id has its own token.
	a, ok := st.Adapters[t.id]
	if !ok || a.Source != "external" {
		t.token, t.mod = "", fi.ModTime()
		return
	}
	t.token, t.mod = a.Token, fi.ModTime()
}
