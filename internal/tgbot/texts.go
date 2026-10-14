package tgbot

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// The bot's own words in both languages. The admin's texts replace the ones marked
// "admin" when set.
type words struct {
	welcome, main, renew, expiring, expired, traffic90, trafficEnd string // admin
	channel, freeButton                                            string // admin: the channel's screen, the free plan's button

	back, yesUnbind, cancel, subscriptions, openPage, support, promo   string
	subTitle, devicesTitle, connectTitle, switchTitle                  string
	stateActive, stateExpiring, stateLimited, stateExpired, stateOff   string
	forever, termUntil, noLimit, trafficOf, trafficNoLimit, resets     string
	devicesNone, devicesOff, devicesNote, confirmUnbind, unbound, wait string
	// The admin's unbind rules: what is left in the window, the pause of an unbound device.
	unbindLeft, returnNote, confirmReturn, unbindFailed          string
	unbindOff, returnNever, confirmNever                         string
	perDay, perWeek, perMonth, perDays, hours                    string
	connectText, linked, alreadyLinked, linkExpired, linkInvalid string
	linkLimit, noSub, sharedPlace, device, justNow               string
	// Taking a subscription off the Telegram account: it keeps working in the apps.
	removeSub, confirmRemove, yesRemove, removed string
	// A subscription already linked to another account is moved only if its owner agrees.
	transferAsk, transferAllow, transferDeny, transferAsked, transferBusy, transferDone string
	transferDenied, transferDeniedNew, transferStale, transferNotice                    string
	minAgo, hoursAgo, yesterday, daysAgo                                                string
	months                                                                              [12]string
	commands                                                                            string
	// menuApp is the Mini App button next to the input field. It shares the row with the
	// field: a long one leaves no room to type on a phone.
	menuApp string

	// The shop.
	buy, buyTitle, renewTitle, payHow, payStars, payCard, payCrypto, payAddon, payButton, invoice, payNew, payRenew string
	notForSale, payUnavailable, tooManyInvoices, paidNew, paidRenew                                                 string
	trial, trialDone, trialUsed, trialOff, trialOpen, trialFail                                                     string
	trialPaid, trialTitle                                                                                           string // the priced trial: its button with the price, its title over the ways to pay
	trialNotGiven                                                                                                   string // a paid trial not given, its money back by hand
	pickTerm, priceFrom                                                                                             string
	poolOut                                                                                                         string // a traffic pool used up

	// The channel a subscriber must join, and the free plan.
	channelJoin, channelCheck, channelNotYet             string
	freeDone, freeActive, freeOff, freeFail, freeChannel string

	// Traffic packages.
	buyTraffic, trafficTitle, payPackage, packageGone, paidPackage, plusPackages string

	// A refund takes back what the payment gave.
	refundedNew, refundedRenew, refundedPackage, refundedGone string
}

var ru = words{
	welcome:    "Это бот {brand}.\n\nЧтобы управлять подпиской, откройте её страницу и нажмите «Открыть в Telegram» — или пришлите сюда ссылку на подписку.",
	main:       "{brand} · {name}\n\n{state}\n📅 {term}\n📦 {traffic}\n📱 Устройства: {devices}",
	renew:      "Чтобы продлить подписку, напишите в поддержку — вам подскажут, как оплатить.",
	expiring:   "⏳ Подписка «{name}» заканчивается {until}: осталось {days}.",
	expired:    "⛔️ Подписка «{name}» закончилась. Чтобы продлить, напишите в поддержку.",
	traffic90:  "📦 Израсходовано 90% трафика подписки «{name}»: осталось {left}.",
	trafficEnd: "📦 Трафик подписки «{name}» на этот период закончился. Обновится {reset}.",
	channel:    "Чтобы пользоваться ботом {brand}, подпишитесь на наш канал. Потом нажмите «Проверить».",
	freeButton: "🎁 Бесплатный тариф",

	back: "⬅️ Назад", promo: "🎟 Промокоды", yesUnbind: "✅ Да, отвязать", cancel: "↩️ Отмена", subscriptions: "🔁 Подписки", openPage: "🌐 Открыть страницу подписки", support: "💬 Поддержка",
	subTitle: "Подписка «%s»", devicesTitle: "Устройства", connectTitle: "Подключить устройство", switchTitle: "Какую подписку показать?",
	stateActive: "✅ Работает", stateExpiring: "⏳ Скоро закончится", stateLimited: "📦 Трафик на этот период закончился", stateExpired: "⛔️ Подписка закончилась", stateOff: "⏸ Доступ приостановлен",
	forever: "бессрочно", termUntil: "до %s — осталось %s", noLimit: "без лимита", trafficOf: "%s из %s", trafficNoLimit: "%s, без лимита", resets: "🔄 Обновится: %s",
	devicesNone:   "Устройства появятся здесь, когда приложение загрузит подписку.",
	devicesOff:    "Привязка устройств выключена: одновременно можно подключаться не больше чем с %s адресов.",
	devicesNote:   "Отвязанное устройство сразу отключается, а место освобождается.",
	confirmUnbind: "Отвязать «%s»? Оно сразу отключится.",
	unbindLeft:    "Можно отвязать ещё %d из %d за %s.",
	returnNote:    "Отвязанное устройство не сможет подключиться снова %s, поэтому сначала закройте на нём приложение.",
	confirmReturn: "Подключиться снова оно сможет только через %s: сначала закройте на нём приложение, иначе оно сразу попробует вернуться.",
	unbindFailed:  "⚠️ Не получилось отвязать устройство. Попробуйте ещё раз чуть позже.",
	unbindOff:     "Отвязать устройство может только администратор: напишите в поддержку.",
	returnNever:   "Отвязанное устройство подключится снова, только если разрешит администратор.",
	confirmNever:  "Подключиться снова оно сможет, только если разрешит администратор: сначала закройте на нём приложение.",
	perDay:        "сутки", perWeek: "неделю", perMonth: "месяц", perDays: "%s", hours: "%d ч",
	unbound:           "✅ «%s» отвязано.",
	removeSub:         "🗑 Убрать из бота",
	confirmRemove:     "Убрать подписку «%s» из этого Telegram?\n\nОна продолжит работать в приложениях, но уведомления о ней сюда приходить перестанут. Чтобы вернуть её, пришлите сюда ссылку на подписку.",
	yesRemove:         "✅ Да, убрать",
	removed:           "✅ Подписка «%s» убрана из бота.",
	wait:              "⏳ Следующее устройство можно отвязать %s.",
	connectText:       "1. Установите приложение: Happ (iPhone, Android), ClashFest (Android), Koala Clash (Windows), SlothClash (Windows, Mac, Linux) или другое со страницы подписки.\n2. Добавьте в него ссылку:\n<code>%s</code>\n\nНа странице подписки — кнопки «Добавить» для всех приложений.",
	linked:            "✅ Подписка «%s» подключена.",
	alreadyLinked:     "Подписка «%s» уже здесь.",
	linkExpired:       "Ссылка устарела. Обновите страницу подписки и нажмите «Открыть в Telegram» ещё раз.",
	linkInvalid:       "Ссылка не подошла. Откройте страницу подписки и нажмите «Открыть в Telegram».",
	linkLimit:         "К одному аккаунту можно подключить не больше %d подписок.",
	transferAsk:       "Подписку «%s» хотят подключить к другому аккаунту Telegram (%s). Если это вы, разрешите. Если нет, откажите: подписка останется у вас.",
	transferAllow:     "✅ Разрешить",
	transferDeny:      "❌ Отказать",
	transferAsked:     "Эта подписка уже подключена к другому аккаунту. Мы спросили владельца: когда он разрешит, подписка появится здесь. Запрос действует час.",
	transferBusy:      "Для этой подписки уже есть запрос от другого аккаунта. Попробуйте позже.",
	transferDone:      "Готово: подписка «%s» теперь подключена к другому аккаунту.",
	transferDenied:    "Отказ записан, подписка «%s» осталась у вас.",
	transferDeniedNew: "Владелец подписки не разрешил подключение.",
	transferStale:     "Запрос уже не действует.",
	transferNotice:    "Подписка «%s» теперь подключена к этому аккаунту.",
	noSub:             "Подписка не найдена.",
	sharedPlace:       "Приложения без ID устройства", device: "Устройство",
	justNow: "только что", minAgo: "%d мин назад", hoursAgo: "%d ч назад", yesterday: "вчера", daysAgo: "%s назад",
	months:   [12]string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"},
	commands: "Главное меню",
	menuApp:  "Подписка",

	buy: "🛒 Купить подписку", buyTitle: "Выберите тариф", renewTitle: "Продление подписки «%s»: выберите тариф",
	payHow: "Как оплатить?", payStars: "⭐ Telegram Stars — %s", payCard: "💳 Картой или СБП — %s", payCrypto: "🪙 Криптовалютой — %s", payAddon: "💳 %s — %s", payButton: "Оплатить %s",
	invoice:         "Счёт: «%s» — %s.\n\nОплатите по кнопке ниже: %s сразу после оплаты, бот пришлёт сообщение.",
	payNew:          "подписка будет готова",
	payRenew:        "подписка продлится",
	notForSale:      "Этот тариф больше не продаётся.",
	pickTerm:        "На какой срок?",
	priceFrom:       "от %s",
	payUnavailable:  "Оплата сейчас недоступна. Попробуйте позже или напишите в поддержку.",
	tooManyInvoices: "Слишком много счетов подряд. Попробуйте через час.",
	trial:           "🎁 Попробовать бесплатно",
	trialDone:       "🎁 Пробная подписка готова: %s.\n\nСсылка и инструкции в меню.",
	trialUsed:       "Пробный период даётся один раз, и только тем, у кого ещё не было подписки.",
	trialOff:        "Пробный период сейчас недоступен.",
	trialOpen:       "📱 Открыть подписку",
	trialFail:       "Не получилось выдать пробный период. Попробуйте позже или напишите в поддержку.",
	trialPaid:       "🎁 Попробовать за %s",
	trialTitle:      "🎁 Пробный период",
	trialNotGiven:   "🎁 Пробный период у вас уже был, поэтому платёж %s не засчитан и подписка не выдана. Деньги вернём: напишите в поддержку.",
	paidNew:         "✅ Оплата получена — подписка «%s» готова (тариф «%s»).\n\nДобавьте ссылку в приложение:\n<code>%s</code>",
	paidRenew:       "✅ Оплата получена — подписка «%s» продлена до %s.",
	poolOut:         "закончился до сброса",

	channelJoin:   "📢 Подписаться на канал",
	channelCheck:  "✅ Проверить",
	channelNotYet: "Пока не видим вас в канале. Подпишитесь и нажмите «Проверить» ещё раз.",
	freeDone:      "🎁 Бесплатная подписка готова: %s.\n\nСсылка и инструкции в меню.",
	freeActive:    "Бесплатная подписка у вас уже есть. Новую можно взять, когда она закончится.",
	freeOff:       "Бесплатный тариф сейчас недоступен.",
	freeFail:      "Не получилось выдать бесплатный тариф. Попробуйте позже или напишите в поддержку.",
	freeChannel:   "Бесплатный тариф для подписчиков нашего канала. Подпишитесь и нажмите «Проверить».",

	buyTraffic:   "📦 Докупить трафик",
	trafficTitle: "Трафик для подписки «%s»: выберите пакет",
	payPackage:   "трафик начислится",
	packageGone:  "Этот пакет больше не продаётся.",
	paidPackage:  "✅ Оплата получена — пакет «%s» начислен на подписку «%s».",
	plusPackages: "%s + пакеты %s",

	refundedNew:     "↩️ Платёж за тариф «%s» возвращён. Подписка «%s» отключена.",
	refundedRenew:   "↩️ Платёж за продление «%s» возвращён. Срок подписки «%s» уменьшен. Окончание: %s.",
	refundedPackage: "↩️ Платёж за пакет «%s» возвращён. Трафик пакета снят с подписки «%s».",
	refundedGone:    "↩️ Платёж за «%s» возвращён.",
}

var en = words{
	welcome:    "This is the {brand} bot.\n\nTo manage your subscription, open its page and tap “Open in Telegram”, or send the subscription link here.",
	main:       "{brand} · {name}\n\n{state}\n📅 {term}\n📦 {traffic}\n📱 Devices: {devices}",
	renew:      "To renew your subscription, message support: they will tell you how to pay.",
	expiring:   "⏳ Subscription “{name}” ends {until}: {days} left.",
	expired:    "⛔️ Subscription “{name}” has ended. Message support to renew it.",
	traffic90:  "📦 90% of the traffic of “{name}” is used: {left} left.",
	trafficEnd: "📦 The traffic of “{name}” for this period is used up. It renews {reset}.",
	channel:    "To use the {brand} bot, join our channel, then tap “Check”.",
	freeButton: "🎁 Free plan",

	back: "⬅️ Back", promo: "🎟 Promo codes", yesUnbind: "✅ Yes, unbind", cancel: "↩️ Cancel", subscriptions: "🔁 Subscriptions", openPage: "🌐 Open the subscription page", support: "💬 Support",
	subTitle: "Subscription “%s”", devicesTitle: "Devices", connectTitle: "Connect a device", switchTitle: "Which subscription to show?",
	stateActive: "✅ Working", stateExpiring: "⏳ Ends soon", stateLimited: "📦 Traffic for this period is used up", stateExpired: "⛔️ The subscription has ended", stateOff: "⏸ Access is paused",
	forever: "no end date", termUntil: "until %s — %s left", noLimit: "unlimited", trafficOf: "%s of %s", trafficNoLimit: "%s, unlimited", resets: "🔄 Renews: %s",
	devicesNone:   "Devices appear here once an app loads the subscription.",
	devicesOff:    "Device binding is off: at most %s addresses can be connected at once.",
	devicesNote:   "An unbound device is disconnected at once and its place frees up.",
	confirmUnbind: "Unbind “%s”? It disconnects at once.",
	unbindLeft:    "You can unbind %d more of %d per %s.",
	returnNote:    "An unbound device cannot connect again for %s, so close the app on it first.",
	confirmReturn: "It can connect again only in %s: close the app on it first, or it tries to come back at once.",
	unbindFailed:  "⚠️ The device was not unbound. Please try again a little later.",
	unbindOff:     "Only the administrator can unbind a device: write to support.",
	returnNever:   "An unbound device connects again only if the administrator lets it.",
	confirmNever:  "It can connect again only if the administrator lets it: close the app on it first.",
	perDay:        "day", perWeek: "week", perMonth: "month", perDays: "%s", hours: "%d h",
	unbound:           "✅ “%s” is unbound.",
	removeSub:         "🗑 Remove from the bot",
	confirmRemove:     "Remove the subscription “%s” from this Telegram account?\n\nIt keeps working in the apps, but its notices stop coming here. To bring it back, send its subscription link here.",
	yesRemove:         "✅ Yes, remove",
	removed:           "✅ The subscription “%s” is removed from the bot.",
	wait:              "⏳ You can unbind the next device %s.",
	connectText:       "1. Install an app: Happ (iPhone, Android), ClashFest (Android), Koala Clash (Windows), SlothClash (Windows, Mac, Linux) or another from the subscription page.\n2. Add this link to it:\n<code>%s</code>\n\nThe subscription page has “Add” buttons for every app.",
	linked:            "✅ Subscription “%s” is connected.",
	alreadyLinked:     "Subscription “%s” is already here.",
	linkExpired:       "The link has expired. Reload the subscription page and tap “Open in Telegram” again.",
	linkInvalid:       "The link did not work. Open the subscription page and tap “Open in Telegram”.",
	linkLimit:         "One account can hold at most %d subscriptions.",
	transferAsk:       "Someone wants to connect subscription “%s” to another Telegram account (%s). If that is you, allow it. If not, refuse: the subscription stays with you.",
	transferAllow:     "✅ Allow",
	transferDeny:      "❌ Refuse",
	transferAsked:     "This subscription is already connected to another account. We asked its owner: once they allow it, the subscription appears here. The request is good for an hour.",
	transferBusy:      "There is already a request for this subscription from another account. Try again later.",
	transferDone:      "Done: subscription “%s” is now connected to the other account.",
	transferDenied:    "Refused: subscription “%s” stays with you.",
	transferDeniedNew: "The owner of the subscription did not allow it.",
	transferStale:     "This request is no longer valid.",
	transferNotice:    "Subscription “%s” is now connected to this account.",
	noSub:             "Subscription not found.",
	sharedPlace:       "Apps without a device ID", device: "Device",
	justNow: "just now", minAgo: "%d min ago", hoursAgo: "%d h ago", yesterday: "yesterday", daysAgo: "%s ago",
	months:   [12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	commands: "Main menu",
	menuApp:  "Subscription",

	buy: "🛒 Buy a subscription", buyTitle: "Pick a plan", renewTitle: "Renew subscription “%s”: pick a plan",
	payHow: "How would you like to pay?", payStars: "⭐ Telegram Stars — %s", payCard: "💳 Card or SBP — %s", payCrypto: "🪙 Crypto — %s", payAddon: "💳 %s — %s", payButton: "Pay %s",
	invoice:         "Invoice: “%s” — %s.\n\nPay with the button below: the %s as soon as it is paid, and the bot will message you.",
	payNew:          "subscription is ready",
	payRenew:        "subscription is renewed",
	notForSale:      "This plan is no longer sold.",
	pickTerm:        "For how long?",
	priceFrom:       "from %s",
	payUnavailable:  "Payment is not available right now. Try later or message support.",
	tooManyInvoices: "Too many invoices in a row. Try again in an hour.",
	trial:           "🎁 Try it for free",
	trialDone:       "🎁 Your trial subscription is ready: %s.\n\nThe link and instructions are in the menu.",
	trialUsed:       "The trial is given once, and only to people who have not had a subscription.",
	trialOff:        "The trial is not available now.",
	trialOpen:       "📱 Open the subscription",
	trialFail:       "Could not give the trial. Try later or message support.",
	trialPaid:       "🎁 Try it for %s",
	trialTitle:      "🎁 Trial",
	trialNotGiven:   "🎁 You have had the trial already, so the payment of %s is not counted and no subscription is given. The money comes back: message support.",
	paidNew:         "✅ Payment received — subscription “%s” is ready (plan “%s”).\n\nAdd the link to your app:\n<code>%s</code>",
	paidRenew:       "✅ Payment received — subscription “%s” is renewed until %s.",
	poolOut:         "used up until the reset",

	channelJoin:   "📢 Join the channel",
	channelCheck:  "✅ Check",
	channelNotYet: "We do not see you in the channel yet. Join it and tap “Check” again.",
	freeDone:      "🎁 Your free subscription is ready: %s.\n\nThe link and instructions are in the menu.",
	freeActive:    "You already have a free subscription. You can take a new one when it ends.",
	freeOff:       "The free plan is not available now.",
	freeFail:      "Could not give the free plan. Try later or message support.",
	freeChannel:   "The free plan is for our channel's subscribers. Join it and tap “Check”.",

	buyTraffic:   "📦 Buy more traffic",
	trafficTitle: "Traffic for subscription “%s”: pick a package",
	payPackage:   "traffic is added",
	packageGone:  "This package is no longer sold.",
	paidPackage:  "✅ Payment received — package “%s” is added to subscription “%s”.",
	plusPackages: "%s + packages %s",

	refundedNew:     "↩️ The payment for plan “%s” is refunded. Subscription “%s” is turned off.",
	refundedRenew:   "↩️ The payment for the renewal “%s” is refunded. The term of subscription “%s” is shortened. Ends: %s.",
	refundedPackage: "↩️ The payment for package “%s” is refunded. Its traffic is taken off subscription “%s”.",
	refundedGone:    "↩️ The payment for “%s” is refunded.",
}

func wordsFor(lang string) *words {
	if lang == "en" {
		return &en
	}
	// plus: the admin's own texts over the built-in ones (plus_texts.go).
	if h := wordsHook.Load(); h != nil {
		return (*h)(&ru)
	}
	return &ru
}

// render turns an admin's (or built-in) text into the bot's HTML: its Markdown becomes
// formatting (markdown.go), the rest is escaped, and the {variables} are filled in. A
// value is text whatever it holds: a subscriber's name with stars or tags stays as typed.
func render(text string, vars map[string]string) string {
	return markdownHTML(text, func(s string) string { return fill(s, vars) })
}

// Render is render for the panel's preview: the HTML the bot would send for text.
func Render(text string, vars map[string]string) string { return render(text, vars) }

// fill escapes a run of text for Telegram's HTML and fills its {variables}, escaped too.
// One pass over the text: a value that itself holds {something} is not filled again, and
// the result does not depend on the order the variables are listed in.
func fill(text string, vars map[string]string) string {
	rest := html.EscapeString(text)
	var out strings.Builder
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			break
		}
		if v, ok := vars[rest[i+1:i+j]]; ok {
			out.WriteString(rest[:i])
			out.WriteString(html.EscapeString(v))
			rest = rest[i+j+1:]
			continue
		}
		out.WriteString(rest[:i+1])
		rest = rest[i+1:]
	}
	out.WriteString(rest)
	return out.String()
}

func pick(admin, builtin string) string {
	if strings.TrimSpace(admin) != "" {
		return admin
	}
	return builtin
}

func (w *words) date(t time.Time) string {
	if w == &en {
		return fmt.Sprintf("%s %d, %d", w.months[t.Month()-1], t.Day(), t.Year())
	}
	return fmt.Sprintf("%d %s %d", t.Day(), w.months[t.Month()-1], t.Year())
}

func (w *words) shortDate(t time.Time) string {
	if w == &en {
		return fmt.Sprintf("%s %d", w.months[t.Month()-1][:3], t.Day())
	}
	return fmt.Sprintf("%d %s", t.Day(), w.months[t.Month()-1])
}

func (w *words) days(n int) string {
	if w == &en {
		if n == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", n)
	}
	switch {
	case n%10 == 1 && n%100 != 11:
		return fmt.Sprintf("%d день", n)
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		return fmt.Sprintf("%d дня", n)
	}
	return fmt.Sprintf("%d дней", n)
}

func (w *words) bytes(n int64) string {
	units := []string{"Б", "КБ", "МБ", "ГБ", "ТБ"}
	sep := ","
	if w == &en {
		units, sep = []string{"B", "KB", "MB", "GB", "TB"}, "."
	}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	s := fmt.Sprintf("%.0f", v)
	if i > 0 && v < 100 {
		s = strings.Replace(strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0"), ".", sep, 1)
	}
	return s + " " + units[i]
}

// period is the unbind window in words: "сутки", "неделю", "10 дней".
func (w *words) period(days int) string {
	switch days {
	case 1:
		return w.perDay
	case 7:
		return w.perWeek
	case 30:
		return w.perMonth
	}
	return fmt.Sprintf(w.perDays, w.days(days))
}

// span is a pause in hours, whole days as days.
func (w *words) span(hours int) string {
	if hours >= 24 && hours%24 == 0 {
		return w.days(hours / 24)
	}
	return fmt.Sprintf(w.hours, hours)
}

func (w *words) ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return w.justNow
	case d < time.Hour:
		return fmt.Sprintf(w.minAgo, int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf(w.hoursAgo, int(d/time.Hour))
	case d < 48*time.Hour:
		return w.yesterday
	}
	return fmt.Sprintf(w.daysAgo, w.days(int(d/(24*time.Hour))))
}

// DefaultTexts are the built-in texts the admin's empty fields fall back to.
func DefaultTexts(lang string) Texts {
	w := wordsFor(lang)
	return Texts{Welcome: w.welcome, Main: w.main, Renew: w.renew, Expiring: w.expiring, Expired: w.expired, Traffic90: w.traffic90, TrafficEnd: w.trafficEnd,
		Channel: w.channel, FreeButton: w.freeButton}
}
