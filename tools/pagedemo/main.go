// pagedemo serves the addon's settings page on its own, with the panel's store in memory:
// for working on the page (scratch harness: an iframe that forwards the calls, as mikan's
// admin panel does).
//
//	go run ./tools/pagedemo -listen 127.0.0.1:28480 -token testtoken
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"subsbot/internal/addon"
	"subsbot/internal/plus"
	"subsbot/internal/plus/plustest"
	"subsbot/internal/tgbot"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:28480", "where the page answers")
	token := flag.String("token", "testtoken", "the token the harness calls with")
	refused := flag.Bool("refused", false, "show the banner of a look Telegram refused")
	noUsers := flag.Bool("no-users-all", false, "the addon lacks the permission users:all")
	flag.BoolVar(&notHolder, "not-holder", false, "the bot is another addon's")
	seed := flag.String("kv", "", "a JSON file of store values to start with: {\"settings\": {...}, \"menu\": {...}}")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	kv, kvSrv := plustest.NewKV("p")
	kvSrv.Close()
	if *seed != "" {
		var vals map[string]json.RawMessage
		b, err := os.ReadFile(*seed)
		if err == nil {
			err = json.Unmarshal(b, &vals)
		}
		if err != nil {
			log.Error("seed", "err", err)
			os.Exit(2)
		}
		for k, v := range vals {
			kv.Set(k, v)
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/panel/v2/kv", kv)
	mux.Handle("/panel/v2/kv/", kv)
	perms := []string{"bot", "users:read", "users:write", "users:all", "billing:invoice", "events", "kv", "ui"}
	if *noUsers {
		perms = []string{"bot", "users:read", "users:write", "billing:invoice", "events", "kv", "ui"}
	}
	mux.HandleFunc("/panel/v2/self", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "subs-bot", "permissions": perms})
	})
	mux.HandleFunc("/panel/v2/users", func(w http.ResponseWriter, _ *http.Request) {
		if *noUsers {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var subs []plus.Sub
		for i := int64(1); i <= 40; i++ {
			tg := 1000 + i
			state := "active"
			if i%4 == 0 {
				state = "expired"
			}
			exp := time.Now().Add(time.Duration(i) * 24 * time.Hour)
			tariff := "Стандарт"
			if i%3 == 0 {
				tariff = "Премиум"
			}
			subs = append(subs, plus.Sub{ID: i, Name: fmt.Sprintf("user-%d", i), State: state, TariffName: tariff, ExpiresAt: &exp, TgID: &tg})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"users": subs, "next": nil})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base := srv.URL + "/panel/v2"

	dir, _ := os.MkdirTemp("", "pagedemo")
	defer os.RemoveAll(dir)
	state := filepath.Join(dir, "state.json")
	_ = os.WriteFile(state, []byte(fmt.Sprintf(`{"adapters":{"subs-bot":{"token":%q,"source":"external","status":"running"}}}`, *token)), 0o600)
	kvc := plus.NewKV(base, "p", nil)
	set := plus.NewStore(kvc, log)
	set.Check(tgbot.CheckTexts)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go set.Run(ctx)
	menu, auto := plus.NewMenu(kvc, log), plus.NewAuto(kvc, log, time.Now)
	go menu.Run(ctx)
	go auto.Run(ctx)
	chats := plus.NewChats(kvc, log, time.Now)
	_ = chats.Load(ctx)
	for i := int64(1); i <= 75; i++ {
		chats.Seen(1000+i, fmt.Sprintf("Имя %d", i), true)
	}
	chats.Blocked(1075)
	users := plus.NewUsers(base, "p", log)
	_ = users.Refresh(ctx)
	jobs := plus.NewBroadcasts(kvc)
	stats := plus.NewStats(kvc, log, time.Now)
	_ = stats.Load(ctx)
	for i := 0; i < 37; i++ {
		stats.Sent("m.paid_new")
		if i%3 == 0 {
			stats.Pressed("m.paid_new")
		}
	}
	stats.Sent("m.expire_1d")
	d := addon.Deps{ID: "subs-bot", State: state, Version: "demo", Set: set, Log: log, Texts: tgbot.TextEntries, AboutText: tgbot.DefaultAboutText,
		MenuButtons: func(context.Context) []plus.Button {
			return []plus.Button{{Key: "btn:c7", Name: "📰 Новости", Hint: "ваша кнопка-ссылка"}, {Key: "page:c8", Name: "📄 Правила", Hint: "ваша кнопка-страница"}}
		},
		Refused: func() (string, time.Time) {
			if *refused {
				return "telegram 400: Bad Request: DOCUMENT_INVALID", time.Now().Add(-3 * time.Minute)
			}
			return "", time.Time{}
		},
		Menu: menu, Auto: auto, Chats: chats, Users: users, Jobs: jobs, Vars: tgbot.Vars, Stats: stats,
		BuiltinDefaults: func(context.Context) map[string]string {
			out := map[string]string{}
			for _, m := range plus.Builtins {
				out[m.Key] = "Стандартный текст «" + m.Name + "» из панели.\nВторая строка."
			}
			return out
		},
		PanelNotify: func(context.Context) map[string]bool {
			return map[string]bool{"expire_3d": true, "expire_1d": true, "expired": false, "traffic_90": true, "traffic_100": true}
		},
		Bot: &demoBot{jobs: jobs, chats: chats, users: users, log: log}}
	log.Info("page", "listen", *listen)
	if err := addon.Serve(ctx, *listen, d); err != nil {
		log.Error("serve", "err", err)
	}
}

var notHolder bool

// demoBot stands in for the bot's broadcasts: a job "sends" ten messages a second.
type demoBot struct {
	jobs  *plus.Broadcasts
	chats *plus.Chats
	users *plus.Users
	log   *slog.Logger
}

func (b *demoBot) AudienceCounts() (map[string]int, bool) {
	total, blocked := b.chats.Counts()
	byTg, at, err := b.users.Snapshot()
	if err != nil || at.IsZero() {
		return map[string]int{"all": total - blocked}, false
	}
	out := map[string]int{"all": total - blocked}
	b.chats.Each(func(tg int64, ch plus.Chat) {
		if ch.Blocked != 0 {
			return
		}
		switch subs := byTg[tg]; {
		case plus.Has(subs):
			out["active"]++
		case len(subs) > 0:
			out["expired"]++
		default:
			out["none"]++
		}
	})
	return out, true
}

func (b *demoBot) StartBroadcast(ctx context.Context, j plus.Broadcast) (*plus.Broadcast, error) {
	if err := j.Valid(); err != nil {
		return nil, err
	}
	if cur, _ := b.jobs.Job(ctx); cur.Active() {
		return nil, fmt.Errorf("broadcast_busy")
	}
	counts, _ := b.AudienceCounts()
	j.ID, j.Created, j.Total = plus.NewID(time.Now().Unix()), time.Now().Unix(), counts[j.Audience]
	j.State, j.Started = plus.BcastRunning, time.Now().Unix()
	if j.At > time.Now().Unix() {
		j.State, j.Started = plus.BcastScheduled, 0
	}
	if err := b.jobs.Save(ctx, &j); err != nil {
		return nil, err
	}
	if j.State == plus.BcastRunning {
		go func(j plus.Broadcast) {
			for j.Sent+j.Blocked < j.Total {
				time.Sleep(100 * time.Millisecond)
				cur, _ := b.jobs.Job(context.Background())
				if cur == nil || cur.ID != j.ID || cur.State != plus.BcastRunning {
					return
				}
				if (j.Sent+j.Blocked)%17 == 16 {
					j.Blocked++
				} else {
					j.Sent++
				}
				_ = b.jobs.Save(context.Background(), &j)
			}
			j.State, j.Finished = plus.BcastDone, time.Now().Unix()
			_ = b.jobs.Done(context.Background(), &j)
		}(j)
	}
	return &j, nil
}

func (b *demoBot) CancelBroadcast(ctx context.Context) (*plus.Broadcast, error) {
	j, err := b.jobs.Job(ctx)
	if err != nil || !j.Active() {
		return j, err
	}
	j.State, j.Finished = plus.BcastCancelled, time.Now().Unix()
	return j, b.jobs.Done(ctx, j)
}

func (b *demoBot) TestMessage(ctx context.Context, chat int64, m plus.Message) error {
	b.log.Info("test message", "chat", chat, "text", m.Text)
	return nil
}

// Holds: the demo's bot is the addon's unless -not-holder.
func (b *demoBot) Holds(context.Context) bool { return !notHolder }
