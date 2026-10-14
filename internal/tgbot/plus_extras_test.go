package tgbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"subsbot/internal/billing"
	"subsbot/internal/db"
	"subsbot/internal/domain"
	"subsbot/internal/events"
	"subsbot/internal/plus"
	"subsbot/internal/plus/plustest"
	"subsbot/internal/tgate"
)

// The extras of the bot (the admin's menu, the automatic messages, the broadcasts) against a
// stand-in for the panel (its store, its subscribers, the bot's backend) and for Telegram.

// treeBackend is the panel as the extras ask it.
type treeBackend struct {
	Backend
	shop  *treeShop
	users map[int64]db.User
	menu  atomic.Int64
}

func (*treeBackend) Config(context.Context) Config {
	c := Default("ru")
	c.QuietNight = false
	return c
}
func (*treeBackend) Brand(context.Context) string           { return "ExampleVPN" }
func (*treeBackend) SupportURL(context.Context) string      { return "https://t.me/plus_support" }
func (*treeBackend) MiniAppURL(context.Context) string      { return "https://sub.example.com/s/tg" }
func (*treeBackend) SubURL(context.Context, db.User) string { return "https://sub.example.com/s/abc" }
func (*treeBackend) GrantsLeft(context.Context, int64, time.Time) (domain.GrantsLeft, error) {
	return domain.GrantsLeft{}, nil
}
func (*treeBackend) DeviceCount(context.Context, int64) (int64, error) { return 1, nil }
func (b *treeBackend) Shop() Shop                                      { return b.shop }
func (b *treeBackend) Chat(context.Context, int64) (db.TgChat, error) {
	return db.TgChat{MenuMsgID: b.menu.Load()}, nil
}
func (b *treeBackend) SetMenu(_ context.Context, _, msg int64) error { b.menu.Store(msg); return nil }
func (*treeBackend) Context(context.Context, int64) (Account, error) { return Account{}, nil }
func (b *treeBackend) User(_ context.Context, id int64) (db.User, error) {
	if u, ok := b.users[id]; ok {
		return u, nil
	}
	return db.User{}, sql.ErrNoRows
}

type treeShop struct {
	Shop
	trial atomic.Bool
}

func (*treeShop) Config(context.Context) billing.Config { return billing.Config{AllowNew: true} }
func (*treeShop) Offers(context.Context) ([]billing.Offer, billing.Available, error) {
	return []billing.Offer{standard()}, billing.Available{}, nil
}
func (s *treeShop) TrialOpen(context.Context, int64) bool { return s.trial.Load() }
func (*treeShop) TrialOffer(context.Context, int64) (billing.TrialOffer, billing.Available, bool) {
	return billing.TrialOffer{}, billing.Available{}, false
}
func (*treeShop) FreeOffer(context.Context, int64) (billing.FreeOffer, bool) {
	return billing.FreeOffer{}, false
}
func (*treeShop) PackageOffers(context.Context, int64) ([]billing.PackageOffer, billing.Available, error) {
	return nil, billing.Available{}, nil
}

// fakeTelegram takes what the bot sends; blocked chats answer 403 as Telegram does.
type fakeTelegram struct {
	mu      sync.Mutex
	sent    []map[string]any
	blocked map[int64]bool
	next    int64
	// notHolder: the bot is another addon's: the proxy answers 401 to everything.
	notHolder bool
	// loseAfter: after this many sends the bot goes to another addon (0: never).
	loseAfter int
	// jitter: each answer waits up to this long, so the sends end out of order.
	jitter time.Duration
	// answers are the presses answered (answerCallbackQuery), by id.
	answers map[string]int
}

func (f *fakeTelegram) setHolder(h bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notHolder = !h
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	jitter := f.jitter
	f.mu.Unlock()
	if jitter > 0 {
		time.Sleep(time.Duration(rand.Int64N(int64(jitter))))
	}
	var in map[string]any
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	_ = dec.Decode(&in)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notHolder {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
		return
	}
	if path.Base(r.URL.Path) == "answerCallbackQuery" {
		if f.answers == nil {
			f.answers = map[string]int{}
		}
		id, _ := in["callback_query_id"].(string)
		f.answers[id]++
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		return
	}
	if path.Base(r.URL.Path) == "getMe" {
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Bot"}}`))
		return
	}
	chat, _ := strconv.ParseInt(string(in["chat_id"].(json.Number)), 10, 64)
	if f.blocked[chat] {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`))
		return
	}
	in["method"] = path.Base(r.URL.Path)
	f.sent = append(f.sent, in)
	if f.loseAfter > 0 && len(f.sent) >= f.loseAfter {
		f.notHolder, f.loseAfter = true, 0
	}
	f.next++
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": f.next, "chat": map[string]any{"id": chat}}})
}

// to is what went to chat, in order.
func (f *fakeTelegram) to(chat int64) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, m := range f.sent {
		if c, _ := strconv.ParseInt(string(m["chat_id"].(json.Number)), 10, 64); c == chat {
			out = append(out, m)
		}
	}
	return out
}

func (f *fakeTelegram) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// fakePanelUsers is the panel's /users (users:all) and /self.
type fakePanelUsers struct {
	mu   sync.Mutex
	subs []plus.Sub
}

func (f *fakePanelUsers) set(subs ...plus.Sub) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = subs
}

func (f *fakePanelUsers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/panel/v2/self" {
		_ = json.NewEncoder(w).Encode(map[string]any{"permissions": []string{"bot", "kv", "users:all", "events", "ui"}})
		return
	}
	if id, ok := strings.CutPrefix(r.URL.Path, "/panel/v2/users/"); ok {
		for _, s := range f.subs {
			if strconv.FormatInt(s.ID, 10) == id {
				_ = json.NewEncoder(w).Encode(s)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}
	q := r.URL.Query().Get("q")
	var page []plus.Sub
	for _, s := range f.subs {
		if q == "" || s.TgID != nil && strconv.FormatInt(*s.TgID, 10) == q {
			page = append(page, s)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"users": page, "next": nil})
}

func subOf(id int64, name, state string, tg int64, expires time.Time) plus.Sub {
	s := plus.Sub{ID: id, Name: name, State: state, TariffName: "Стандарт", TgID: &tg}
	if !expires.IsZero() {
		s.ExpiresAt = &expires
	}
	return s
}

// extrasEnv is a bot with its extras, a clock the test moves, the panel and Telegram.
type extrasEnv struct {
	t     *testing.T
	b     *Bot
	be    *treeBackend
	tg    *fakeTelegram
	users *fakePanelUsers
	ex    Extras
	clock atomic.Int64
}

func (e *extrasEnv) now() time.Time { return time.Unix(e.clock.Load(), 0) }
func (e *extrasEnv) move(d time.Duration) {
	e.clock.Add(int64(d / time.Second))
}

func newExtras(t *testing.T) *extrasEnv {
	t.Helper()
	e := &extrasEnv{t: t, tg: &fakeTelegram{blocked: map[int64]bool{}}, users: &fakePanelUsers{}}
	e.clock.Store(1_800_000_000)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tgSrv := httptest.NewServer(e.tg)
	t.Cleanup(tgSrv.Close)
	kv, kvSrv := plustest.NewKV("tok")
	mux := http.NewServeMux()
	mux.Handle("/panel/v2/kv", kv)
	mux.Handle("/panel/v2/kv/", kv)
	mux.Handle("/panel/v2/users", e.users)
	mux.Handle("/panel/v2/users/", e.users)
	mux.Handle("/panel/v2/self", e.users)
	kvSrv.Close()
	panel := httptest.NewServer(mux)
	t.Cleanup(panel.Close)
	base := panel.URL + "/panel/v2"

	client := tgate.NewClient(tgSrv.URL, "0:tok", nil)
	out := tgate.NewOutbox(client, tgate.Limits{Total: 1000, Bulk: 1000, PerChat: 1000, Burst: 100, Workers: 4, MaxTries: 2},
		time.Now, func(int64) {}, func(error) {})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.be = &treeBackend{shop: &treeShop{}, users: map[int64]db.User{}}
	e.b = New(Deps{Backend: e.be, Client: client, Outbox: out, Go: func(f func()) { go f() }, Log: log, Now: e.now})
	kvc := plus.NewKV(base, "tok", nil)
	set := plus.NewStore(kvc, log)
	set.Use(plus.Settings{}) // no look: the bodies stay as the screens make them
	e.b.Plus(set, log)
	e.ex = Extras{
		Menu:       plus.NewMenu(kvc, log),
		Auto:       plus.NewAuto(kvc, log, e.now),
		Chats:      plus.NewChats(kvc, log, e.now),
		Users:      plus.NewUsers(base, "tok", log),
		Broadcasts: plus.NewBroadcasts(kvc),
	}
	e.b.PlusExtras(e.ex)
	if err := e.ex.Chats.Load(ctx); err != nil {
		t.Fatal(err)
	}
	go out.Run(ctx)
	return e
}

// wait waits until f holds, for up to two seconds.
func (e *extrasEnv) wait(what string, f func() bool) {
	e.t.Helper()
	for range 200 {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("waited in vain: %s", what)
}

func treeMenu() plus.Menu {
	return plus.Menu{On: true, Main: "main", Welcome: "hello", Screens: []plus.Screen{
		{ID: "main", Rows: [][]plus.Action{
			{{Label: "📊 Подписка", Do: "sub"}, {Label: "📱 Устройства", Do: "devices"}},
			{{Label: "📚 Инструкции", Do: "screen", To: "help"}},
			{{Label: "💬 Поддержка", Do: "support"}, {Label: "🌐 Страница", Do: "app"}},
		}},
		{ID: "help", Text: "**Инструкции** для {first_name}", Back: true, Rows: [][]plus.Action{
			{{Label: "iPhone", Do: "url", URL: "https://example.com/ios", Icon: "5134263008683362081", Style: "primary"}},
			{{Label: "Ещё", Do: "screen", To: "more"}},
		}},
		{ID: "more", Text: "Ещё", Back: true},
		{ID: "hello", Text: "Привет, {first_name}! Это {brand}.", Rows: [][]plus.Action{
			{{Label: "🎁 Попробовать", Do: "trial"}, {Label: "💳 Купить", Do: "buy"}},
			{{Label: "📊 Подписка", Do: "sub"}},
			{{Label: "📚 Инструкции", Do: "screen", To: "help"}},
		}},
	}}
}

func codes(kb *tgate.Keyboard) []string {
	var out []string
	if kb == nil {
		return out
	}
	for _, r := range kb.InlineKeyboard {
		for _, b := range r {
			switch {
			case b.CallbackData != "":
				out = append(out, b.CallbackData)
			case b.URL != "":
				out = append(out, "url:"+b.URL)
			case b.WebApp != nil:
				out = append(out, "app:"+b.WebApp.URL)
			}
		}
	}
	return out
}

func TestTreeDrawsTheAdminsMenu(t *testing.T) {
	e := newExtras(t)
	e.ex.Menu.Use(treeMenu())
	e.ex.Chats.Seen(7, "Аня", true)
	ctx := context.Background()
	cfg := e.b.config(ctx)
	w := wordsFor("ru")
	u := db.User{ID: 70, Name: "Аня-1"}
	sub := subsOf{list: []db.User{u}, u: u, ok: true}

	// A subscriber's main menu: the panel's main text, the admin's buttons.
	text, kb, ok := e.b.treeFor(ctx, cfg, w, 7, "m", "", "", sub)
	if !ok || !strings.Contains(text, "Аня-1") && text == "" {
		t.Fatalf("main: %v %q", ok, text)
	}
	if got := strings.Join(codes(kb), " "); got != "s d k:help:main url:https://t.me/plus_support app:https://sub.example.com/s/tg" {
		t.Fatalf("main buttons: %s", got)
	}
	// A section: its text with the variables, its button's look, back to the menu.
	text, kb, _ = e.b.treeFor(ctx, cfg, w, 7, treeCode, "help:main", "", sub)
	if text != "<b>Инструкции</b> для Аня" {
		t.Fatalf("help: %q", text)
	}
	if kb.InlineKeyboard[0][0].Icon != "5134263008683362081" || kb.InlineKeyboard[0][0].Style != "primary" {
		t.Fatalf("look: %+v", kb.InlineKeyboard[0][0])
	}
	if got := strings.Join(codes(kb), " "); got != "url:https://example.com/ios k:more:help m" {
		t.Fatalf("help buttons: %s", got)
	}
	// A section of a section goes back to the section.
	_, kb, _ = e.b.treeFor(ctx, cfg, w, 7, treeCode, "more:help", "", sub)
	if got := strings.Join(codes(kb), " "); got != "k:help" {
		t.Fatalf("more buttons: %s", got)
	}
	// A section that is gone: the main menu.
	text2, _, _ := e.b.treeFor(ctx, cfg, w, 7, treeCode, "gone", "", sub)
	text1, _, _ := e.b.treeFor(ctx, cfg, w, 7, "m", "", "", sub)
	if text2 != text1 {
		t.Fatalf("gone: %q", text2)
	}

	// Without a subscription: the admin's first screen; what needs one is not there, the
	// trial only while it can be taken.
	text, kb, ok = e.b.treeFor(ctx, cfg, w, 7, "m", "", "", subsOf{})
	if !ok || text != "Привет, Аня! Это ExampleVPN." {
		t.Fatalf("welcome: %v %q", ok, text)
	}
	if got := strings.Join(codes(kb), " "); got != "b k:help:hello" {
		t.Fatalf("welcome buttons: %s", got)
	}
	e.be.shop.trial.Store(true)
	_, kb, _ = e.b.treeFor(ctx, cfg, w, 7, "m", "", "", subsOf{})
	if got := strings.Join(codes(kb), " "); got != "tr b k:help:hello" {
		t.Fatalf("welcome with a trial: %s", got)
	}
	// Under a message of the admin's the buttons open a new menu; a customer gets no trial.
	rows := e.b.actionRows(ctx, cfg, w, 7, treeMenu().Screens[3].Rows, "", true, sub)
	if got := strings.Join(codes(&tgate.Keyboard{InlineKeyboard: rows}), " "); got != "kn:b kn:s kn:k:help" {
		t.Fatalf("fresh: %s", got)
	}

	// Off: mikan's menu, but a section still opens by its button (a broadcast's).
	m := treeMenu()
	m.On = false
	e.ex.Menu.Use(m)
	if _, _, ok := e.b.treeFor(ctx, cfg, w, 7, "m", "", "", sub); ok {
		t.Fatal("off, yet drawn")
	}
	if _, _, ok := e.b.treeFor(ctx, cfg, w, 7, treeCode, "help", "", sub); !ok {
		t.Fatal("off: a section does not open")
	}
}

func autoRules(created int64) plus.Auto {
	msg := func(text string) plus.Message {
		return plus.Message{Text: text, Rows: [][]plus.Action{{{Label: "Купить", Do: "buy"}, {Label: "Меню", Do: "menu"}}}}
	}
	a := plus.Auto{Rules: []plus.Rule{
		{ID: "hi", On: true, Trigger: "start", Delay: 60, Message: msg("Как дела, {first_name}?")},
		{ID: "nosub", On: true, Trigger: "no_sub", Delay: 24 * 60, Message: msg("Не забыли про {brand}?")},
		{ID: "soon", On: true, Trigger: "expiring", Days: 3, Replace: true, Message: msg("{name} закончится {until}")},
		{ID: "gone", On: true, Trigger: "expired", Delay: 3 * 24 * 60, Message: msg("Вернитесь, {first_name}")},
		{ID: "thanks", On: true, Trigger: "paid", First: true, Message: msg("Спасибо!")},
		{ID: "off", On: false, Trigger: "start", Message: msg("выключено")},
	}}
	for i := range a.Rules {
		a.Rules[i].Created = created
	}
	return a
}

func textOf(m map[string]any) string {
	if t, ok := m["text"].(string); ok {
		return t
	}
	t, _ := m["caption"].(string)
	return t
}

func TestAutoMessagesOfTheBot(t *testing.T) {
	e := newExtras(t)
	ctx := context.Background()
	e.ex.Auto.Use(autoRules(e.now().Unix() - 3600))
	// 11 came before the rules: not a newcomer to them.
	e.ex.Chats.Seen(11, "Old", true)
	e.ex.Auto.Use(autoRules(e.now().Unix() + 1))
	e.move(time.Minute)
	e.ex.Chats.Seen(12, "Аня", true)   // a newcomer who will not buy
	e.ex.Chats.Seen(13, "Борис", true) // a newcomer who buys
	e.ex.Chats.Seen(14, "Вера", false) // wrote, but no /start first: an old customer
	e.users.set(subOf(130, "Борис-1", "active", 13, e.now().Add(30*24*time.Hour)))
	if err := e.ex.Users.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	e.move(30 * time.Minute)
	e.b.autoTick(ctx)
	time.Sleep(50 * time.Millisecond)
	if n := e.tg.count(); n != 0 {
		t.Fatalf("too early: %d", n)
	}
	e.move(31 * time.Minute)
	e.b.autoTick(ctx)
	e.wait("the hour's messages", func() bool { return len(e.tg.to(12)) == 1 && len(e.tg.to(13)) == 1 })
	if got := textOf(e.tg.to(12)[0]); got != "Как дела, Аня?" {
		t.Fatalf("text: %q", got)
	}
	if len(e.tg.to(11)) != 0 || len(e.tg.to(14)) != 0 {
		t.Fatal("an old account got the newcomers' message")
	}
	// The buttons open a new menu; a customer gets no «Купить» (buy opens to everyone).
	kb := e.tg.to(12)[0]["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	if btn := kb[0].([]any)[0].(map[string]any); btn["callback_data"] != "kn:b" {
		t.Fatalf("button: %+v", btn)
	}
	e.b.autoTick(ctx)
	time.Sleep(50 * time.Millisecond)
	if len(e.tg.to(12)) != 1 {
		t.Fatal("sent twice")
	}
	// A day after: only the one who did not buy.
	e.move(24 * time.Hour)
	e.b.autoTick(ctx)
	e.wait("no subscription", func() bool { return len(e.tg.to(12)) == 2 })
	if got := textOf(e.tg.to(12)[1]); got != "Не забыли про ExampleVPN?" {
		t.Fatalf("no sub text: %q", got)
	}
	time.Sleep(50 * time.Millisecond)
	if len(e.tg.to(13)) != 1 {
		t.Fatalf("a customer was told to buy: %d", len(e.tg.to(13)))
	}

	// The panel's event: the admin's message in place of the panel's notice, once.
	exp := e.now().Add(3 * 24 * time.Hour)
	e.be.users[130] = db.User{ID: 130, Name: "Борис-1", ExpiresAt: sql.NullInt64{Int64: exp.Unix(), Valid: true}}
	ev := events.Event{ID: 501, Type: events.UserExpiring, At: e.now(), UserID: 130, TgID: 13, Data: json.RawMessage(`{"days":3}`)}
	if err := e.b.HandleEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if err := e.b.HandleEvent(ctx, ev); err != nil { // delivered again
		t.Fatal(err)
	}
	e.wait("the expiring message", func() bool { return len(e.tg.to(13)) == 2 })
	time.Sleep(50 * time.Millisecond)
	if msgs := e.tg.to(13); len(msgs) != 2 || !strings.HasPrefix(textOf(msgs[1]), "Борис-1 закончится") {
		t.Fatalf("expiring: %d %q", len(msgs), textOf(msgs[len(msgs)-1]))
	}
	// One day before: no rule of its own, the panel's notice goes.
	ev1 := events.Event{ID: 502, Type: events.UserExpiring, At: e.now(), UserID: 130, TgID: 13, Data: json.RawMessage(`{"days":1}`)}
	if err := e.b.HandleEvent(ctx, ev1); err != nil {
		t.Fatal(err)
	}
	e.wait("the panel's notice", func() bool { return len(e.tg.to(13)) == 3 })

	// Ended: the panel's notice now, the admin's three days later, unless renewed by then.
	e.be.users[140] = db.User{ID: 140, Name: "Вера-1"}
	e.users.set(subOf(130, "Борис-1", "expired", 13, e.now()), subOf(140, "Вера-1", "expired", 14, e.now()))
	for i, tg := range []int64{13, 14} {
		uid := []int64{130, 140}[i]
		if err := e.b.HandleEvent(ctx, events.Event{ID: int64(600 + i), Type: events.UserExpired, At: e.now(), UserID: uid, TgID: tg}); err != nil {
			t.Fatal(err)
		}
	}
	e.wait("the panel's ended notices", func() bool { return len(e.tg.to(13)) == 4 && len(e.tg.to(14)) == 1 })
	e.users.set(subOf(130, "Борис-1", "active", 13, e.now().Add(30*24*time.Hour)), subOf(140, "Вера-1", "expired", 14, e.now()))
	e.move(3*24*time.Hour + time.Minute)
	e.b.autoTick(ctx)
	e.wait("come back", func() bool { return len(e.tg.to(14)) == 2 })
	time.Sleep(50 * time.Millisecond)
	if got := textOf(e.tg.to(14)[1]); got != "Вернитесь, Вера" || len(e.tg.to(13)) != 4 {
		t.Fatalf("come back: %q, renewed got %d", got, len(e.tg.to(13)))
	}

	// After a payment: only the first purchase.
	u := db.User{ID: 140, Name: "Вера-1"}
	renew := events.Event{ID: 700, Type: events.PaymentPaid, At: e.now(), UserID: 140, TgID: 14, Data: json.RawMessage(`{"kind":"renew"}`)}
	buy := events.Event{ID: 701, Type: events.PaymentPaid, At: e.now(), UserID: 140, TgID: 14, Data: json.RawMessage(`{"kind":"new"}`)}
	e.b.autoEvent(ctx, renew, u)
	e.b.autoEvent(ctx, buy, u)
	e.wait("thanks", func() bool { return len(e.tg.to(14)) == 3 })
	time.Sleep(50 * time.Millisecond)
	if got := textOf(e.tg.to(14)[2]); got != "Спасибо!" || len(e.tg.to(14)) != 3 {
		t.Fatalf("thanks: %q %d", got, len(e.tg.to(14)))
	}
}

func TestBroadcastReachesEveryChat(t *testing.T) {
	e := newExtras(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.ex.Chats.Seen(101, "Аня", true)   // never bought
	e.ex.Chats.Seen(102, "Борис", true) // a customer
	e.ex.Chats.Seen(103, "Вера", true)  // had one; blocked the bot since, Telegram will say
	e.ex.Chats.Seen(104, "Глеб", true)
	e.ex.Chats.Blocked(104) // known to have blocked it
	e.users.set(subOf(102, "Борис-1", "active", 102, e.now().Add(10*24*time.Hour)),
		subOf(103, "Вера-1", "expired", 103, e.now().Add(-time.Hour)),
		subOf(105, "Дина-1", "expiring", 105, e.now().Add(24*time.Hour))) // never wrote to the bot
	if err := e.ex.Users.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	counts, ok := e.b.AudienceCounts()
	if !ok || counts["all"] != 4 || counts["active"] != 2 || counts["expired"] != 1 || counts["none"] != 1 {
		t.Fatalf("counts: %v %+v", ok, counts)
	}
	e.tg.blocked[103] = true
	go e.b.BroadcastRun(ctx)
	j, err := e.b.StartBroadcast(ctx, plus.Broadcast{Audience: "all", Message: plus.Message{Text: "Привет, {first_name}! {name}",
		Rows: [][]plus.Action{{{Label: "Продлить", Do: "renew"}, {Label: "Канал", Do: "url", URL: "https://t.me/plus_news"}}}}})
	if err != nil || j.Total != 4 {
		t.Fatalf("start: %v %+v", err, j)
	}
	if _, err := e.b.StartBroadcast(ctx, plus.Broadcast{Audience: "all", Message: plus.Message{Text: "второй"}}); err != ErrBroadcastBusy {
		t.Fatalf("a second at once: %v", err)
	}
	var done *plus.Broadcast
	e.wait("the broadcast's end", func() bool {
		done, _ = e.ex.Broadcasts.Job(ctx)
		return done != nil && !done.Active()
	})
	if done.State != plus.BcastDone || done.Sent != 3 || done.Blocked != 1 || done.Failed != 0 {
		t.Fatalf("done: %+v", done)
	}
	if got := textOf(e.tg.to(102)[0]); got != "Привет, Борис! Борис-1" {
		t.Fatalf("text: %q", got)
	}
	// «Продлить» only for those with a subscription.
	kb := func(chat int64) string {
		var out []string
		for _, r := range e.tg.to(chat)[0]["reply_markup"].(map[string]any)["inline_keyboard"].([]any) {
			for _, x := range r.([]any) {
				b := x.(map[string]any)
				out = append(out, b["text"].(string))
			}
		}
		return strings.Join(out, ",")
	}
	if kb(102) != "Продлить,Канал" || kb(101) != "Канал" || len(e.tg.to(104)) != 0 {
		t.Fatalf("buttons: %q %q, blocked got %d", kb(102), kb(101), len(e.tg.to(104)))
	}
	if c, _ := e.ex.Chats.Get(103); c.Blocked == 0 {
		t.Fatal("a chat that blocked the bot is not marked")
	}
	// The history is written right after the job's end.
	e.wait("the history", func() bool { h, _ := e.ex.Broadcasts.History(ctx); return len(h) == 1 && h[0].Sent == 3 })

	// Scheduled: nothing before its time; its list is made when it starts.
	j, err = e.b.StartBroadcast(ctx, plus.Broadcast{Audience: "none", At: e.now().Unix() + 3600, Message: plus.Message{Text: "Скоро"}})
	if err != nil || j.State != plus.BcastScheduled {
		t.Fatalf("schedule: %v %+v", err, j)
	}
	if err := e.b.bcastStep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.tg.to(101)) != 1 {
		t.Fatal("went before its time")
	}
	e.move(time.Hour + time.Minute)
	e.b.bc().poke()
	e.wait("the scheduled one", func() bool { return len(e.tg.to(101)) == 2 })
	// And one more, cancelled before its time.
	if _, err := e.b.StartBroadcast(ctx, plus.Broadcast{Audience: "all", At: e.now().Unix() + 3600, Message: plus.Message{Text: "Нет"}}); err != nil {
		e.wait("the scheduled one ends", func() bool { j, _ := e.ex.Broadcasts.Job(ctx); return j != nil && !j.Active() })
		if _, err := e.b.StartBroadcast(ctx, plus.Broadcast{Audience: "all", At: e.now().Unix() + 3600, Message: plus.Message{Text: "Нет"}}); err != nil {
			t.Fatal(err)
		}
	}
	if j, err := e.b.CancelBroadcast(ctx); err != nil || j.State != plus.BcastCancelled {
		t.Fatalf("cancel: %v %+v", err, j)
	}
}

// While the bot is another addon's nothing is sent and nothing is lost: a broadcast does not
// start, one that loses the bot midway waits and then goes on to the end.
func TestBroadcastWaitsForTheBot(t *testing.T) {
	e := newExtras(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := int64(1); i <= 40; i++ {
		e.ex.Chats.Seen(5000+i, "", true)
	}
	msg := plus.Broadcast{Audience: "all", Message: plus.Message{Text: "Новости"}}
	e.tg.setHolder(false)
	if _, err := e.b.StartBroadcast(ctx, msg); err != ErrNotHolder {
		t.Fatalf("not the holder: %v", err)
	}
	if err := e.b.TestMessage(ctx, 5001, msg.Message); err != ErrNotHolder {
		t.Fatalf("test message: %v", err)
	}
	e.ex.Auto.Use(autoRules(e.now().Unix() - 1))
	e.move(2 * time.Hour)
	e.b.autoTick(ctx)
	if c, _ := e.ex.Chats.Get(5001); len(c.Sent) != 0 {
		t.Fatalf("an automatic message was marked sent while nothing could go: %+v", c.Sent)
	}

	// Given the bot: it starts, loses the bot after 10 messages, waits, goes on.
	e.tg.setHolder(true)
	e.b.bc().mu.Lock()
	e.b.bc().holdAt = time.Time{}
	e.b.bc().mu.Unlock()
	e.tg.mu.Lock()
	e.tg.loseAfter, e.tg.jitter = 10, 8*time.Millisecond
	e.tg.mu.Unlock()
	go e.b.BroadcastRun(ctx)
	if _, err := e.b.StartBroadcast(ctx, msg); err != nil {
		t.Fatal(err)
	}
	var j *plus.Broadcast
	e.wait("the pause", func() bool {
		j, _ = e.ex.Broadcasts.Job(ctx)
		return j != nil && j.State == plus.BcastRunning && j.Sent >= 10 && e.tg.count() >= 10
	})
	time.Sleep(300 * time.Millisecond)
	j, _ = e.ex.Broadcasts.Job(ctx)
	if j.State != plus.BcastRunning || j.Failed != 0 || j.Sent+j.Blocked >= j.Total {
		t.Fatalf("paused: %+v", j)
	}
	e.tg.setHolder(true)
	e.b.bc().mu.Lock()
	e.b.bc().holdAt = time.Time{}
	e.b.bc().mu.Unlock()
	e.b.bc().poke()
	e.wait("the end", func() bool { j, _ = e.ex.Broadcasts.Job(ctx); return j != nil && j.State == plus.BcastDone })
	got := map[int64]int{}
	for _, m := range e.tg.sent {
		c, _ := strconv.ParseInt(string(m["chat_id"].(json.Number)), 10, 64)
		got[c]++
	}
	for c, n := range got {
		if n != 1 {
			t.Errorf("chat %d got %d messages", c, n)
		}
	}
	if len(got) != j.Total || j.Sent != j.Total || j.Failed != 0 {
		t.Fatalf("after the pause: %d chats reached, job %+v", len(got), j)
	}
}

// Every press of a batch is answered at once, and once, however long the rest takes.
func TestPressesAreAnsweredOnce(t *testing.T) {
	e := newExtras(t)
	ctx := context.Background()
	e.ex.Menu.Use(treeMenu())
	press := func(n int, data string) tgate.Update {
		return tgate.Update{UpdateID: int64(n), CallbackQuery: &tgate.CallbackQuery{ID: "q" + strconv.Itoa(n), From: tgate.User{ID: 7},
			Data: data, Message: &tgate.Message{MessageID: 100, Chat: tgate.Chat{ID: 7, Type: "private"}}}}
	}
	ups := []tgate.Update{press(1, "k:help"), press(2, treeCode+":more:help"), press(3, freshCode+":m"), press(4, "ta:1:2")}
	e.b.AnswerAll(ctx, ups)
	e.wait("the answers", func() bool { e.tg.mu.Lock(); defer e.tg.mu.Unlock(); return len(e.tg.answers) == 4 })
	for _, up := range ups[:3] {
		e.b.Update(ctx, up)
	}
	time.Sleep(200 * time.Millisecond)
	e.tg.mu.Lock()
	defer e.tg.mu.Unlock()
	for id, n := range e.tg.answers {
		if n != 1 {
			t.Errorf("press %s answered %d times", id, n)
		}
	}
}
