// Command subs-bot is a subscribers' bot: mikan's official bot addon
// (cmd/mikan-telegram, see UPSTREAM for the release) taken out of mikan and made our own.
// It runs next to the panel as an addon of protocol 2 in client mode and sees the panel only
// through what the host hands it:
//
//	MIKAN_PANEL_URL, MIKAN_PANEL_TOKEN  the Panel API: the bot's data (POST /bot/{method})
//	                                     and the feed of events (payment messages, notices)
//	MIKAN_BOT_API_URL, MIKAN_BOT_TOKEN  the Bot API proxy: the updates and the sends; the
//	                                     panel keeps the real token and the payments
//
// or, installed as a process on the server (Addons → Own addon → Process on the server):
//
//	BOT_ADDON_STATE  the host's state file, /opt/mikan/addons/state.json: the tokens
//	                    and the address of the panel's API for addons are read from it
//	BOT_ADDON_ID     the addon's id there, subs-bot by default
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"subsbot/internal/tgate"
	"subsbot/internal/tgbot"
)

// version is set at build time (-ldflags -X main.version=...).
var version = "dev"

// addonID is the id the addon answers the panel with (/v2/info): the panel checks it against
// the catalog entry it installed, so a release build sets it to its entry's (-X main.addonID=...).
var addonID = "subs-bot"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("subs-bot starting", "version", version)
	page := pageOfEnv()
	run := func(ctx context.Context, ac access) {
		a := newAddon(ac.panelURL, ac.panelToken, ac.botURL, ac.botToken, log)
		a.plus(ac, page)
		a.Run(ctx)
	}
	// A process of the admin's own (source external) reads its tokens from the host's state;
	// a container gets them in its environment.
	if page.state != "" {
		watchState(ctx, page.state, page.id, 10*time.Second, log, run)
	} else {
		ac, err := accessOfEnv()
		if err != nil {
			log.Error("no access to the panel", "err", err)
			os.Exit(2)
		}
		run(ctx, ac)
	}
	log.Info("subs-bot stopped")
}

// addon is the bot's screens on the panel's data and the Bot API proxy.
type addon struct {
	log         *slog.Logger
	client      *tgate.Client
	out         *tgate.Outbox
	bot         *tgbot.Bot
	backend     *tgbot.Remote
	running     sync.WaitGroup
	updatesWait time.Duration
	// plus: what else runs beside the bot (plus.go): the settings, the addon's page.
	extra []func(ctx context.Context)
}

func newAddon(panelURL, panelToken, botURL, botToken string, log *slog.Logger) *addon {
	a := &addon{log: log, updatesWait: 25 * time.Second}
	a.client = tgate.NewClient(botURL, botToken, nil)
	a.out = tgate.NewOutbox(a.client, tgate.DefaultLimits, time.Now, func(int64) {},
		func(err error) { log.Warn("send", "err", err) })
	a.backend = tgbot.NewRemote(panelURL, panelToken, nil)
	a.bot = tgbot.New(tgbot.Deps{Backend: a.backend, Client: a.client, Outbox: a.out, Go: a.running.Go, Log: log, Now: time.Now})
	return a
}

// Run takes the updates and the events until ctx ends, and returns when all it started has.
func (a *addon) Run(ctx context.Context) {
	defer a.running.Wait()
	for _, f := range a.extra {
		a.running.Go(func() { f(ctx) })
	}
	a.running.Go(func() { a.out.Run(ctx) })
	a.running.Go(func() { a.setUp(ctx) })
	a.running.Go(func() { a.events(ctx) })
	a.updates(ctx)
}

// setUp sets the chats' commands and menu button, again until it goes through: the panel's
// gateway may not reach Telegram yet when the addon starts.
func (a *addon) setUp(ctx context.Context) {
	backoff := time.Second
	for {
		err := a.bot.Started(ctx)
		if err == nil || ctx.Err() != nil {
			return
		}
		a.log.Warn("set up the chats", "err", err)
		sleep(ctx, backoff)
		backoff = min(2*backoff, time.Minute)
	}
}

// updates reads the panel's buffer as Telegram's getUpdates: an update is acknowledged with
// the next call, after the screens took it.
func (a *addon) updates(ctx context.Context) {
	var offset int64
	backoff := time.Second
	refused := false // plus: the panel gave the bot to another addon
	for ctx.Err() == nil {
		ups, err := a.client.Updates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if notHolder(err) {
				if !refused {
					a.log.Info("the bot is another addon's: waiting until the admin gives it to this one")
				}
				refused = true
				// Asked again often: once the admin gives the bot over, the chats wait for it.
				sleep(ctx, min(backoff, 10*time.Second))
				backoff = min(2*backoff, time.Minute)
				continue
			}
			a.log.Warn("updates", "err", err)
			sleep(ctx, backoff)
			backoff = min(2*backoff, time.Minute)
			continue
		}
		if refused {
			// plus: the admin gave the bot to this addon: the chats' commands and menu
			// button are its to set now.
			a.log.Info("the bot is this addon's now")
			refused = false
			a.bot.Started(ctx)
		}
		backoff = time.Second
		for _, up := range ups {
			a.bot.Update(ctx, up)
			offset = up.UpdateID + 1
		}
	}
}

// events reads the feed of the events the bot acts on, and acknowledges what it handled.
func (a *addon) events(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		evs, err := a.backend.Events(ctx, a.updatesWait, tgbot.EventTypes)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.log.Warn("events", "err", err)
			sleep(ctx, backoff)
			backoff = min(2*backoff, time.Minute)
			continue
		}
		backoff = time.Second
		for _, e := range evs {
			if err := a.bot.HandleEvent(ctx, e); err != nil {
				// plus: the notices go to every bot addon, and only the one holding the bot
				// may read the subscriptions: the one that does tells them. Left
				// unacknowledged they would all go out late once the bot is this one's.
				if !a.holdsBot(ctx) {
					if err := a.backend.Ack(ctx, e.ID); err != nil {
						a.log.Warn("ack", "id", e.ID, "err", err)
						break
					}
					continue
				}
				// Not acknowledged: it comes again with the next pull.
				a.log.Warn("event", "id", e.ID, "type", e.Type, "err", err)
				sleep(ctx, 5*time.Second)
				break
			}
			if err := a.backend.Ack(ctx, e.ID); err != nil {
				a.log.Warn("ack", "id", e.ID, "err", err)
				break
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// notHolder: the Bot API proxy turned the addon away, as it does every addon but the one the
// admin gave the bot to.
func notHolder(err error) bool {
	var ae *tgate.APIError
	return errors.As(err, &ae) && ae.Code == 401
}

// holdsBot asks the Bot API proxy whether the bot is this addon's.
func (a *addon) holdsBot(ctx context.Context) bool {
	_, err := a.client.Me(ctx)
	return !notHolder(err)
}
