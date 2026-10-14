package main

import (
	"cmp"
	"context"
	"os"
	"time"

	addonpage "subsbot/internal/addon"
	"subsbot/internal/plus"
	"subsbot/internal/tgbot"
)

// pageConf is where the addon answers the panel (its settings page) and how it knows the
// panel's calls.
type pageConf struct {
	state  string // the host's state file (a process on the server): tokens are read there
	id     string // the addon's id in it
	listen string // where the panel calls the addon; "" serves no page
	token  string // the token the panel calls with, when the environment gives it
}

// pageOfEnv: a process on the server names the host's state (BOT_ADDON_STATE) and listens
// on BOT_LISTEN, 127.0.0.1:18490 by default; a container gets MIKAN_ADAPTER_LISTEN and
// MIKAN_ADAPTER_TOKEN from the host.
func pageOfEnv() pageConf {
	p := pageConf{state: os.Getenv("BOT_ADDON_STATE"), id: cmp.Or(os.Getenv("BOT_ADDON_ID"), addonID)}
	if p.state != "" {
		p.listen = cmp.Or(os.Getenv("BOT_LISTEN"), "127.0.0.1:18490")
		return p
	}
	p.listen, p.token = os.Getenv("MIKAN_ADAPTER_LISTEN"), os.Getenv("MIKAN_ADAPTER_TOKEN")
	if p.token == "" {
		p.listen = ""
	}
	return p
}

// plus turns on what the bot has on top of mikan's: the settings, the admin's menu and
// automatic messages from the panel's store, the list of chats, the broadcasts, the look they
// give what the bot sends, and the settings page.
func (a *addon) plus(ac access, page pageConf) {
	kv := plus.NewKV(ac.panelURL, ac.panelToken, nil)
	set := plus.NewStore(kv, a.log)
	a.bot.Plus(set, a.log)
	ex := tgbot.Extras{
		Menu:       plus.NewMenu(kv, a.log),
		Auto:       plus.NewAuto(kv, a.log, time.Now),
		Chats:      plus.NewChats(kv, a.log, time.Now),
		Users:      plus.NewUsers(ac.panelURL, ac.panelToken, a.log),
		Broadcasts: plus.NewBroadcasts(kv),
		Stats:      plus.NewStats(kv, a.log, time.Now),
	}
	a.bot.PlusExtras(ex)
	a.extra = append(a.extra, set.Run, ex.Menu.Run, ex.Auto.Run, ex.Chats.Run, ex.Stats.Run, a.bot.AutoRun, a.bot.BroadcastRun,
		func(ctx context.Context) {
			// The panel's subscribers every ten minutes: who has a subscription, for the
			// broadcasts; and their accounts join the list of chats.
			ex.Users.Run(ctx, 10*time.Minute, func(byTg map[int64][]plus.Sub) {
				tgs := make([]int64, 0, len(byTg))
				for tg := range byTg {
					tgs = append(tgs, tg)
				}
				if n := ex.Chats.Seed(tgs); n > 0 {
					a.log.Info("chats: accounts of the panel's subscribers added", "n", n)
				}
			})
		})
	if page.listen == "" {
		return
	}
	d := addonpage.Deps{ID: page.id, State: page.state, Token: page.token, Version: version, Set: set, Log: a.log,
		MenuButtons: a.bot.MenuButtons, Refused: a.bot.LastRefused, Texts: tgbot.TextEntries,
		Saved:     func(ctx context.Context) { _ = a.bot.Started(ctx) },
		AboutText: tgbot.DefaultAboutText,
		Menu:      ex.Menu, Auto: ex.Auto, Chats: ex.Chats, Users: ex.Users, Bot: a.bot, Jobs: ex.Broadcasts, Vars: tgbot.Vars,
		Stats: ex.Stats, BuiltinDefaults: a.bot.BuiltinDefaults, PanelNotify: a.bot.PanelNotify}
	a.extra = append(a.extra, func(ctx context.Context) {
		if err := addonpage.Serve(ctx, page.listen, d); err != nil {
			a.log.Error("the settings page", "listen", page.listen, "err", err)
		}
	})
}
