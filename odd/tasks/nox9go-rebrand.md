# NOX9GO — rebrand de 9router-go

Objetivo: convertir este checkout de `9router-go` en **NOX9GO**, versión propia de Pacoaldev, apuntando al
repo nuevo `https://github.com/Pacoaldev/Nox9Go.git` (privado). Rama `main`, historial completo preservado.

Rama de trabajo: `main`. Estrategia de entrega: work-unit commits en la rama, force-push a `origin` (el repo
nuevo tiene 2 commits previos que se pierden a propósito). Runner de tests: `go test ./...` +
`make test-integration` + `cd web && bun test` + `make vet-svelte` + `make cross`.

## Specs

- **S1** — El módulo Go pasa de `9router/proxy` a `nox9go/proxy`. Todos los imports (`go.mod`, 1200
  referencias en 587 archivos) y el `-ldflags` de `Makefile` deben usar el path nuevo. `go build ./...` tiene
  que pasar sin tocar imports a mano.
- **S2** — El binario y la CLI pasan de `9router-go` a `nox9go`: `cmd/9router-go/` → `cmd/nox9go/`,
  `cmd/9router-tray/` → `cmd/nox9go-tray/`, `BINARY_NAME` del Makefile, nombres de artefactos del release
  workflow, mensajes de error y del updater. El alias `opencode-go` (binario renombrado) se conserva.
- **S3** — El directorio de datos por defecto pasa a `~/.nox9go` (`%APPDATA%/nox9go` en Windows). Si no
  existe una base de datos ahí pero sí existe en el directorio legacy `~/.9router`
  (`%APPDATA%/9router`), se adopta el legacy para no perder datos ni la compatibilidad de DB compartida con
  upstream 9Router. `DATA_DIR` explícito y `.env` mandan sobre todo lo demás. La CLI (`status|stop|logs`) y
  el servidor deben resolver el mismo directorio.
- **S4** — La URL del repositorio pasa de `luqman-v1/9router-go` a `Pacoaldev/Nox9Go`: `install.sh`,
  `install.ps1`, `internal/updater/updater.go` (`DefaultGitHubRepo`, `DefaultUpdateURL`), workflows de CI y
  release, y toda la documentación. El `User-Agent` del updater pasa a `nox9go/<version>`.
- **S5** — Se elimina la publicación de la imagen Docker: el job "Build and push Docker image" del release
  workflow, el `Dockerfile`, y las referencias a `luqmenul/9router-go` en el README. No queda ninguna
  promesa de imagen publicada.
- **S6** — La identidad web pasa a NOX9GO: `<title>` de `web/index.html`, `web/public/manifest.json`
  (name/short_name/description), textos de la UI, la página de callback OAuth (`9router — OAuth callback`,
  `9Router Relay`), el token local `sk-9router-local-token` → `sk-nox9go-local-token`, y la clave de config
  de OpenCode `model_providers.9router` → `model_providers.nox9go`.
- **S7** — La documentación vigente pasa a NOX9GO: `README.md`, `AGENTS.md`, `ARCHITECTURE.md`,
  `DATABASE.md`, `COMPARISON.md`, `ROADMAP.md`, `TECHNICAL_DEBT.md`, `CLAUDE.md` y `docs/*.md` de estado
  actual. `CHANGELOG.md` y `.changes/` **no** se reescriben (son historia). Las referencias a upstream
  (`decolua/9router`, `9router.com`, el producto Node.js "9Router") se conservan: son atribución y
  paridad, no identidad propia.
- **S8** — `LICENSE` suma `Copyright (c) 2026 Pacoaldev` conservando los créditos de `decolua and
  contributors` y `Luqmanul Hakim`.
- **S9** — Verificación verde: `go build ./...`, `go vet ./...`, `go test ./...`, `make test-integration`,
  `cd web && bun test`, `make vet-svelte`, y el gate de portabilidad de los 5 targets de `CROSS_TARGETS` con
  `CGO_ENABLED=0`.
- **S10** — `origin` apunta a `https://github.com/Pacoaldev/Nox9Go.git` y `main` se fuerza (force-push) con
  el historial completo tras el commit del rebrand.
- **S11** — La carpeta de trabajo se renombra de `9router-go` a `NOX9GO` en `C:\Users\spano\Documents\PROYECTOS`.
  `.git` viaja con la carpeta; el path del módulo Go no depende del nombre de la carpeta.
- **S12** — Los identificadores de proveedor y los prefijos de modelo **no** cambian: `ag`, `cc`, `cx`,
  `gh`, `oc`, `kr`, `vx`, etc., y `KnownProviders`, `RegistryAliases` y `ProviderAliasMap` quedan intactos.
  El aislamiento de proveedores (`AGENTS.md` §3) es un contrato, no un nombre.

## Tasks

| ID | Spec | Route | Status |
| --- | --- | --- | --- |
| T1 | S1 | inline | done — 580 `.go` + `go.mod`, `go build ./...` verde |
| T2 | S2 | inline | done — `cmd/nox9go/`, `cmd/nox9go-tray/`, `BINARY_NAME`, artefactos del release, updater, scripts |
| T3 | S3 | inline | done — `platformDataDirs()` + `dirHasDatabase()`, 3 tests nuevos en `internal/config` |
| T4 | S4 | inline | done — updater, `install.sh`, `install.ps1`, workflows, badges, docs |
| T5 | S5 | inline | done — job `docker` del release.yml, job `docker` del ci.yml, `Dockerfile`, `.dockerignore`, `docker-compose.yml`, `make docker`/`docker-build`, `scripts/notify-telegram.sh` |
| T6 | S6 | inline | done — `index.html`, `manifest.json`, `TopBar`, `CliToolsView`, claves de localStorage, `model_providers.nox9go`, `hasNox9Go` (Go + web) |
| T7 | S7 | inline | done — 40 documentos; `CHANGELOG.md` y `.changes/` intactos |
| T8 | S8 | inline | done — `Copyright (c) 2026 Pacoaldev` añadido, créditos de upstream conservados |
| T9 | S9 | inline | done — ver gates |
| T10 | S10 | inline | pending |
| T11 | S11 | inline | pending |

### Gates (S9)

| Gate | Resultado |
| --- | --- |
| `go build ./...` | OK |
| `go vet ./...` | OK |
| `go test ./...` | OK salvo `TestBrowserNamesCoverTheBrowsersTheFlowCanDrive` (preexistente en Windows: `browserNames()` devuelve `chrome/msedge/brave/chromium` y el test espera los nombres de Linux) |
| `make test-integration` | OK (`internal/integration` + `bootfx`) |
| `cd web && bun test src scripts` | OK salvo `types.test.ts` (preexistente: `369.000` vs `369,000`, formato numérico de la locale de Windows) |
| `make vet-svelte` (ratchet) | OK — 0 identificadores sin resolver, 78 errores = baseline |
| `oxlint --deny-warnings` | OK |
| `actionlint` | OK |
| Cross-compile `CROSS_TARGETS` con `CGO_ENABLED=0` | OK en los 5 targets |
| `go test ./web/...` | OK |
| `golangci-lint` | bloqueado por incompatibilidad de toolchain (Go 1.27 export data vs golangci-lint v2.6.2), no por el código |

## Log

- L1 (2026-10-24) — "Tendremos que actualizar tal y como se hizo en el README.md del proyecto 9NOX al de
  este proyecto, pero este proyecto que estamos tiene otras tecnologias. Fijate en lo que esta escrito
  actualmente antes de adaptarlo." (README reescrito a estilo 9NOX, 1088 líneas.)
- L2 (2026-10-24) — "Este proyecto para traerlo a mi PC use: git clone https://github.com/luqman-v1/9router-go.git,
  pero quiero que sea una version 'propia' mia, con lo que he creado un repositorio nuevo:
  https://github.com/Pacoaldev/Nox9Go.git (Este es el nuevo nombre de mi proyecto: 'NOX9GO') puedes hacer todo
  lo necesario para que apunte a mi repositorio nuevo, cambio de nombres, etc...? Puedo cambiar el nombre de
  mi carpeta en Windows al nuevo 9router-go por NOX9GO?"
- L3 (2026-10-24) — Decisiones del usuario: (a) data dir `~/.nox9go` con fallback automático a `~/.9router`
  si ahí está la base; (b) módulo Go `nox9go/proxy`; (c) quitar el job de Docker del release workflow;
  (d) force-push de `main` al repo nuevo (privado, ya tiene 2 commits previos que se pierden).
- L4 (2026-10-24) — Inventario previo al rename: 753 ocurrencias de `9router-go` en 127 archivos; 1200 de
  `9router/proxy` en 587; 66 de `luqman-v1` en 21; 6 de `luqmenul` en 4; 285 de `9router` fuera del path de
  módulo y del nombre del binario en `.go` (de las cuales 3 son comentarios de paridad con
  `decolua/9router`, que no se tocan).
- L5 (2026-10-24) — Restricción de la sesión: la delegación a subagentes está caída porque el proceso de
  OpenCode cachea `~/.config/opencode/opencode.jsonc`; el fix en disco (`9router-go/` → `9router/` en los
  `model` de agente) se aplica al reiniciar OpenCode. Todo el rebrand se ejecuta inline.
- L6 (2026-10-24) — Incidente: `README.md` quedó corrupto en disco con una sustitución global `a` → `n`
  (0 aes, 4363 enes). No se reprodujo con los scripts usados (bash, `edit`, `write` probados sobre archivos
  de prueba de 1 KB y de 1000 líneas, con y sin comandos de instalación): fue un fallo puntual del entorno.
  Único archivo afectado; los otros 736 se verificaron con un conteo de aes contra `HEAD`. Se reconstruyó el
  README y se re-verificó. **Mitigación aplicada: contar aes y ocurrencias de `dntnbnse` después de cada
  escritura sobre archivos de texto.**
- L7 (2026-10-24) — `PowerShell -ne` es case-insensitive: dos passes de renombre que solo cambiaban mayúsculas
  se aplicaron sin efecto. Corregido usando `-cne` y re-aplicados. El `README.md` usa anchors con guion
  inicial solo en títulos que empiezan con emoji (`#-quick-start`); los que no empiezan con emoji van sin
  guion (`#environment`, `#where-the-data-lives`).
