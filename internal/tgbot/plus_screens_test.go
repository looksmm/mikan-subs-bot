package tgbot

import (
	"context"
	"database/sql"
	"html"
	"strings"
	"testing"
	"time"

	"subsbot/internal/billing"
	"subsbot/internal/db"
	"subsbot/internal/domain"
)

// panelStub is the panel as the templates ask it: the Russian bot, ExampleVPN, a link.
type panelStub struct {
	Backend
	devices int64
}

func (panelStub) Config(context.Context) Config          { return Default("ru") }
func (panelStub) Brand(context.Context) string           { return "ExampleVPN" }
func (panelStub) SubURL(context.Context, db.User) string { return "https://sub.example.com/s/abc" }
func (p panelStub) DeviceCount(context.Context, int64) (int64, error) {
	return p.devices, nil
}

func screensBot(t *testing.T, texts map[string]string) *Bot {
	t.Helper()
	b := withTexts(t, texts)
	b.d.Backend = panelStub{devices: 2}
	return b
}

func standard() billing.Offer {
	return billing.Offer{
		Tariff: db.Tariff{ID: 3, Name: "Стандарт", DurationDays: 30, TrafficLimit: sql.NullInt64{Int64: 1000 << 30, Valid: true},
			DeviceLimit: sql.NullInt64{Int64: 3, Valid: true}, Description: "⚪️ Белые списки: 15ГБ\n🟢 Обычные сервера: 1000ГБ"},
		Terms: []billing.OfferTerm{{Days: 30, Rub: 19900}, {Days: 365, Rub: 199000}},
		Rub:   19900,
	}
}

func TestFillTemplate(t *testing.T) {
	got := fillTemplate("**{title}**\n\n{list}\n🔄 {reset}\n\n\n\nЗвёзды: {x}\n", map[string]string{"title": "<Тарифы>", "reset": "", "x": "a*b*"},
		map[string]string{"list": "<b>один</b>\nдва"})
	want := "<b>&lt;Тарифы&gt;</b>\n\n<b>один</b>\nдва\n\nЗвёзды: a*b*"
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
	// A list that came out empty takes its line with it.
	if got := fillTemplate("A\n{list}\nB", nil, map[string]string{"list": ""}); got != "A\nB" {
		t.Fatalf("empty list: %q", got)
	}
}

// The template's start is mikan's screen: taking it changes nothing, then the admin edits.
func TestShopBaseIsMikansList(t *testing.T) {
	b := screensBot(t, map[string]string{tplShopItem: templateBase(tplShopItem)})
	w := wordsFor("ru")
	o := standard()
	mikan := "<b>" + html.EscapeString(w.buyTitle) + "</b>\n\n• <b>Стандарт</b> — " + html.EscapeString(billing.DescribeOffer(o, "ru")) + noteOf(o.Tariff.Description)
	got, ok := b.shopListText(context.Background(), w, w.buyTitle, "", []billing.Offer{o})
	if !ok || got != mikan {
		t.Fatalf("got\n%s\nwant\n%s", got, mikan)
	}
	// A tariff without a description has no empty line under it, as in mikan.
	o.Tariff.Description = ""
	got, _ = b.shopListText(context.Background(), w, w.buyTitle, "", []billing.Offer{o})
	if strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 2 {
		t.Fatalf("no description: %q", got)
	}
}

func TestShopTemplates(t *testing.T) {
	b := screensBot(t, map[string]string{
		tplShopList: "🛒 **{title}**\n\n{tariffs}\n\nВопросы — в поддержку.",
		tplShopItem: "**{tariff}** · {price}\n{traffic}, устройств: {devices}\n_{description}_\nСкорость: {speed}",
	})
	w := wordsFor("ru")
	got, ok := b.shopListText(context.Background(), w, "Продление подписки «123»: выберите тариф", "✅ Готово", []billing.Offer{standard()})
	if !ok {
		t.Fatal("no template")
	}
	for _, s := range []string{"✅ Готово\n\n🛒 <b>Продление подписки «123»: выберите тариф</b>", "<b>Стандарт</b> · от 199 ₽", "1000 ГБ, устройств: 3",
		"<i>⚪️ Белые списки: 15ГБ\n🟢 Обычные сервера: 1000ГБ</i>", "Вопросы — в поддержку."} {
		if !strings.Contains(got, s) {
			t.Errorf("no %q in\n%s", s, got)
		}
	}
	if strings.Contains(got, "Скорость") {
		t.Errorf("the line of an empty speed stayed:\n%s", got)
	}
	if _, ok := screensBot(t, nil).shopListText(context.Background(), w, "x", "", nil); ok {
		t.Fatal("no templates: mikan's screen")
	}

	b = screensBot(t, map[string]string{tplTariff: "**{tariff}** на {term}\nЦена: {price}\n{question}"})
	o := standard()
	got, _ = b.tariffText(context.Background(), w, o, nil, w.pickTerm)
	if got != "<b>Стандарт</b> на 30 дн. – 365 дн.\nЦена: от 199 ₽\n"+w.pickTerm {
		t.Fatalf("terms: %q", got)
	}
	got, _ = b.tariffText(context.Background(), w, o, &o.Terms[1], w.payHow)
	if got != "<b>Стандарт</b> на 365 дн.\nЦена: 1990 ₽\n"+w.payHow {
		t.Fatalf("one term: %q", got)
	}
}

func TestConnectTemplate(t *testing.T) {
	b := screensBot(t, map[string]string{tplConnect: templateBase(tplConnect)})
	w := wordsFor("ru")
	got, ok := b.connectText(context.Background(), db.User{ID: 1, Name: "123"})
	mikan := "<b>" + w.connectTitle + "</b>\n\n" + strings.ReplaceAll(w.connectText, "%s", "https://sub.example.com/s/abc")
	if !ok || got != mikan {
		t.Fatalf("got\n%q\nwant\n%q", got, mikan)
	}
}

func TestDevicesAndTrafficTemplates(t *testing.T) {
	b := screensBot(t, map[string]string{tplDeviceItem: "📱 {name} ({app}), {seen}", tplTrafficItem: "{package}: {size} за {price}"})
	w := wordsFor("ru")
	now := time.Unix(1_800_000_000, 0)
	u := db.User{ID: 1, Name: "123", DeviceLimit: sql.NullInt64{Int64: 3, Valid: true}}
	devs := []db.BoundDevice{{ID: 5, Hwid: "h", Model: "iPhone 15", Os: "iOS", OsVersion: "18", App: "Happ/4.12.0 ios", LastSeen: now.Add(-2 * time.Hour).Unix()}}
	got, ok := b.devicesText(context.Background(), w, u, devs, map[int64]int64{}, domain.UnbindState{}, false, "", now)
	if !ok || !strings.Contains(got, "<b>Устройства</b> · 2 из 3") || !strings.Contains(got, "📱 iPhone 15 (Happ 4.12.0), 2 ч назад") || strings.HasPrefix(got, "\n") {
		t.Fatalf("devices:\n%s", got)
	}
	got, ok = b.trafficText(context.Background(), w, u, []billing.PackageOffer{{Package: db.TrafficPackage{Name: "Малый", Bytes: 10 << 30}, Rub: 9900}}, "")
	if !ok || !strings.Contains(got, "Малый: 10 ГБ за 99 ₽") || !strings.Contains(got, "Трафик для подписки «123»") {
		t.Fatalf("traffic:\n%s", got)
	}
}
