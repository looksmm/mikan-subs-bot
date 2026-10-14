package plus_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"subsbot/internal/plus"
	"subsbot/internal/plus/plustest"
)

func act(label, do, to string) plus.Action { return plus.Action{Label: label, Do: do, To: to} }

// goodMenu: a main menu with a section, a welcome screen, a link.
func goodMenu() plus.Menu {
	return plus.Menu{On: true, Main: "main", Welcome: "hello", Screens: []plus.Screen{
		{ID: "main", Name: "Главное", Rows: [][]plus.Action{
			{act("📊 Подписка", "sub", ""), act("📱 Устройства", "devices", "")},
			{act("📚 Инструкции", "screen", "help")},
		}},
		{ID: "help", Name: "Инструкции", Text: "Выберите устройство", Back: true, Rows: [][]plus.Action{
			{{Label: "iPhone", Do: "url", URL: "https://example.com/ios", Icon: "5134263008683362081", Style: "primary"}},
		}},
		{ID: "hello", Name: "Первый экран", Text: "Привет, {first_name}!", Rows: [][]plus.Action{
			{act("🎁 Попробовать", "trial", ""), act("💳 Купить", "buy", "")},
		}},
	}}
}

func TestMenuValid(t *testing.T) {
	m := goodMenu()
	m.Normalize()
	if err := m.Valid(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		edit  func(m *plus.Menu)
		field string
	}{
		{"on without a main screen", func(m *plus.Menu) { m.Main = "" }, "main"},
		{"a main screen that is not there", func(m *plus.Menu) { m.Main = "nope" }, "main"},
		{"a welcome that is not there", func(m *plus.Menu) { m.Welcome = "nope" }, "welcome"},
		{"two screens of one id", func(m *plus.Menu) { m.Screens[1].ID = "main" }, "screens.1.id"},
		{"an id with capitals", func(m *plus.Menu) { m.Screens[1].ID = "Help" }, "screens.1.id"},
		{"a button to nowhere", func(m *plus.Menu) { m.Screens[0].Rows[1][0].To = "gone" }, "screens.0.rows.1.0.to"},
		{"a link without https", func(m *plus.Menu) { m.Screens[1].Rows[0][0].URL = "http://example.com" }, "screens.1.rows.0.0.url"},
		{"a doing nobody knows", func(m *plus.Menu) { m.Screens[0].Rows[0][0].Do = "fly" }, "screens.0.rows.0.0.do"},
		{"a colour nobody knows", func(m *plus.Menu) { m.Screens[1].Rows[0][0].Style = "pink" }, "screens.1.rows.0.0.style"},
		{"an icon that is not a number", func(m *plus.Menu) { m.Screens[1].Rows[0][0].Icon = "abc" }, "screens.1.rows.0.0.icon"},
		{"four in a row", func(m *plus.Menu) {
			m.Screens[0].Rows[0] = append(m.Screens[0].Rows[0], act("a", "menu", ""), act("b", "menu", ""))
		}, "screens.0.rows.0"},
		{"a long label", func(m *plus.Menu) { m.Screens[0].Rows[0][0].Label = strings.Repeat("я", 41) }, "screens.0.rows.0.0.label"},
	} {
		m := goodMenu()
		c.edit(&m)
		m.Normalize()
		err := m.Valid()
		var fe *plus.FieldError
		if !errors.As(err, &fe) || fe.Field != c.field {
			t.Errorf("%s: %v, want field %s", c.name, err, c.field)
		}
	}
	// Off, it may be half made.
	m = goodMenu()
	m.On, m.Main = false, ""
	if err := m.Valid(); err != nil {
		t.Fatalf("off: %v", err)
	}
	// Empty rows and buttons are dropped, not refused; a link's target goes from a section.
	m = goodMenu()
	m.Screens[0].Rows = append(m.Screens[0].Rows, []plus.Action{{}}, nil)
	m.Screens[0].Rows[0][0].URL = "https://x.example"
	m.Normalize()
	if len(m.Screens[0].Rows) != 2 || m.Screens[0].Rows[0][0].URL != "" {
		t.Fatalf("normalize: %+v", m.Screens[0].Rows)
	}
}

func kvFor(t *testing.T) (*plustest.KV, *plus.KV) {
	t.Helper()
	fake, srv := plustest.NewKV("tok")
	t.Cleanup(srv.Close)
	return fake, plus.NewKV(srv.URL+"/panel/v2", "tok", nil)
}

func TestAutoKeepsWhenARuleWasMade(t *testing.T) {
	_, kv := kvFor(t)
	clock := time.Unix(1_800_000_000, 0)
	doc := plus.NewAuto(kv, quiet(), func() time.Time { return clock })
	rule := plus.Rule{ID: "r1", Name: "Привет", On: true, Trigger: "start", Delay: 60, Message: plus.Message{Text: "Привет!",
		Rows: [][]plus.Action{{act("Купить", "buy", "")}}}}
	saved, err := doc.Save(context.Background(), plus.Auto{Rules: []plus.Rule{rule}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Rules[0].Created != clock.Unix() {
		t.Fatalf("created %d", saved.Rules[0].Created)
	}
	// Saved again later (the page sends what it has, Created or not): the time stays; a new
	// rule gets the new time.
	clock = clock.Add(time.Hour)
	rule.Created = 0
	two := rule
	two.ID = "r2"
	saved, err = doc.Save(context.Background(), plus.Auto{Rules: []plus.Rule{rule, two}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Rules[0].Created != clock.Add(-time.Hour).Unix() || saved.Rules[1].Created != clock.Unix() {
		t.Fatalf("created %d %d", saved.Rules[0].Created, saved.Rules[1].Created)
	}
	// Read back by another run of the bot.
	other := plus.NewAuto(kv, quiet(), time.Now)
	if err := other.Load(context.Background()); err != nil || len(other.Get().Rules) != 2 {
		t.Fatalf("load: %v %+v", err, other.Get())
	}
	for _, c := range []struct {
		name string
		edit func(r *plus.Rule)
	}{
		{"no trigger", func(r *plus.Rule) { r.Trigger = "" }},
		{"a delay past 60 days", func(r *plus.Rule) { r.Delay = 61 * 24 * 60 }},
		{"neither text nor picture", func(r *plus.Rule) { r.Message.Text = "" }},
		{"a picture over http", func(r *plus.Rule) { r.Message.Photo = "http://example.com/a.png" }},
		{"a long caption", func(r *plus.Rule) {
			r.Message.Photo, r.Message.Text = "https://example.com/a.png", strings.Repeat("я", 1001)
		}},
		{"days other than 1 and 3", func(r *plus.Rule) { r.Trigger, r.Days = "expiring", 2 }},
	} {
		r := rule
		c.edit(&r)
		if _, err := doc.Save(context.Background(), plus.Auto{Rules: []plus.Rule{r}}, nil); err == nil {
			t.Errorf("%s: saved", c.name)
		}
	}
}

func TestChatsRoundTrip(t *testing.T) {
	fake, kv := kvFor(t)
	clock := time.Unix(1_800_000_000, 0)
	now := func() time.Time { return clock }
	c := plus.NewChats(kv, quiet(), now)
	if err := c.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.Seen(1001, "Аня", true) || c.Seen(1001, "Аня", false) {
		t.Fatal("new is told once")
	}
	c.Seen(1002, "Борис\nx", false)
	c.Blocked(1003)
	if n := c.Seed([]int64{1001, 1004, 1005}); n != 2 {
		t.Fatalf("seeded %d", n)
	}
	c.MarkSent(1001, "r1", 42)
	c.AddDue(1002, plus.Due{Rule: "r2", At: clock.Unix() + 60, User: 7, Event: 9})
	c.AddDue(1002, plus.Due{Rule: "r2", At: clock.Unix() + 120, User: 7, Event: 10}) // replaces the first
	c.Flush(context.Background())
	if fake.Puts == 0 {
		t.Fatal("nothing written")
	}

	// Another run reads it all back.
	d := plus.NewChats(kv, quiet(), now)
	if err := d.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, _ := d.Get(1001)
	b, _ := d.Get(1002)
	old, _ := d.Get(1004)
	if !a.Start || a.First != clock.Unix() || a.Name != "Аня" || a.Sent["r1"] != 42 {
		t.Fatalf("1001: %+v", a)
	}
	if b.Start || b.Name != "Борисx" || len(b.Due) != 1 || b.Due[0].Event != 10 {
		t.Fatalf("1002: %+v", b)
	}
	if old.First != 0 || old.Start {
		t.Fatalf("a subscriber's account is no newcomer: %+v", old)
	}
	if total, blocked := d.Counts(); total != 5 || blocked != 1 {
		t.Fatalf("counts %d %d", total, blocked)
	}
	// An old customer who writes is not made a newcomer.
	d.Seen(1004, "", true)
	if old, _ = d.Get(1004); old.Start {
		t.Fatalf("seeded then /start: %+v", old)
	}
	// Writing again unblocks.
	d.Seen(1003, "", false)
	if x, _ := d.Get(1003); x.Blocked != 0 {
		t.Fatalf("unblocked: %+v", x)
	}
	if due := d.TakeDue(clock.Add(time.Minute)); len(due) != 0 {
		t.Fatalf("early: %+v", due)
	}
	if due := d.TakeDue(clock.Add(3 * time.Minute)); len(due) != 1 || due[0].TgID != 1002 {
		t.Fatalf("due: %+v", due)
	}
	if due := d.TakeDue(clock.Add(time.Hour)); len(due) != 0 {
		t.Fatalf("taken twice: %+v", due)
	}

	// A write of another run meanwhile is merged, not lost: 1006 and 1262 share a part of the
	// list (1262 = 1006 + 256).
	c.Seen(1006, "Вера", true)
	d.Seen(1262, "Глеб", true)
	d.Flush(context.Background())
	c.Flush(context.Background()) // conflict: reads d's and merges
	c.Flush(context.Background())
	e := plus.NewChats(kv, quiet(), now)
	if err := e.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tg := range []int64{1001, 1006, 1262} {
		if _, ok := e.Get(tg); !ok {
			t.Errorf("%d lost", tg)
		}
	}
	// Removed rules' marks go.
	e.Forget(func(rule string) bool { return rule != "r1" })
	if x, _ := e.Get(1001); x.Sent["r1"] != 0 {
		t.Fatalf("forget: %+v", x)
	}
}

// fakeUsers is the panel's /users for the users:all permission, and /self.
type fakeUsers struct {
	mu    sync.Mutex
	subs  []plus.Sub
	perms []string
}

func (f *fakeUsers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/panel/v2/self" {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "bot", "version": "1", "permissions": f.perms})
		return
	}
	has := false
	for _, p := range f.perms {
		has = has || p == "users:all"
	}
	if !has {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"forbidden"}`))
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
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	needle := q.Get("q")
	var page []plus.Sub
	var next *int64
	for _, s := range f.subs {
		if s.ID <= after {
			continue
		}
		if needle != "" && !strings.Contains(s.Name, needle) && (s.TgID == nil || strconv.FormatInt(*s.TgID, 10) != needle) {
			continue
		}
		if len(page) == limit {
			id := page[len(page)-1].ID
			next = &id
			break
		}
		page = append(page, s)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"users": page, "next": next})
}

func sub(id int64, name, state string, tg int64) plus.Sub {
	s := plus.Sub{ID: id, Name: name, State: state}
	if tg != 0 {
		s.TgID = &tg
	}
	return s
}

func TestUsersReadsEveryPage(t *testing.T) {
	f := &fakeUsers{perms: []string{"users:all"}}
	for i := int64(1); i <= 250; i++ {
		f.subs = append(f.subs, sub(i, "user"+strconv.FormatInt(i, 10), "active", 5000+i%100))
	}
	f.subs = append(f.subs, sub(300, "5003 in the name", "expired", 0))
	srv := httptest.NewServer(f)
	defer srv.Close()
	u := plus.NewUsers(srv.URL+"/panel/v2", "tok", quiet())
	if err := u.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	byTg, at, err := u.Snapshot()
	if err != nil || at.IsZero() || len(byTg) != 100 || len(byTg[5003]) < 2 {
		t.Fatalf("snapshot: %v %d %d", err, len(byTg), len(byTg[5003]))
	}
	// A search by an account: only the linked ones, not a name with its digits.
	mine, err := u.OfTg(context.Background(), 5003)
	if err != nil || len(mine) != 3 {
		t.Fatalf("of tg: %v %d", err, len(mine))
	}
	if s, err := u.One(context.Background(), 300); err != nil || s.State != "expired" || s.Works() {
		t.Fatalf("one: %v %+v", err, s)
	}
	if _, err := u.One(context.Background(), 999); !errors.Is(err, plus.ErrNotFound) {
		t.Fatalf("one missing: %v", err)
	}
	// Without the permission: told so, not a failure of the panel.
	f.mu.Lock()
	f.perms = []string{"kv"}
	f.mu.Unlock()
	if err := u.Refresh(context.Background()); !errors.Is(err, plus.ErrNoUsersAll) {
		t.Fatalf("no permission: %v", err)
	}
	if self, err := u.Self(context.Background()); err != nil || len(self.Permissions) != 1 {
		t.Fatalf("self: %v %+v", err, self)
	}
}

func TestBroadcastJobKeepsItsRecipients(t *testing.T) {
	fake, kv := kvFor(t)
	st := plus.NewBroadcasts(kv)
	j := &plus.Broadcast{ID: plus.NewID(1), Audience: "all", State: plus.BcastRunning, Message: plus.Message{Text: "Новости"}}
	var to []int64
	for i := int64(1); i <= 9000; i++ {
		to = append(to, i)
	}
	if err := st.SetRecipients(context.Background(), j, to); err != nil {
		t.Fatal(err)
	}
	if j.Total != 9000 || j.Chunks != 3 {
		t.Fatalf("chunks: %+v", j)
	}
	last, err := st.Recipients(context.Background(), j, 2)
	if err != nil || len(last) != 1000 || last[999] != 9000 {
		t.Fatalf("last chunk: %v %d", err, len(last))
	}
	if err := st.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	j.State, j.Sent = plus.BcastDone, 9000
	if err := st.Done(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if fake.Value("broadcast/to/"+j.ID+"/0") != nil {
		t.Fatal("recipients left behind")
	}
	h, err := st.History(context.Background())
	if err != nil || len(h) != 1 || h[0].Sent != 9000 {
		t.Fatalf("history: %v %+v", err, h)
	}
	cur, err := st.Job(context.Background())
	if err != nil || cur.Active() || cur.State != plus.BcastDone {
		t.Fatalf("job: %v %+v", err, cur)
	}
}
