package tgbot

import (
	"cmp"
	"context"
	"html"

	"subsbot/internal/plus"
	"subsbot/internal/tgate"
)

// «О сервисе»: the admin's documents (the privacy policy, the agreement)
// leave the main menu and the welcome screen for a screen of their own, whose button sits
// before «Поддержка» in its row.

// aboutCode is the callback of the «О сервисе» button.
const aboutCode = "ab"

// DefaultAboutText is the screen's text when the admin wrote none.
const DefaultAboutText = "**О сервисе**\n\n{brand} — быстрый VPN без ограничений: подключение в одно нажатие на телефоне и компьютере.\n\nПравила сервиса и то, как мы обращаемся с данными, — по кнопкам ниже."

func (b *Bot) about() (plus.About, bool) {
	if b.x == nil {
		return plus.About{}, false
	}
	a := b.x.set.Get().About
	return a, a.On
}

// menuConfig is cfg as the menu and the welcome screen lay it out: the buttons that live in
// «О сервисе» are taken out and its button put in, before «Поддержка» or where they were.
func (b *Bot) menuConfig(cfg Config) Config {
	a, on := b.about()
	if !on {
		return cfg
	}
	moved := map[string]bool{}
	for _, it := range a.Items {
		moved[it.ID] = true
	}
	btn := MenuButton{ID: "about", Action: "about", Label: cmp.Or(a.Label, plus.DefaultAboutLabel), On: true}
	out := cfg
	out.Buttons = make([]MenuButton, 0, len(cfg.Buttons)+1)
	placed := false
	for _, mb := range cfg.Buttons {
		switch {
		case moved[mb.ID]:
			if !placed && !a.WithSupport {
				out.Buttons = append(out.Buttons, btn)
				placed = true
			}
			continue
		case !placed && a.WithSupport && mb.Action == "support" && mb.On:
			// A row of their own, «О сервисе» first.
			mb.Row = true
			out.Buttons = append(out.Buttons, btn)
			placed = true
		}
		out.Buttons = append(out.Buttons, mb)
	}
	if !placed {
		out.Buttons = append(out.Buttons, btn)
	}
	return out
}

// welcomeRows are the welcome screen's rows with the admin's links and pages under them;
// «О сервисе» goes before «Поддержка» in its row when the menu has it there.
func (b *Bot) welcomeRows(ctx context.Context, cfg Config, rows [][]tgate.Button) [][]tgate.Button {
	extra := b.welcomeButtons(cfg)
	a, on := b.about()
	sup := b.supportURL(ctx)
	if !on || !a.WithSupport || sup == "" {
		return append(rows, extra...)
	}
	for i, row := range rows {
		if len(row) == 0 || row[len(row)-1].URL != sup || len(row) >= 3 {
			continue
		}
		for j, r := range extra {
			for k, btn := range r {
				if btn.CallbackData != aboutCode {
					continue
				}
				rows[i] = append([]tgate.Button{btn}, row...)
				r = append(r[:k:k], r[k+1:]...)
				if len(r) == 0 {
					extra = append(extra[:j:j], extra[j+1:]...)
				} else {
					extra[j] = r
				}
				return append(rows, extra...)
			}
		}
	}
	return append(rows, extra...)
}

// aboutScreen is «О сервисе»: its text, and the admin's documents two to a row.
func (b *Bot) aboutScreen(ctx context.Context, cfg Config, w *words, notice string) (string, *tgate.Keyboard, bool) {
	a, on := b.about()
	if !on {
		return "", nil, false
	}
	text := render(cmp.Or(a.Text, DefaultAboutText), map[string]string{"brand": b.brand(ctx)})
	if notice != "" {
		text = html.EscapeString(notice) + "\n\n" + text
	}
	byID := map[string]MenuButton{}
	for _, mb := range cfg.Buttons {
		byID[mb.ID] = mb
	}
	var rows [][]tgate.Button
	for _, it := range a.Items {
		mb, ok := byID[it.ID]
		if !ok || !mb.On {
			continue
		}
		label := cmp.Or(it.Label, mb.Label)
		var btn tgate.Button
		switch mb.Action {
		case "url":
			btn = tgate.Button{Text: label, URL: mb.URL}
		case "page":
			btn = tgate.Button{Text: label, CallbackData: "p:" + mb.ID}
		default:
			continue
		}
		if len(rows) > 0 && len(rows[len(rows)-1]) < 2 {
			rows[len(rows)-1] = append(rows[len(rows)-1], btn)
		} else {
			rows = append(rows, []tgate.Button{btn})
		}
	}
	rows = append(rows, []tgate.Button{{Text: w.back, CallbackData: "m"}})
	return text, &tgate.Keyboard{InlineKeyboard: rows}, true
}

// aboutHas: the admin's button id lives in «О сервисе», so its page opens from there for
// someone without a subscription too.
func (b *Bot) aboutHas(id string) bool {
	a, on := b.about()
	if !on {
		return false
	}
	for _, it := range a.Items {
		if it.ID == id {
			return true
		}
	}
	return false
}
