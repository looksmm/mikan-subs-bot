// The page's own menu, automatic messages and broadcasts (bot.js keeps the settings and the
// save bar). The menu and the automatic messages are drafts saved with «Сохранить»; a
// broadcast starts with its own button.
"use strict";

window.X = (() => {
  const { $, el, rpc, failText, showError, toast, ago } = K;
  const clone = (v) => JSON.parse(JSON.stringify(v));
  const styleNames = { "": "Обычный", primary: "Синий", success: "Зелёный", danger: "Красный" };
  const newID = () => Math.random().toString(36).slice(2, 10).replace(/[^a-z0-9]/g, "x");

  let view = null;
  let menuSaved = null, menu = null;
  let autoSaved = null, auto = null;
  let bc = null; // the broadcasts' last status
  let onChange = () => {};
  let msg = { text: "", photo: "", rows: [] }; // the broadcast being written
  let audience = "all";
  let openScreens = new Set(), openRules = new Set();

  const doing = (k) => (view.doings || []).find((d) => d.key === k);

  // ask turns btn into a question with «Да» and «Отмена» in its place: the admin panel's frame
  // is sandboxed without dialogs, so confirm() would answer no by itself.
  function ask(btn, question, yes, yesLabel = "Да") {
    const box = el("span", { class: "ask" }, el("span", { class: "ask-q", text: question }),
      el("button", { type: "button", class: "small-btn danger", text: yesLabel, onclick: () => { box.replaceWith(btn); yes(); } }),
      el("button", { type: "button", class: "small-btn", text: "Отмена", onclick: () => box.replaceWith(btn) }));
    btn.replaceWith(box);
  }

  // ---- the buttons' editor, shared by the menu's sections and the messages ----

  // rowsEditor edits rows of buttons in place; screens are the menu's sections to point at.
  function rowsEditor(rows, changed, opts = {}) {
    const box = el("div", { class: "rows" });
    const draw = () => {
      box.replaceChildren();
      if (!rows.length) box.append(el("p", { class: "empty small", text: "Кнопок нет." }));
      rows.forEach((row, ri) => {
        const line = el("div", { class: "btn-row" });
        row.forEach((b, bi) => line.append(buttonEditor(b, () => { changed(); }, () => {
          row.splice(bi, 1);
          if (!row.length) rows.splice(ri, 1);
          draw(); changed();
        })));
        const tools = el("div", { class: "row-tools" },
          row.length < 3 ? el("button", { type: "button", class: "small-btn", text: "+ в ряд", onclick: () => { row.push(blank()); draw(); changed(); } }) : null,
          ri > 0 ? el("button", { type: "button", class: "small-btn", title: "Ряд выше", text: "↑", onclick: () => { rows.splice(ri - 1, 0, rows.splice(ri, 1)[0]); draw(); changed(); } }) : null,
          ri < rows.length - 1 ? el("button", { type: "button", class: "small-btn", title: "Ряд ниже", text: "↓", onclick: () => { rows.splice(ri + 1, 0, rows.splice(ri, 1)[0]); draw(); changed(); } }) : null);
        box.append(el("div", { class: "btn-line" }, line, tools));
      });
      if (rows.length < 10) box.append(el("button", { type: "button", class: "small-btn add", text: "+ Ряд кнопок", onclick: () => { rows.push([blank()]); draw(); changed(); } }));
    };
    const blank = () => ({ label: "", do: opts.firstDo || "menu" });
    draw();
    return box;
  }

  function buttonEditor(b, changed, remove) {
    const label = el("input", { type: "text", maxlength: "40", placeholder: "Надпись", value: b.label || "", "aria-label": "Надпись кнопки" });
    const what = el("select", { "aria-label": "Что делает кнопка" });
    for (const d of view.doings) what.append(el("option", { value: d.key, text: d.name }));
    what.value = b.do || "menu";
    const to = el("select", { "aria-label": "Какой раздел открыть" });
    const fillTo = () => {
      to.replaceChildren(el("option", { value: "", text: "— раздел —" }));
      for (const s of (menu && menu.screens) || []) to.append(el("option", { value: s.id, text: s.name || s.id }));
      to.value = b.to || "";
    };
    fillTo();
    const url = el("input", { type: "text", spellcheck: "false", placeholder: "https://…", value: b.url || "", "aria-label": "Адрес ссылки" });
    const style = el("select", { "aria-label": "Цвет кнопки" });
    for (const s of view.styles) style.append(el("option", { value: s, text: styleNames[s] || s }));
    style.value = b.style || "";
    const icon = el("input", { type: "text", inputmode: "numeric", spellcheck: "false", placeholder: "ID эмодзи", value: b.icon || "", "aria-label": "Иконка: ID своего эмодзи" });
    const hint = el("small", { class: "muted" });
    const sync = () => {
      b.label = label.value; b.do = what.value;
      const set = (k, v) => { if (v) b[k] = v; else delete b[k]; };
      set("style", style.value);
      set("icon", icon.value.trim());
      set("to", b.do === "screen" ? to.value : "");
      set("url", b.do === "url" ? url.value.trim() : "");
      to.hidden = b.do !== "screen";
      url.hidden = b.do !== "url";
      const d = doing(b.do);
      hint.textContent = d ? d.hint + (d.sub ? " · только с подпиской" : "") : "";
      label.classList.toggle("invalid", !b.label.trim());
      to.classList.toggle("invalid", b.do === "screen" && !b.to);
      url.classList.toggle("invalid", b.do === "url" && !/^(https|tg):\/\/\S+$/.test(b.url || ""));
      icon.classList.toggle("invalid", !!b.icon && !/^\d{5,32}$/.test(b.icon));
      changed();
    };
    for (const n of [label, url, icon]) n.addEventListener("input", sync);
    for (const n of [what, to, style]) n.addEventListener("change", sync);
    to.addEventListener("focus", () => { const v = to.value; fillTo(); to.value = v; });
    const card = el("div", { class: "btn-edit" },
      el("div", { class: "btn-main" }, label, el("button", { type: "button", class: "small-btn x", title: "Убрать кнопку", text: "×", onclick: remove })),
      what, to, url,
      el("div", { class: "btn-look" }, style, icon),
      hint);
    // Set the visibility without counting it as an edit.
    const keep = onChange; onChange = () => {}; sync(); onChange = keep;
    return card;
  }

  // ---- the preview: the message as Telegram shows it, roughly ----

  function mdPreview(text) {
    const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    return esc(text)
      .replace(/\*\*(.+?)\*\*/g, "<b>$1</b>")
      .replace(/__(.+?)__/g, "<i>$1</i>")
      .replace(/`(.+?)`/g, "<code>$1</code>")
      .replace(/\{([a-z_]+)\}/g, '<span class="var">{$1}</span>');
  }

  function preview(text, rows, photo, extra) {
    const bubble = el("div", { class: "tg-bubble" });
    if (photo) bubble.append(el("div", { class: "tg-photo", text: "🖼 картинка" }));
    const body = el("div", { class: "tg-text" });
    body.innerHTML = mdPreview(text || "") || '<span class="muted">Текст не задан</span>';
    bubble.append(body);
    const kb = el("div", { class: "tg-kb" });
    for (const r of [...rows, ...(extra || [])]) {
      const line = el("div", { class: "tg-kb-row" });
      for (const b of r) {
        const d = doing(b.do);
        line.append(el("span", { class: "tg-btn " + (b.style || ""), title: d ? d.name : "" },
          b.icon ? el("span", { class: "tg-icon", text: "◆" }) : null, b.label || "…"));
      }
      if (line.childElementCount) kb.append(line);
    }
    return el("div", { class: "tg" }, bubble, kb.childElementCount ? kb : null);
  }

  function varsHint() {
    return el("small", { class: "muted" }, "Разметка: **жирный**, __курсив__, `код`. Переменные: ",
      ...(view.vars || []).flatMap((v, i) => [i ? ", " : "", el("span", { class: "code", text: "{" + v.key + "}" }), " " + v.hint]));
  }

  // messageEditor edits m (text, picture, buttons) with a preview and «Отправить себе».
  function messageEditor(m, changed) {
    const text = el("textarea", { rows: "5", spellcheck: "false", placeholder: "Текст сообщения" });
    text.value = m.text || "";
    const photo = el("input", { type: "text", spellcheck: "false", placeholder: "https://… адрес картинки (необязательно)", value: m.photo || "" });
    const pv = el("div", { class: "pv" });
    const count = el("small", { class: "muted" });
    const redraw = () => {
      pv.replaceChildren(preview(m.text, m.rows || [], m.photo));
      const n = (m.text || "").length, max = m.photo ? 1000 : 4000;
      count.textContent = n + " / " + max;
      count.classList.toggle("bad-text", n > max);
      photo.classList.toggle("invalid", !!m.photo && !/^https:\/\/\S+$/.test(m.photo));
    };
    const edit = () => { redraw(); changed(); };
    text.addEventListener("input", () => { m.text = text.value; edit(); });
    photo.addEventListener("input", () => { m.photo = photo.value.trim(); edit(); });
    m.rows = m.rows || [];
    const test = el("button", { type: "button", class: "small-btn", text: "Отправить себе" });
    const testState = el("small", { class: "muted" });
    test.addEventListener("click", async () => {
      const chat = Number(K.testChat());
      if (!chat) { testState.textContent = "Укажите свой Telegram ID во вкладке «Рассылка»."; return; }
      test.disabled = true; testState.textContent = "Отправляю…";
      const r = await rpc("POST", "/test", { chat, message: m }, 40000);
      test.disabled = false;
      testState.textContent = r.status === 200 ? "Отправлено, проверьте Telegram." : failText(r);
    });
    redraw();
    return el("div", { class: "msg-edit" },
      el("div", { class: "msg-fields" },
        el("div", { class: "field" }, el("label", { text: "Текст" }), text, el("div", { class: "row between wrap" }, varsHint(), count)),
        el("div", { class: "field" }, el("label", { text: "Картинка" }), photo, el("small", { class: "muted", text: "Telegram загрузит её по адресу. С картинкой текст — до 1000 символов." })),
        el("div", { class: "field" }, el("label", { text: "Кнопки" }), rowsEditor(m.rows, edit)),
        el("div", { class: "row wrap" }, test, testState)),
      el("div", { class: "msg-pv" }, el("div", { class: "muted small", text: "Как увидит подписчик" }), pv));
  }

  // ---- the menu ----

  function screenName(id) {
    const s = menu.screens.find((x) => x.id === id);
    return s ? s.name || s.id : id;
  }

  function renderMenu() {
    $("menu-on").checked = !!menu.on;
    const fill = (sel, val, none) => {
      sel.replaceChildren();
      if (none) sel.append(el("option", { value: "", text: none }));
      for (const s of menu.screens) sel.append(el("option", { value: s.id, text: s.name || s.id }));
      sel.value = val || "";
    };
    fill($("menu-main"), menu.main, "— выберите раздел —");
    fill($("menu-welcome"), menu.welcome, "Стандартный экран mikan");
    $("menu-starter").hidden = menu.screens.length > 0;
    const box = $("screens");
    box.replaceChildren();
    for (const s of menu.screens) box.append(screenCard(s));
    if (!menu.screens.length) box.append(el("p", { class: "empty", text: "Разделов пока нет. Начните с готового меню или добавьте раздел." }));
    for (const id of ["menu-on", "menu-main", "menu-welcome", "screen-add", "menu-starter"]) $(id).disabled = false;
  }

  function screenCard(s) {
    const roles = [];
    if (s.id === menu.main) roles.push("Главное меню");
    if (s.id === menu.welcome) roles.push("Первый экран");
    const det = el("details", { class: "card" });
    det.open = openScreens.has(s.id);
    det.addEventListener("toggle", () => { if (det.open) openScreens.add(s.id); else openScreens.delete(s.id); });
    const title = el("h3", { text: s.name || "Без названия" });
    det.append(el("summary", {}, title, ...roles.map((r) => el("span", { class: "pill good", text: r })), el("span", { class: "tag", text: s.id })));
    const name = el("input", { type: "text", maxlength: "60", placeholder: "Название раздела (видно только вам)", value: s.name || "" });
    const text = el("textarea", { rows: "4", spellcheck: "false" });
    text.value = s.text || "";
    text.placeholder = s.id === menu.main ? "Пусто — шапка главного меню из панели" : s.id === menu.welcome ? "Пусто — приветствие из панели" : "Текст раздела";
    const back = el("input", { type: "checkbox" });
    back.checked = !!s.back;
    const pv = el("div", { class: "pv" });
    const redraw = () => {
      const extra = s.back && s.id !== menu.main && s.id !== menu.welcome ? [[{ label: "⬅️ Назад", do: "menu" }]] : [];
      pv.replaceChildren(preview(s.text || (s.id === menu.main ? "(шапка главного меню из панели)" : s.id === menu.welcome ? "(приветствие из панели)" : ""), s.rows, "", extra));
    };
    const edit = () => { redraw(); changed(); };
    name.addEventListener("input", () => { s.name = name.value; title.textContent = s.name || "Без названия"; changed(); });
    text.addEventListener("input", () => { s.text = text.value; edit(); });
    back.addEventListener("change", () => { s.back = back.checked; edit(); });
    s.rows = s.rows || [];
    redraw();
    const del = el("button", { type: "button", class: "small-btn danger", text: "Удалить раздел" });
    del.addEventListener("click", () => {
      const used = menu.screens.some((o) => o.rows.some((r) => r.some((b) => b.do === "screen" && b.to === s.id)));
      ask(del, "Удалить раздел?" + (used ? " На него ведут кнопки — они пропадут." : ""), () => {
        menu.screens = menu.screens.filter((x) => x !== s);
        if (menu.main === s.id) menu.main = "";
        if (menu.welcome === s.id) menu.welcome = "";
        renderMenu(); changed();
      }, "Удалить");
    });
    det.append(el("div", { class: "msg-edit" },
      el("div", { class: "msg-fields" },
        el("div", { class: "field" }, el("label", { text: "Название" }), name),
        el("div", { class: "field" }, el("label", { text: "Текст" }), text, varsHint()),
        el("label", { class: "switch" }, back, el("span", { text: "Кнопка «Назад» внизу (кроме главного меню и первого экрана)" })),
        el("div", { class: "field" }, el("label", { text: "Кнопки" }), rowsEditor(s.rows, edit, { firstDo: "screen" })),
        el("div", { class: "row wrap" }, del)),
      el("div", { class: "msg-pv" }, el("div", { class: "muted small", text: "Как увидит подписчик" }), pv)));
    return det;
  }

  // starter is a menu to begin with: mikan's main menu, a first screen and a help section.
  function starter() {
    const B = (label, d, extra) => Object.assign({ label, do: d }, extra || {});
    return {
      on: false, main: "main", welcome: "hello", saved: 0,
      screens: [
        { id: "main", name: "Главное меню", text: "", back: false, rows: [
          [B("📊 Подписка", "sub"), B("📱 Устройства", "devices")],
          [B("🔌 Подключить устройство", "connect")],
          [B("💳 Продлить", "renew"), B("💬 Поддержка", "support")],
          [B("📚 Инструкции", "screen", { to: "help" })],
          [B("🌐 Страница подписки", "app")]] },
        { id: "hello", name: "Первый экран", text: "", back: false, rows: [
          [B("🎁 Пробный период", "trial")],
          [B("💳 Купить подписку", "buy")],
          [B("📚 Инструкции", "screen", { to: "help" }), B("💬 Поддержка", "support")]] },
        { id: "help", name: "Инструкции", text: "**Как подключиться**\n\nВыберите своё устройство:", back: true, rows: [
          [B("📱 iPhone", "url", { url: "https://example.com/ios" }), B("🤖 Android", "url", { url: "https://example.com/android" })],
          [B("💻 Компьютер", "url", { url: "https://example.com/pc" })]] },
      ],
    };
  }

  // ---- the automatic messages ----

  const units = [["1", "минут"], ["60", "часов"], ["1440", "дней"]];
  function delayParts(min) {
    if (!min) return [0, "60"];
    for (const [k] of [...units].reverse()) if (min % Number(k) === 0) return [min / Number(k), k];
    return [min, "1"];
  }
  function delayText(min) {
    if (!min) return "сразу";
    const [n, u] = delayParts(min);
    return "через " + n + " " + units.find((x) => x[0] === u)[1].replace("минут", "мин").replace("часов", "ч").replace("дней", "дн");
  }

  function renderAuto() {
    const box = $("rules");
    box.replaceChildren();
    for (const r of auto.rules) box.append(ruleCard(r));
    if (!auto.rules.length) box.append(el("p", { class: "empty", text: "Автосообщений нет." }));
    $("rule-add").disabled = false;
  }

  function ruleCard(r) {
    const trig = (k) => (view.triggers || []).find((t) => t.key === k);
    const det = el("details", { class: "card" });
    det.open = openRules.has(r.id);
    det.addEventListener("toggle", () => { if (det.open) openRules.add(r.id); else openRules.delete(r.id); });
    const on = el("input", { type: "checkbox", "aria-label": "Включено" });
    on.checked = !!r.on;
    on.addEventListener("click", (e) => e.stopPropagation());
    on.addEventListener("change", () => { r.on = on.checked; changed(); });
    const title = el("h3", { text: r.name || "Без названия" });
    const when = el("span", { class: "muted small" });
    const sum = () => { const t = trig(r.trigger); when.textContent = (t ? t.name : "?") + " · " + delayText(r.delay); };
    sum();
    det.append(el("summary", {}, el("label", { class: "switch" }, on), title, when));
    const name = el("input", { type: "text", maxlength: "60", placeholder: "Название (видно только вам)", value: r.name || "" });
    name.addEventListener("input", () => { r.name = name.value; title.textContent = r.name || "Без названия"; changed(); });
    const tsel = el("select");
    for (const t of view.triggers) tsel.append(el("option", { value: t.key, text: t.name }));
    tsel.value = r.trigger || "start";
    const thint = el("small", { class: "muted" });
    const [dn, du] = delayParts(r.delay || 0);
    const dnum = el("input", { type: "text", inputmode: "numeric", class: "num", value: String(dn) });
    const dunit = el("select");
    for (const [k, n] of units) dunit.append(el("option", { value: k, text: n }));
    dunit.value = du;
    const days = el("select");
    for (const [k, n] of [["0", "за 3 дня и за 1 день"], ["3", "только за 3 дня"], ["1", "только за 1 день"]]) days.append(el("option", { value: k, text: n }));
    days.value = String(r.days || 0);
    const first = el("input", { type: "checkbox" });
    first.checked = !!r.first;
    const replace = el("input", { type: "checkbox" });
    replace.checked = !!r.replace;
    const daysF = el("div", { class: "field" }, el("label", { text: "Когда напоминает панель" }), days);
    const firstF = el("label", { class: "switch" }, first, el("span", { text: "Только после первой покупки подписки" }));
    const replF = el("label", { class: "switch" }, replace, el("span", { text: "Вместо уведомления панели (стандартное не придёт)" }));
    const needs = el("div", { class: "check warn small" });
    const sync = () => {
      r.trigger = tsel.value;
      const n = Math.max(0, Math.round(Number(dnum.value) || 0));
      r.delay = n * Number(dunit.value);
      dnum.classList.toggle("invalid", !/^\d+$/.test(dnum.value.trim()) || r.delay > 86400);
      r.days = r.trigger === "expiring" ? Number(days.value) : 0;
      r.first = r.trigger === "paid" && first.checked;
      const t = trig(r.trigger) || {};
      r.replace = !!t.notice && replace.checked;
      thint.textContent = t.hint || "";
      daysF.hidden = r.trigger !== "expiring";
      firstF.hidden = r.trigger !== "paid";
      replF.hidden = !t.notice;
      const missing = (view.missing || []).includes("users:all");
      needs.hidden = !(missing && (r.trigger === "no_sub" || (r.delay > 0 && (r.trigger === "expired" || r.trigger === "expiring"))));
      needs.textContent = "Нужно право аддона «Все пользователи»: без него бот не узнает, оформил ли человек подписку или продлил ли её.";
      sum();
      changed();
    };
    dnum.addEventListener("input", sync);
    for (const n of [tsel, dunit, days, first, replace]) n.addEventListener("change", sync);
    const keep = onChange; onChange = () => {}; sync(); onChange = keep;
    r.message = r.message || { text: "", rows: [] };
    const del = el("button", { type: "button", class: "small-btn danger", text: "Удалить" });
    del.addEventListener("click", () => ask(del, "Удалить автосообщение?", () => {
      auto.rules = auto.rules.filter((x) => x !== r); renderAuto(); changed();
    }, "Удалить"));
    det.append(el("div", { class: "rule-body" },
      el("div", { class: "rule-when" },
        el("div", { class: "field" }, el("label", { text: "Название" }), name),
        el("div", { class: "field" }, el("label", { text: "После чего" }), tsel, thint),
        el("div", { class: "field" }, el("label", { text: "Задержка" }), el("div", { class: "row" }, dnum, dunit), el("small", { class: "muted", text: "0 — сразу. До 60 дней." })),
        daysF, firstF, replF, needs),
      messageEditor(r.message, changed),
      el("div", { class: "row wrap" }, del)));
    return det;
  }

  // ---- the broadcasts ----

  function renderBroadcast() {
    if (!bc) return;
    const box = $("audiences");
    box.replaceChildren();
    for (const a of view.audiences) {
      const n = bc.counts[a.key];
      const r = el("input", { type: "radio", name: "aud", value: a.key });
      r.checked = audience === a.key;
      const off = a.users && !bc.users_ok;
      r.disabled = off;
      r.addEventListener("change", () => { audience = a.key; drawStart(); });
      box.append(el("label", { class: "aud" + (off ? " off" : "") }, r,
        el("span", {}, el("b", { text: a.name }), " ", el("span", { class: "muted", text: n == null ? "" : "· " + n })),
        el("small", { class: "muted", text: off ? "нужно право «Все пользователи»" : a.hint })));
    }
    const j = bc.job;
    const run = $("job");
    run.replaceChildren();
    if (j && (j.state === "running" || j.state === "scheduled")) {
      const done = j.sent + j.failed + j.blocked;
      const pct = j.total ? Math.round((done / j.total) * 100) : 0;
      run.append(el("div", { class: "card job" },
        el("div", { class: "row between wrap" },
          el("b", { text: j.state === "scheduled" ? "Запланирована на " + new Date(j.at * 1000).toLocaleString("ru-RU") : "Идёт рассылка" }),
          stopButton(j)),
        j.state === "running" ? el("div", { class: "bar" }, el("span", { style: "width:" + pct + "%" })) : null,
        el("div", { class: "muted small", text: j.state === "running" ? `Отправлено ${j.sent} из ${j.total} · заблокировали бота ${j.blocked} · не дошло ${j.failed}` : audienceName(j.audience) }),
        el("div", { class: "muted small job-text", text: (j.message.text || "").slice(0, 300) })));
    }
    const hist = $("history");
    hist.replaceChildren();
    const h = (bc.history || []).filter((x) => !j || x.id !== j.id || !(j.state === "running" || j.state === "scheduled"));
    if (!h.length) hist.append(el("p", { class: "empty small", text: "Рассылок ещё не было." }));
    for (const x of h) {
      hist.append(el("div", { class: "hist" },
        el("div", { class: "row between wrap" },
          el("b", { text: (x.state === "done" ? "✓ " : "✕ ") + audienceName(x.audience) }),
          el("span", { class: "muted small", text: ago(x.finished || x.created) })),
        el("div", { class: "muted small", text: x.state === "cancelled" && !x.started ? "отменена до начала" + (x.error ? ": " + x.error : "") :
          `отправлено ${x.sent} из ${x.total} · заблокировали ${x.blocked} · не дошло ${x.failed}` + (x.state === "cancelled" ? " · остановлена" : "") }),
        el("div", { class: "small job-text", text: x.message.text })));
    }
    drawStart();
  }

  function audienceName(k) { const a = view.audiences.find((x) => x.key === k); return a ? a.name : k; }

  function drawStart() {
    if (!$("bc-start")) return; // asked to confirm right now
    const busy = bc && bc.job && (bc.job.state === "running" || bc.job.state === "scheduled");
    const n = bc ? bc.counts[audience] : null;
    $("bc-start").disabled = busy || !n;
    $("bc-start").textContent = $("bc-when").value === "at" ? "Запланировать" : "Начать рассылку" + (n ? " (" + n + ")" : "");
    $("bc-busy").hidden = !busy;
  }

  async function loadBroadcast() {
    const r = await rpc("GET", "/broadcast", undefined, 20000);
    if (r.status !== 200) { $("bc-error").textContent = failText(r); $("bc-error").hidden = false; return; }
    $("bc-error").hidden = true;
    bc = r.body;
    renderBroadcast();
  }

  function startBroadcast() {
    const body = { audience, message: msg };
    if ($("bc-when").value === "at") {
      const t = Date.parse($("bc-at").value);
      if (!t || t < Date.now() + 60000) { $("bc-error").textContent = "Укажите время в будущем."; $("bc-error").hidden = false; return; }
      body.at = Math.round(t / 1000);
    }
    const n = bc.counts[audience];
    const what = body.at ? "Запланировать для «" + audienceName(audience) + "» (сейчас " + n + ")?" : "Отправить " + n + " получателям? Отправленное не отменить.";
    ask($("bc-start"), what, () => sendBroadcast(body), body.at ? "Да, запланировать" : "Да, отправить");
  }

  async function sendBroadcast(body) {
    $("bc-start").disabled = true;
    const r = await rpc("POST", "/broadcast", body, 60000);
    if (r.status !== 200) { $("bc-error").textContent = failText(r); $("bc-error").hidden = false; drawStart(); return; }
    $("bc-error").hidden = true;
    bc = r.body;
    renderBroadcast();
    toast();
    $("job").scrollIntoView({ behavior: "smooth", block: "start" });
  }

  function stopButton(j) {
    const btn = el("button", { type: "button", class: "small-btn danger", text: j.state === "scheduled" ? "Отменить" : "Остановить" });
    btn.addEventListener("click", () => ask(btn, j.state === "scheduled" ? "Отменить рассылку?" : "Остановить? Отправленное останется.", cancel, "Да"));
    return btn;
  }

  async function cancel() {
    const r = await rpc("POST", "/broadcast/cancel", {}, 30000);
    if (r.status !== 200) { $("bc-error").textContent = failText(r); $("bc-error").hidden = false; return; }
    bc = r.body;
    renderBroadcast();
  }

  // ---- the page's side: drafts, dirty, save ----

  const changed = () => onChange();
  // norm is a draft as the addon keeps it: an empty field is no field, and the time of saving
  // is the addon's.
  const norm = (v) => JSON.stringify(v, (k, x) => (x === "" || x === null || k === "saved" ? undefined : x));

  function dirty() {
    return !!menu && (norm(menu) !== norm(menuSaved) || norm(auto) !== norm(autoSaved));
  }

  function take(v, first) {
    view = v;
    if (first || !dirty()) {
      menuSaved = clone(v.menu || { on: false, main: "", welcome: "", screens: [] });
      menu = clone(menuSaved);
      autoSaved = clone(v.auto || { rules: [] });
      auto = clone(autoSaved);
      renderMenu();
      renderAuto();
    }
    const miss = (v.missing || []).map((p) => ({ "users:all": "«Все пользователи»", kv: "«Хранилище»", events: "«События»", ui: "«Страницы»" })[p] || p);
    $("perms").hidden = !miss.length;
    $("perms").textContent = miss.length ? "Аддону не хватает прав: " + miss.join(", ") + ". Дайте их в «Аддоны» → этот аддон → «Права»: без «Все пользователи» рассылка не делит получателей по подписке, а автосообщения не знают, купил ли человек." : "";
    const c = v.chats || {};
    $("chats-count").textContent = c.loaded ? "Бот знает " + c.total + " чатов" + (c.blocked ? ", из них " + c.blocked + " заблокировали бота" : "") + "." : "Список чатов загружается…";
    if (first) {
      $("bc-msg").replaceChildren(messageEditor(msg, () => drawStart()));
      loadBroadcast();
    }
  }

  async function save() {
    const out = [];
    if (norm(menu) !== norm(menuSaved)) {
      const r = await rpc("PUT", "/menu", menu, 20000, 2);
      if (r.status !== 200) return { ok: false, r, where: "menu" };
      out.push(r.body);
    }
    if (norm(auto) !== norm(autoSaved)) {
      const r = await rpc("PUT", "/auto", auto, 20000, 2);
      if (r.status !== 200) return { ok: false, r, where: "auto" };
      out.push(r.body);
    }
    const last = out[out.length - 1];
    if (last) {
      menuSaved = clone(last.menu); menu = clone(menuSaved);
      autoSaved = clone(last.auto); auto = clone(autoSaved);
      view = last;
      renderMenu(); renderAuto();
    }
    return { ok: true, view: last };
  }

  function revert() {
    if (!menuSaved) return;
    menu = clone(menuSaved); auto = clone(autoSaved);
    renderMenu(); renderAuto();
  }

  // ---- the controls ----

  $("menu-on").addEventListener("change", () => { menu.on = $("menu-on").checked; changed(); });
  $("menu-main").addEventListener("change", () => { menu.main = $("menu-main").value; renderMenu(); changed(); });
  $("menu-welcome").addEventListener("change", () => { menu.welcome = $("menu-welcome").value; renderMenu(); changed(); });
  $("screen-add").addEventListener("click", () => {
    const s = { id: newID(), name: "Новый раздел", text: "", back: true, rows: [] };
    menu.screens.push(s); openScreens.add(s.id); renderMenu(); changed();
  });
  $("menu-starter").addEventListener("click", () => {
    const st = starter();
    menu.screens = st.screens; menu.main = st.main; menu.welcome = st.welcome;
    openScreens = new Set(["main"]);
    renderMenu(); changed();
  });
  $("rule-add").addEventListener("click", () => {
    const r = { id: newID(), name: "Новое автосообщение", on: false, trigger: "start", delay: 60, message: { text: "", rows: [] } };
    auto.rules.push(r); openRules.add(r.id); renderAuto(); changed();
  });
  // The chats the panel has but does not hand to addons, pasted as Telegram ids: sent in parts.
  $("ids-text").addEventListener("input", () => {
    const n = new Set($("ids-text").value.match(/\d{5,15}/g) || []).size;
    $("ids-state").textContent = n ? "Найдено номеров: " + n : "";
  });
  $("ids-add").addEventListener("click", async () => {
    const ids = [...new Set($("ids-text").value.match(/\d{5,15}/g) || [])].map(Number).filter((x) => x > 0);
    if (!ids.length) { $("ids-state").textContent = "Номеров не нашлось."; return; }
    $("ids-add").disabled = true;
    let added = 0, last = null;
    for (let i = 0; i < ids.length; i += 5000) {
      $("ids-state").textContent = "Добавляю… " + Math.min(i + 5000, ids.length) + " из " + ids.length;
      const r = await rpc("POST", "/chats/import", { ids: ids.slice(i, i + 5000) }, 60000, 2);
      if (r.status !== 200) { $("ids-state").textContent = "Не добавилось: " + failText(r); $("ids-add").disabled = false; return; }
      added += r.body.added; last = r.body;
    }
    $("ids-add").disabled = false;
    $("ids-text").value = "";
    $("ids-state").textContent = "Готово: новых " + added + ", всего бот знает " + last.total + " чатов.";
    $("chats-count").textContent = "Бот знает " + last.total + " чатов" + (last.blocked ? ", из них " + last.blocked + " заблокировали бота" : "") + ".";
    loadBroadcast();
  });
  $("bc-when").addEventListener("change", () => { $("bc-at").hidden = $("bc-when").value !== "at"; drawStart(); });
  $("bc-start").addEventListener("click", startBroadcast);
  $("bc-refresh").addEventListener("click", loadBroadcast);
  // While a broadcast runs its progress is read every few seconds.
  setInterval(() => {
    if (!document.hidden && bc && bc.job && bc.job.state === "running") loadBroadcast();
  }, 3000);

  return { take, dirty, save, revert, setOnChange: (f) => { onChange = f; }, showError };
})();
