package tgbot

import (
	"cmp"
	"context"
	"fmt"
	"html"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"time"

	"subsbot/internal/billing"
	"subsbot/internal/db"
	"subsbot/internal/domain"
)

// The bot's screens by the admin's templates: the whole text of a screen, and
// for a list one line of it. An empty template is mikan's screen as it is; the buttons stay
// mikan's either way.

// screenTpl is a template the page offers: Base is mikan's screen written as one, to start
// from.
type screenTpl struct {
	key, title, vars, hint, base string
}

const (
	tplSub         = "sub.screen"
	tplShopList    = "shop.list"
	tplShopItem    = "shop.item"
	tplTariff      = "shop.tariff"
	tplDevices     = "devices.screen"
	tplDeviceItem  = "devices.item"
	tplConnect     = "connect.screen"
	tplTraffic     = "traffic.screen"
	tplTrafficItem = "traffic.item"
)

// lineHint is said of every template.
const lineHint = "Пусто — как в mikan. Строка, где переменная оказалась пустой, не показывается; если строка списка в несколько строк, пункты списка разделяются пустой строкой. Разметка как в текстах панели: **жирный**, _курсив_, `код`."

var screenTemplates = []screenTpl{
	{tplSub, "«Подписка» — экран подписки", "{name} {state} {term} {until} {days} {traffic} {used} {limit} {left} {pools} {reset} {devices} {link} {brand}", "",
		"**Подписка «{name}»**\n{state}\n\n📅 {term}\n📦 {traffic}\n📦 {pools}\n🔄 Обновится: {reset}\n📱 Устройства: {devices}"},
	{tplShopList, "«Продлить» и «Купить подписку» — меню покупки", "{title} {tariffs} {brand}",
		"{title} — заголовок («Выберите тариф» или «Продление подписки…», меняется в «Покупка»), {tariffs} — тарифы, каждый по шаблону строки ниже.",
		"**{title}**\n\n{tariffs}"},
	{tplShopItem, "Меню покупки: как выглядит один тариф", "{tariff} {terms} {limits} {traffic} {devices} {speed} {price} {description}",
		"{terms} — сроки («30 дн. – 365 дн.»), {limits} — «1000 ГБ · устройств: 3», {price} — самая низкая цена («от 199 ₽»), {description} — «Описание для покупателей» тарифа в панели.",
		"• **{tariff}** — {terms} · {limits}\n{description}"},
	{tplTariff, "Выбранный тариф: срок и способ оплаты", "{tariff} {about} {term} {limits} {traffic} {devices} {speed} {price} {description} {question}",
		"{about} — что mikan пишет второй строкой (срок и лимиты), {question} — «На какой срок?» или «Как оплатить?».",
		"**{tariff}**\n{about}\n{description}\n\n{question}"},
	{tplDevices, "«Устройства» — экран", "{title} {devices} {list} {note} {wait}",
		"{devices} — «2 из 3», {list} — устройства по шаблону строки ниже, {note} — подсказка про отвязку, {wait} — когда можно отвязать снова или итог нажатия.",
		"{wait}\n\n**{title}** · {devices}\n\n{list}\n\n{note}"},
	{tplDeviceItem, "«Устройства»: как выглядит одно устройство", "{n} {name} {info} {model} {os} {app} {traffic} {seen}",
		"{info} — всё, что mikan пишет после имени: модель, система, приложение, трафик, когда было в сети.",
		"{n}. {name} — {info}"},
	{tplConnect, "«Подключить устройство» — экран", "{link} {name} {brand}", "",
		"**Подключить устройство**\n\n1. Установите приложение: Happ (iPhone, Android), ClashFest (Android), Koala Clash (Windows), SlothClash (Windows, Mac, Linux) или другое со страницы подписки.\n2. Добавьте в него ссылку:\n`{link}`\n\nНа странице подписки — кнопки «Добавить» для всех приложений."},
	{tplTraffic, "«Докупить трафик» — меню пакетов", "{title} {packages} {name}",
		"{title} — «Трафик для подписки…», {packages} — пакеты по шаблону строки ниже.",
		"**{title}**\n\n{packages}"},
	{tplTrafficItem, "Меню пакетов: как выглядит один пакет", "{package} {about} {size} {pool} {price}",
		"{about} — «10 ГБ на основной трафик», {pool} — пул пакета, {price} — цена.",
		"• **{package}** — {about}"},
}

func templateBase(key string) string {
	for _, t := range screenTemplates {
		if t.key == key {
			return t.base
		}
	}
	return ""
}

// tpl is the admin's template under key, "" none.
func (b *Bot) tpl(key string) string {
	if b.x == nil {
		return ""
	}
	return b.x.set.Get().Texts[key]
}

// templates are a screen's template and its line's, each mikan's when only the other is the
// admin's; ok is false when neither is.
func (b *Bot) templates(screen, item string) (string, string, bool) {
	s, i := b.tpl(screen), ""
	if item != "" {
		i = b.tpl(item)
	}
	if s == "" && i == "" {
		return "", "", false
	}
	return cmp.Or(s, templateBase(screen)), cmp.Or(i, templateBase(item)), true
}

var (
	varRe   = regexp.MustCompile(`\{([a-z_]+)\}`)
	blankRe = regexp.MustCompile(`\n{3,}`)
)

// fillTemplate is a template made the bot's HTML: a line with a variable that came out empty
// is left out, the variables are filled in as text, the lists (already HTML) as they are.
func fillTemplate(tpl string, vars, lists map[string]string) string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(tpl, "\r\n", "\n"), "\n") {
		drop := false
		for _, m := range varRe.FindAllStringSubmatch(line, -1) {
			v, isVar := vars[m[1]]
			l, isList := lists[m[1]]
			if isVar && strings.TrimSpace(v) == "" || isList && strings.TrimSpace(l) == "" {
				drop = true
				break
			}
		}
		if !drop {
			lines = append(lines, line)
		}
	}
	v := maps.Clone(vars)
	if v == nil {
		v = map[string]string{}
	}
	marks := map[string]string{}
	for name, list := range lists {
		// A mark no Markdown or escaping touches, swapped for the list after.
		mark := "PLUSxLIST" + strings.ReplaceAll(name, "_", "x") + "xEND"
		v[name], marks[mark] = mark, list
	}
	out := render(strings.Join(lines, "\n"), v)
	for mark, list := range marks {
		out = strings.ReplaceAll(out, mark, list)
	}
	return strings.Trim(blankRe.ReplaceAllString(out, "\n\n"), "\n ")
}

// joinItems joins the lines of a list: a line template of several lines makes blocks, which a
// blank line keeps apart.
func joinItems(item string, items []string) string {
	if strings.Contains(strings.TrimSpace(item), "\n") {
		return strings.Join(items, "\n\n")
	}
	return strings.Join(items, "\n")
}

// withNotice puts what a press did above a screen, as mikan's screens do.
func withNoticeHTML(notice, text string) string {
	if notice == "" {
		return text
	}
	return html.EscapeString(notice) + "\n\n" + text
}

// subScreenText is the subscription screen by the template, with the line about a new link
// mikan's screen has; ok false without a template.
func (b *Bot) subScreenText(ctx context.Context, w *words, userID int64, vars map[string]string, linkLine string) (string, bool) {
	tpl := b.tpl(tplSub)
	if tpl == "" {
		return "", false
	}
	v := maps.Clone(vars)
	v["pools"] = strings.Join(b.poolLines(ctx, w, userID), "\n")
	text := fillTemplate(tpl, v, nil)
	if linkLine != "" {
		text += "\n" + linkLine
	}
	return text, true
}

// tariffVars are a tariff's variables in the shop.
func (b *Bot) tariffVars(w *words, o billing.Offer, lang string) map[string]string {
	t := o.Tariff
	limits := billing.DescribeLimits(t, lang)
	v := map[string]string{
		"tariff": t.Name, "limits": limits, "price": w.cheapest(o), "description": t.Description,
		"terms":   strings.TrimSuffix(billing.DescribeOffer(o, lang), " · "+limits),
		"traffic": "без лимита", "devices": "", "speed": "",
	}
	if t.TrafficLimit.Valid {
		v["traffic"] = strconv.FormatInt(t.TrafficLimit.Int64>>30, 10) + " ГБ"
	}
	if t.DeviceLimit.Valid {
		v["devices"] = strconv.FormatInt(t.DeviceLimit.Int64, 10)
	}
	if t.SpeedLimit.Valid {
		v["speed"] = strconv.FormatInt(t.SpeedLimit.Int64, 10) + " Мбит/с"
	}
	return v
}

// shopListText is the list of tariffs (buying and renewing) by the templates.
func (b *Bot) shopListText(ctx context.Context, w *words, title, notice string, offers []billing.Offer) (string, bool) {
	screen, item, ok := b.templates(tplShopList, tplShopItem)
	if !ok {
		return "", false
	}
	lang := b.lang(ctx)
	items := make([]string, 0, len(offers))
	for _, o := range offers {
		items = append(items, fillTemplate(item, b.tariffVars(w, o, lang), nil))
	}
	list := joinItems(item, items)
	if len(offers) == 0 {
		list = html.EscapeString(w.payUnavailable)
	}
	text := fillTemplate(screen, map[string]string{"title": title, "brand": b.brand(ctx)}, map[string]string{"tariffs": list})
	return withNoticeHTML(notice, text), true
}

// tariffText is one tariff's screen by the template: its terms to pick (term nil) or the
// ways to pay for one term.
func (b *Bot) tariffText(ctx context.Context, w *words, o billing.Offer, term *billing.OfferTerm, question string) (string, bool) {
	tpl := b.tpl(tplTariff)
	if tpl == "" {
		return "", false
	}
	lang := b.lang(ctx)
	v := b.tariffVars(w, o, lang)
	v["question"], v["about"], v["term"] = question, v["limits"], v["terms"]
	if term != nil {
		v["about"] = billing.Describe(o.Tariff, term.Days, lang)
		v["term"] = billing.TermLabel(o.Tariff, term.Days, lang)
		v["price"] = w.cheapest(billing.Offer{Stars: term.Stars, Rub: term.Rub})
	}
	return fillTemplate(tpl, v, nil), true
}

// connectText is the screen to connect a device by the template.
func (b *Bot) connectText(ctx context.Context, u db.User) (string, bool) {
	tpl := b.tpl(tplConnect)
	if tpl == "" {
		return "", false
	}
	return fillTemplate(tpl, map[string]string{"link": b.subURL(ctx, u), "name": u.Name, "brand": b.brand(ctx)}, nil), true
}

// trafficText is the list of traffic packages by the templates.
func (b *Bot) trafficText(ctx context.Context, w *words, u db.User, offers []billing.PackageOffer, notice string) (string, bool) {
	screen, item, ok := b.templates(tplTraffic, tplTrafficItem)
	if !ok {
		return "", false
	}
	lang := b.lang(ctx)
	items := make([]string, 0, len(offers))
	for _, o := range offers {
		pool := o.Pool
		if pool == "" {
			pool = "основной трафик"
		}
		items = append(items, fillTemplate(item, map[string]string{
			"package": o.Package.Name, "about": billing.DescribePackage(o.Package, o.Pool, lang), "pool": pool,
			"size": w.bytes(o.Package.Bytes), "price": w.cheapest(billing.Offer{Stars: o.Stars, Rub: o.Rub}),
		}, nil))
	}
	list := joinItems(item, items)
	if len(offers) == 0 {
		list = html.EscapeString(w.payUnavailable)
	}
	text := fillTemplate(screen, map[string]string{"title": fmt.Sprintf(w.trafficTitle, u.Name), "name": u.Name}, map[string]string{"packages": list})
	return withNoticeHTML(notice, text), true
}

// devicesText is the list of a subscription's devices by the templates; what mikan's screen
// computed comes along.
func (b *Bot) devicesText(ctx context.Context, w *words, u db.User, devs []db.BoundDevice, used map[int64]int64,
	rules domain.UnbindState, waiting bool, notice string, now time.Time) (string, bool) {
	screen, item, ok := b.templates(tplDevices, tplDeviceItem)
	if !ok {
		return "", false
	}
	items := make([]string, 0, len(devs))
	for i, d := range devs {
		model, os := "", ""
		meta := []string{}
		if d.Name != "" && d.Model != "" {
			model = d.Model
			meta = append(meta, d.Model)
		}
		if d.Hwid != "" && d.Model != "" && d.Os != "" {
			os = strings.TrimSpace(d.Os + " " + d.OsVersion)
			meta = append(meta, os)
		}
		app, _, _ := strings.Cut(strings.TrimSpace(d.App), " ")
		app = strings.Replace(app, "/", " ", 1)
		if app != "" {
			meta = append(meta, app)
		}
		traffic := ""
		if n := used[d.ID]; n > 0 {
			traffic = w.bytes(n)
			meta = append(meta, traffic)
		}
		seen := w.ago(time.Unix(d.LastSeen, 0), now)
		meta = append(meta, seen)
		items = append(items, fillTemplate(item, map[string]string{
			"n": strconv.Itoa(i + 1), "name": deviceName(w, d), "info": strings.Join(meta, " · "),
			"model": cmp.Or(model, d.Model), "os": cmp.Or(os, strings.TrimSpace(d.Os+" "+d.OsVersion)), "app": app, "traffic": traffic, "seen": seen,
		}, nil))
	}
	list := joinItems(item, items)
	if len(devs) == 0 {
		list = html.EscapeString(w.devicesNone)
	}
	note := ""
	if len(devs) > 0 {
		parts := []string{w.devicesNote}
		if !domain.BindsDevices(u.DeviceMode) {
			parts = append(parts, linkWordsFor(w).onlineNote)
		}
		switch r := rules.Rules.ReturnHours; {
		case rules.Rules.Off:
			parts = append(parts, w.unbindOff)
		case r == domain.ReturnNever:
			parts = append(parts, w.returnNever)
		case r > 0:
			parts = append(parts, fmt.Sprintf(w.returnNote, w.span(r)))
		}
		if left := rules.Left(); left >= 0 && !waiting {
			parts = append(parts[:1], append([]string{fmt.Sprintf(w.unbindLeft, left, rules.Rules.Limit, w.period(rules.Rules.Days))}, parts[1:]...)...)
		}
		note = strings.Join(parts, " ")
	}
	wait := notice
	if wait == "" && !rules.Next.IsZero() && len(devs) > 0 {
		wait = fmt.Sprintf(w.wait, b.when(w, rules.Next))
	}
	count, _ := b.d.Backend.DeviceCount(ctx, u.ID)
	devices := strconv.FormatInt(count, 10)
	switch {
	case u.DeviceLimit.Valid && !domain.BindsDevices(u.DeviceMode):
		devices = fmt.Sprintf(linkWordsFor(w).onlineLimit, strconv.FormatInt(u.DeviceLimit.Int64, 10))
	case u.DeviceLimit.Valid:
		devices = fmt.Sprintf(w.trafficOf, devices, strconv.FormatInt(u.DeviceLimit.Int64, 10))
	}
	return fillTemplate(screen, map[string]string{"title": w.devicesTitle, "devices": devices, "note": note, "wait": wait},
		map[string]string{"list": list}), true
}
