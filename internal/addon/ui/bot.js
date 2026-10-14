// The bot's settings page: the look of the buttons, the first screen and the texts (the menu,
// the automatic messages and the broadcasts are in extras.js). Edits stay in the page until
// «Сохранить»; a refresh never overwrites them.
"use strict";

(() => {
  const { $, el, rpc, failText, showError, toast, start, status, save, ago } = K;

  let saved = null;   // the settings as the addon has them
  let draft = null;   // the page's edit of them
  let emojiRows = []; // the text emoji as rows, empty ones kept while editing
  let view = null;    // the last status

  const clone = (v) => JSON.parse(JSON.stringify(v));
  const styleNames = { "": "Обычный", primary: "Синий", success: "Зелёный", danger: "Красный" };

  // comparable is the settings as saved: what the page compares to tell an edit.
  function comparable(s) {
    const icons = {};
    for (const [k, v] of Object.entries(s.look.icons || {})) {
      const id = (v.id || "").trim();
      if (id || v.style) icons[k] = { id, style: v.style || "" };
    }
    const emoji = {};
    for (const [c, id] of Object.entries(s.look.emoji || {})) if (c.trim() && String(id).trim()) emoji[c.trim()] = String(id).trim();
    const texts = {};
    for (const [k, v] of Object.entries(s.texts || {})) {
      const t = String(v).replace(/\r\n/g, "\n").replace(/[ \t\n]+$/, "");
      if (t.trim()) texts[k] = t;
    }
    const sorted = (o) => Object.fromEntries(Object.entries(o).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)));
    const hide = [...new Set((s.welcome && s.welcome.hide) || [])].sort();
    const ab = s.about || {};
    const about = { on: !!ab.on, label: (ab.label || "").trim(), text: (ab.text || "").replace(/[ \t\n]+$/, ""), support: !!ab.with_support,
      items: (ab.items || []).filter((x) => x.id).map((x) => x.id + "=" + (x.label || "").trim()) };
    return JSON.stringify({ on: !!s.look.on, strip: !!s.look.strip_emoji, icons: sorted(icons), emoji: sorted(emoji), texts: sorted(texts), hide, about, test: Number(s.test_chat) || 0 });
  }

  function fromInputs() {
    const emoji = {};
    for (const r of emojiRows) if (r.c.trim() || r.id.trim()) emoji[r.c.trim()] = r.id.trim();
    draft.look.emoji = emoji;
    const tc = $("test-chat").value.trim();
    draft.test_chat = /^\d+$/.test(tc) ? Number(tc) : 0;
    $("test-chat").classList.toggle("invalid", tc !== "" && !/^\d+$/.test(tc));
  }

  function dirty() {
    return settingsDirty() || X.dirty();
  }

  function settingsDirty() {
    if (!saved || !draft) return false;
    return comparable(saved) !== comparable(draft);
  }

  function changed() {
    fromInputs();
    $("savebar").hidden = !dirty();
  }

  function renderIcons() {
    const box = $("icons");
    box.replaceChildren();
    const own = view.buttons.filter((b) => b.key.startsWith("btn:") || b.key.startsWith("page:"));
    const bot = view.buttons.filter((b) => !own.includes(b));
    const group = (title, list) => {
      if (!list.length) return;
      box.append(el("div", { class: "icon-group", text: title }));
      for (const b of list) box.append(iconRow(b));
    };
    group("Кнопки бота", bot);
    group("Ваши кнопки меню из «Telegram → Меню и тексты»", own);
  }

  function iconRow(b) {
    const cur = (draft.look.icons || {})[b.key] || { id: "", style: "" };
    const id = el("input", { type: "text", inputmode: "numeric", spellcheck: "false", placeholder: "ID эмодзи", value: cur.id || "", "aria-label": "ID эмодзи: " + b.name });
    const sel = el("select", { "aria-label": "Цвет: " + b.name });
    for (const s of view.styles) sel.append(el("option", { value: s, text: styleNames[s] || s }));
    sel.value = cur.style || "";
    const sw = el("span", { class: "swatch " + (sel.value || "none") });
    const update = () => {
      const v = { id: id.value.trim(), style: sel.value };
      draft.look.icons = draft.look.icons || {};
      if (v.id || v.style) draft.look.icons[b.key] = v;
      else delete draft.look.icons[b.key];
      id.classList.toggle("invalid", v.id !== "" && !/^\d{5,25}$/.test(v.id));
      sw.className = "swatch " + (sel.value || "none");
      changed();
    };
    id.addEventListener("input", update);
    sel.addEventListener("change", update);
    id.classList.toggle("invalid", id.value !== "" && !/^\d{5,25}$/.test(id.value));
    return el("div", { class: "icon-row" },
      el("div", { class: "icon-name" }, el("b", { text: b.name }), b.hint ? el("small", { text: b.hint }) : null),
      id,
      el("span", { class: "style-opt" }, sw, sel));
  }

  // renderAbout: «О сервисе» — the switch, the button's label, the text and which of the
  // admin's buttons live there, each with a short label of its own.
  function renderAbout() {
    draft.about = draft.about || { on: false, label: "", text: "", items: [], with_support: true };
    const a = draft.about;
    a.items = a.items || [];
    $("about-on").checked = !!a.on;
    $("about-support").checked = !!a.with_support;
    $("about-label").value = a.label || "";
    $("about-label").placeholder = "ℹ️ О сервисе";
    $("about-text").value = a.text || "";
    $("about-text").placeholder = view.about_default || "";
    const box = $("about-items");
    box.replaceChildren();
    const own = view.buttons.filter((b) => b.key.startsWith("btn:") || b.key.startsWith("page:"));
    if (!own.length) {
      box.append(el("p", { class: "empty", text: "Своих кнопок-ссылок и страниц в меню панели нет." }));
      return;
    }
    for (const b of own) {
      const id = b.key.slice(b.key.indexOf(":") + 1);
      const cur = a.items.find((x) => x.id === id);
      const check = el("input", { type: "checkbox" });
      check.checked = !!cur;
      const label = el("input", { type: "text", maxlength: "40", spellcheck: "false", placeholder: b.name, value: cur ? cur.label || "" : "" });
      label.disabled = !cur;
      const sync = () => {
        const i = a.items.findIndex((x) => x.id === id);
        if (check.checked && i < 0) a.items.push({ id, label: label.value });
        if (!check.checked && i >= 0) a.items.splice(i, 1);
        if (check.checked) a.items.find((x) => x.id === id).label = label.value;
        label.disabled = !check.checked;
        changed();
      };
      check.addEventListener("change", sync);
      label.addEventListener("input", sync);
      box.append(el("div", { class: "about-item" },
        el("label", { class: "switch" }, check, el("span", {}, el("b", { text: b.name }), " ", el("small", { class: "muted", text: b.off ? "выключена в меню панели" : (b.key.startsWith("page:") ? "страница" : "ссылка") }))),
        label));
    }
  }

  // renderWelcome: a switch per admin's link or page button, on while it shows to newcomers.
  function renderWelcome() {
    const box = $("welcome");
    box.replaceChildren();
    const own = view.buttons.filter((b) => b.key.startsWith("btn:") || b.key.startsWith("page:"));
    if (!own.length) {
      box.append(el("p", { class: "empty", text: "Своих кнопок-ссылок и страниц в меню панели нет." }));
      return;
    }
    draft.welcome = draft.welcome || { hide: [] };
    for (const b of own) {
      const id = b.key.slice(b.key.indexOf(":") + 1);
      const input = el("input", { type: "checkbox" });
      input.checked = !b.off && !draft.welcome.hide.includes(id);
      input.disabled = !!b.off;
      input.addEventListener("change", () => {
        const hide = new Set(draft.welcome.hide);
        if (input.checked) hide.delete(id); else hide.add(id);
        draft.welcome.hide = [...hide];
        changed();
      });
      box.append(el("label", { class: "switch welcome-row" }, input,
        el("span", {}, el("b", { text: b.name }), " ", el("small", { class: "muted", text: b.off ? "выключена в меню панели" : (b.key.startsWith("page:") ? "страница" : "ссылка") }))));
    }
  }

  // The texts: a group each (open while it has changes or a search finds something in it),
  // a field per text with the built-in one as its placeholder.
  function textCount() {
    const n = Object.values(draft.texts || {}).filter((v) => String(v).trim()).length;
    $("texts-count").textContent = n ? "Изменено: " + n : "Все тексты встроенные";
  }

  function renderTexts() {
    const box = $("texts");
    box.replaceChildren();
    draft.texts = draft.texts || {};
    const groups = new Map();
    for (const t of view.texts) {
      if (!groups.has(t.group)) groups.set(t.group, []);
      groups.get(t.group).push(t);
    }
    for (const [name, list] of groups) {
      const changedIn = list.filter((t) => (draft.texts[t.key] || "").trim()).length;
      const badge = el("span", { class: "muted small", text: changedIn ? "изменено: " + changedIn : "" });
      const det = el("details", { class: "text-group", "data-group": name }, el("summary", {}, el("h3", { text: name }), badge));
      // The screens come first and open: they are what the page is mostly for.
      if (changedIn || box.childElementCount === 0) det.open = true;
      for (const t of list) det.append(textRow(t, () => {
        const n = list.filter((x) => (draft.texts[x.key] || "").trim()).length;
        badge.textContent = n ? "изменено: " + n : "";
      }));
      box.append(det);
    }
    textCount();
    filterTexts();
  }

  function textRow(t, onEdit) {
    const val = draft.texts[t.key] || "";
    const lines = Math.max((t.default || "").split("\n").length, val.split("\n").length);
    const tpl = !!t.base;
    const long = (t.default || "").length > 70 || lines > 1 || tpl;
    const input = long
      ? el("textarea", { rows: String(Math.min(Math.max(lines, tpl ? Math.max(4, t.base.split("\n").length) : 2), 12)), spellcheck: "false", placeholder: t.default || "Пусто — экран как в mikan" })
      : el("input", { type: "text", spellcheck: "false", placeholder: t.default });
    input.value = val;
    input.dataset.key = t.key;
    const reset = el("button", { type: "button", class: "small-btn", text: "Как было", disabled: !val });
    const set = (v) => {
      input.value = v;
      if (v.trim()) draft.texts[t.key] = v; else delete draft.texts[t.key];
      reset.disabled = !v;
      input.classList.remove("invalid");
      textCount();
      onEdit();
      changed();
    };
    input.addEventListener("input", () => set(input.value));
    reset.addEventListener("click", () => set(""));
    const head = el("div", { class: "text-head" }, el("b", { text: t.title }), reset);
    const row = el("div", { class: "text-row", "data-search": (t.title + " " + (t.default || "") + " " + val).toLowerCase() }, head, input);
    if (t.vars) row.append(el("small", { class: "muted" }, "Переменные: ", el("span", { class: "code", text: t.vars })));
    // Values the bot fills in (%s, %d): they may go by number, in any order, some left out.
    const verbs = (t.default || "").match(/%[-+# 0-9.]*[a-zA-Z]/g) || [];
    if (verbs.length && !tpl) {
      const after = (t.title || "").split(": ").slice(1).join(": ");
      const names = after ? after.split(", ").map((x) => x.replace(/^%[a-z]\s*—\s*/i, "")) : [];
      const list = verbs.map((_, i) => "%" + (i + 1) + (names[i] ? " — " + names[i] : "")).join(", ");
      const num = el("button", { type: "button", class: "small-btn", text: "Пронумеровать", onclick: () => {
        let i = 0;
        set((input.value || t.default || "").replace(/%%|%[-+# 0-9.]*[a-zA-Z]/g, (m) => (m === "%%" ? m : "%" + ++i)));
      } });
      row.append(el("small", { class: "muted" }, "Значения по номерам: " + list + ". Пишите %1, %2…, чтобы переставить их или убрать лишние; хотя бы одно оставьте. ", num));
    }
    if (t.hint) row.append(el("small", { class: "muted", text: t.hint }));
    if (tpl) {
      row.append(el("div", { class: "row wrap" }, el("button", { type: "button", class: "small-btn", text: "Взять за основу вид mikan", onclick: () => set(t.base) })));
    }
    return row;
  }

  // filterTexts shows the texts the search finds, and opens their groups.
  function filterTexts() {
    const q = $("texts-search").value.trim().toLowerCase();
    for (const det of document.querySelectorAll(".text-group")) {
      let shown = 0;
      for (const row of det.querySelectorAll(".text-row")) {
        const hit = !q || row.dataset.search.includes(q);
        row.hidden = !hit;
        if (hit) shown++;
      }
      det.hidden = q && !shown;
      if (q && shown) det.open = true;
    }
  }

  // markText marks the text a save was refused for, and opens its group.
  function markText(field) {
    if (!field || !field.startsWith("texts.")) return;
    const key = field.slice(6);
    const input = document.querySelector('[data-key="' + CSS.escape(key) + '"]');
    if (!input) return;
    showTab("texts");
    // A search may hide it: shown again with all the texts.
    if (input.closest(".text-row").hidden) {
      $("texts-search").value = "";
      filterTexts();
    }
    input.classList.add("invalid");
    const det = input.closest("details");
    if (det) det.open = true;
    input.focus();
  }

  function renderEmoji() {
    const box = $("emoji");
    box.replaceChildren();
    if (!emojiRows.length) box.append(el("p", { class: "empty", text: "Замен нет: эмодзи в текстах остаются обычными." }));
    emojiRows.forEach((r, i) => {
      const c = el("input", { type: "text", class: "char", value: r.c, placeholder: "📅", "aria-label": "Эмодзи" });
      const id = el("input", { type: "text", class: "id", inputmode: "numeric", spellcheck: "false", value: r.id, placeholder: "ID своего эмодзи", "aria-label": "ID своего эмодзи" });
      const bad = () => id.classList.toggle("invalid", id.value.trim() !== "" && !/^\d{5,25}$/.test(id.value.trim()));
      c.addEventListener("input", () => { r.c = c.value; changed(); });
      id.addEventListener("input", () => { r.id = id.value; bad(); changed(); });
      bad();
      const del = el("button", { type: "button", text: "Убрать", onclick: () => { emojiRows.splice(i, 1); renderEmoji(); changed(); } });
      box.append(el("div", { class: "emoji-row" }, c, id, del));
    });
  }

  function renderAll() {
    $("look-on").checked = !!draft.look.on;
    $("strip").checked = !!draft.look.strip_emoji;
    $("test-chat").value = draft.test_chat ? String(draft.test_chat) : "";
    emojiRows = Object.entries(draft.look.emoji || {}).map(([c, id]) => ({ c, id }));
    renderIcons();
    renderWelcome();
    renderAbout();
    renderEmoji();
    renderTexts();
    for (const id of ["look-on", "strip", "test-chat", "defaults", "clear-icons", "emoji-add", "texts-search", "about-on", "about-support", "about-label", "about-text"]) $(id).disabled = false;
    $("savebar").hidden = true;
  }

  function renderStatus() {
    const st = $("state");
    st.className = "pill " + (view.loaded ? "good" : "warn");
    const last = Math.max(view.settings.saved || 0, (view.menu && view.menu.saved) || 0, (view.auto && view.auto.saved) || 0);
    st.textContent = view.loaded ? (last ? "Сохранено " + ago(last) : "Настройки по умолчанию") : "Панель не ответила";
    $("f-version").textContent = "Версия бота " + view.version;
    const r = view.refused;
    $("refused").hidden = !r;
    if (r) {
      $("refused").textContent = "Telegram не принял оформление сообщения " + ago(r.at) + " (" + r.text + "), бот отправил его без иконок. Проверьте ID эмодзи и есть ли у владельца бота Telegram Premium.";
    }
  }

  async function load(first) {
    const r = await status(first);
    if (r.status !== 200) {
      showError("Не удалось загрузить: " + failText(r));
      return;
    }
    showError("");
    view = r.body;
    renderStatus();
    // A refresh never overwrites what is being edited.
    if (first || !settingsDirty()) {
      saved = clone(view.settings);
      draft = clone(view.settings);
      renderAll();
    }
    X.take(view, first);
    $("savebar").hidden = !dirty();
    $("app").removeAttribute("aria-busy");
  }

  async function doSave() {
    fromInputs();
    $("save").disabled = true;
    if (settingsDirty()) {
      const r = await save(draft);
      if (r.status !== 200) {
        $("save").disabled = false;
        showError("Не сохранилось: " + failText(r));
        markText(r.body && r.body.field);
        return;
      }
      view = r.body;
      saved = clone(view.settings);
      draft = clone(view.settings);
    }
    const x = await X.save();
    $("save").disabled = false;
    if (!x.ok) {
      showError("Не сохранилось (" + (x.where === "menu" ? "меню" : "автосообщения") + "): " + failText(x.r));
      showTab(x.where === "menu" ? "menu" : "auto");
      return;
    }
    if (x.view) view = x.view;
    showError("");
    renderStatus();
    renderAll();
    $("savebar").hidden = false;
    $("savebar-text").textContent = "";
    toast();
    setTimeout(() => { if (!dirty()) $("savebar").hidden = true; $("savebar-text").textContent = "Есть несохранённые изменения"; }, 1800);
  }

  // The tabs, the menu first. The sandboxed frame has no storage of its own: every visit
  // opens on the menu.
  function showTab(name) {
    for (const b of document.querySelectorAll(".tabs [data-tab]")) b.setAttribute("aria-selected", String(b.dataset.tab === name));
    for (const p of document.querySelectorAll(".tab[data-panel]")) p.hidden = p.dataset.panel !== name;
  }
  for (const b of document.querySelectorAll(".tabs [data-tab]")) b.addEventListener("click", () => showTab(b.dataset.tab));

  $("about-on").addEventListener("change", () => { draft.about.on = $("about-on").checked; changed(); });
  $("about-support").addEventListener("change", () => { draft.about.with_support = $("about-support").checked; changed(); });
  $("about-label").addEventListener("input", () => { draft.about.label = $("about-label").value; changed(); });
  $("about-text").addEventListener("input", () => { draft.about.text = $("about-text").value; changed(); });

  $("look-on").addEventListener("change", () => { draft.look.on = $("look-on").checked; changed(); });
  $("strip").addEventListener("change", () => { draft.look.strip_emoji = $("strip").checked; changed(); });
  $("texts-search").addEventListener("input", filterTexts);
  $("test-chat").addEventListener("input", changed);
  $("emoji-add").addEventListener("click", () => { emojiRows.push({ c: "", id: "" }); renderEmoji(); changed(); });
  $("defaults").addEventListener("click", () => {
    const d = clone(view.defaults);
    draft.look.icons = d.look.icons;
    draft.look.strip_emoji = d.look.strip_emoji;
    draft.look.emoji = d.look.emoji;
    emojiRows = Object.entries(draft.look.emoji).map(([c, id]) => ({ c, id }));
    renderIcons();
    renderEmoji();
    changed();
  });
  $("clear-icons").addEventListener("click", () => { draft.look.icons = {}; renderIcons(); changed(); });
  $("revert").addEventListener("click", () => { draft = clone(saved); renderAll(); X.revert(); $("savebar").hidden = true; showError(""); });
  X.setOnChange(changed);
  K.testChat = () => (draft && draft.test_chat) || 0;
  $("save").addEventListener("click", doSave);

  // Moving the settings: out as one text, in after a check of all of it.
  $("export").addEventListener("click", async () => {
    const r = await rpc("GET", "/export", undefined, 20000);
    if (r.status !== 200) { showError("Не выгрузилось: " + failText(r)); return; }
    $("export-text").value = JSON.stringify(r.body, null, 1);
    $("export-text").hidden = false;
    $("export-select").hidden = false;
    $("export-text").select();
  });
  $("export-select").addEventListener("click", () => { $("export-text").focus(); $("export-text").select(); });
  $("import").addEventListener("click", async () => {
    let body;
    try { body = JSON.parse($("import-text").value); } catch { $("import-state").textContent = "Это не тот текст: вставьте его целиком."; return; }
    if (dirty()) { $("import-state").textContent = "Сначала сохраните или отмените свои изменения."; return; }
    $("import").disabled = true;
    $("import-state").textContent = "Загружаю…";
    const r = await rpc("POST", "/import", body, 30000);
    $("import").disabled = false;
    if (r.status !== 200) { $("import-state").textContent = "Не загрузилось: " + failText(r); return; }
    $("import-state").textContent = "Загружено и сохранено.";
    $("import-text").value = "";
    view = r.body;
    saved = clone(view.settings);
    draft = clone(view.settings);
    renderStatus();
    renderAll();
    X.take(view, true);
  });

  start(load);
})();
