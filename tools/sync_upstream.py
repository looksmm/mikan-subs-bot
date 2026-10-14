#!/usr/bin/env python3
"""Вытаскивает официального бота из исходников mikan в этот проект (ветка upstream).

    python3 tools/sync_upstream.py <исходники mikan на нужном теге> <тег, например v0.5.1.0-rc.9>

Копирует cmd/mikan-telegram и internal/panel/tgbot (без local.go: это сторона панели), а из
остальных пакетов панели (store/db, domain, billing, events, tgate) берёт только те объявления,
на которые бот ссылается, со всем, от чего они зависят (tools/extract). Пути импорта
mikan/internal/... меняются на subsbot/internal/...

Правки делаются только в ветке upstream; main забирает их обычным git merge.
"""

import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

OUT = Path(__file__).resolve().parent.parent
GOIMPORTS = ["go", "run", "golang.org/x/tools/cmd/goimports@v0.40.0"]

# Пакет проекта -> папка в internal/panel; и что из него не нужно никогда (сторона панели).
PACKAGES = {
    "db": ("store/db", []),
    "domain": ("domain", []),
    "billing": ("billing", ["Service", "Notifier", "Telegram"]),
    "events": ("events", []),
    "tgate": ("tgate", ["Gateway"]),
}

IMPORTS = [
    (re.compile(r'"mikan/internal/panel/store/db"'), '"subsbot/internal/db"'),
    (re.compile(r'"mikan/internal/panel/([a-z]+)"'), r'"subsbot/internal/\1"'),
    (re.compile(r'"mikan/internal/([a-z]+)"'), r'"subsbot/internal/\1"'),
]

BACKEND_CHECKS = re.compile(
    r"// The panel's pieces and the addon's are what they say they are\.\nvar \(\n.*?\n\)\n", re.S
)
BACKEND_CHECK = (
    "// The addon's backend is what the screens use. (The panel's own pieces, Local and the\n"
    "// billing service, stay in mikan.)\nvar _ Backend = (*Remote)(nil)\n"
)


def rewrite(path: Path) -> None:
    s = path.read_text()
    for rx, repl in IMPORTS:
        s = rx.sub(repl, s)
    path.write_text(s)


def run(*args: str, cwd: Path | None = None) -> None:
    subprocess.run(args, cwd=cwd, check=True)


def go_files(*dirs: Path) -> list[Path]:
    return [f for d in dirs for f in d.rglob("*.go") if not f.name.endswith("_test.go")]


def main() -> None:
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    src, tag = Path(sys.argv[1]).resolve(), sys.argv[2]
    panel = src / "internal/panel"
    if not (src / "cmd/mikan-telegram/main.go").is_file():
        sys.exit(f"нет {src}/cmd/mikan-telegram: это не исходники mikan с ботом-аддоном")

    work = Path(tempfile.mkdtemp())
    try:
        extract = work / "extract"
        run("go", "build", "-o", str(extract), ".", cwd=OUT / "tools/extract")

        for name in ["tgbot", "textutil", *PACKAGES]:
            shutil.rmtree(OUT / "internal" / name, ignore_errors=True)
            (OUT / "internal" / name).mkdir(parents=True)
        (OUT / "cmd/subs-bot").mkdir(parents=True, exist_ok=True)

        shutil.copy(src / "cmd/mikan-telegram/main.go", OUT / "cmd/subs-bot/main.go")
        for f in (panel / "tgbot").glob("*.go"):
            if f.name != "local.go" and not f.name.endswith("_test.go"):
                shutil.copy(f, OUT / "internal/tgbot")
        for f in (src / "internal/textutil").glob("*.go"):
            if not f.name.endswith("_test.go"):
                shutil.copy(f, OUT / "internal/textutil")
        for f in go_files(OUT / "cmd/subs-bot", OUT / "internal/tgbot"):
            rewrite(f)

        backend = OUT / "internal/tgbot/backend.go"
        s, n = BACKEND_CHECKS.subn(BACKEND_CHECK, backend.read_text())
        if n != 1:
            sys.exit("backend.go: блок проверок интерфейсов не найден, поправьте tools/sync_upstream.py")
        backend.write_text(s)

        # Имена, на которые ссылаются файлы проекта, до неподвижной точки: billing ссылается
        # на domain, domain на db, и так далее.
        names: dict[str, list[str]] = {}
        for _ in range(8):
            changed = False
            for pkg, (pdir, skip) in PACKAGES.items():
                rx = re.compile(rf"\b{pkg}\.([A-Z][A-Za-z0-9_]*)")
                want = sorted({m for f in go_files(OUT / "cmd", OUT / "internal")
                               if f.parent.name != pkg for m in rx.findall(f.read_text())} - set(skip))
                if want == names.get(pkg):
                    continue
                names[pkg], changed = want, True
                out = OUT / "internal" / pkg / f"{pkg}.go"
                run(str(extract), "-dir", str(panel / pdir), "-pkg", pkg, "-skip", ",".join(skip),
                    "-out", str(out), *want)
                rewrite(out)
            if not changed:
                break

        # billing.AddonID проверяет id через addons.ValidID: одно регулярное выражение, без пакета.
        billing = OUT / "internal/billing/billing.go"
        s = billing.read_text()
        if "addons.ValidID" in s:
            valid = re.search(r"var validID = (.*)\n", (panel / "addons/addons.go").read_text()).group(1)
            s = re.sub(r"addons\.ValidID\((\w+)\)", r"validAddonID.MatchString(\1)", s)
            s = s.replace('\t"subsbot/internal/addons"\n', "")
            s += ("\n// validAddonID is mikan's addons.ValidID: adapter ids are path and provider-name safe.\n"
                  f"var validAddonID = {valid}\n")
            billing.write_text(s)

        run(*GOIMPORTS, "-w", str(OUT / "cmd/subs-bot"), *(str(OUT / "internal" / p) for p in ["tgbot", *PACKAGES]))
        run("go", "build", "./...", cwd=OUT)
        run("go", "vet", "./...", cwd=OUT)
        (OUT / "UPSTREAM").write_text(f"mikan {tag}\n")
        print(f"готово: mikan {tag}")
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    main()
