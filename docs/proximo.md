# Próximos pasos — NOX9GO

Estado al cierre de la sesión del 2026-10-24. Todo lo de acá está **verificado contra el código o contra
GitHub**, no supuesto. Marcá cada casilla cuando la resuelvas.

---

## 1. Cosas que YA están hechas

- [x] Rebrand completo a **NOX9GO**: módulo Go `nox9go/proxy`, binario `nox9go`, carpetas `cmd/nox9go/` y
      `cmd/nox9go-tray/`, dashboard, documentación, workflows, instaladores.
- [x] `origin` apunta a `https://github.com/Pacoaldev/Nox9Go.git` (privado) y `main` está pusheada.
- [x] LICENSE con `Copyright (c) 2026 Pacoaldev` conservando los créditos de `decolua` y de Luqmanul Hakim.
- [x] Data dir nuevo `~/.nox9go` con fallback automático al `~/.9router` legacy.
- [x] Publicación de la imagen Docker eliminada (jobs de release y CI, `Dockerfile`, `.dockerignore`,
      `docker-compose.yml`, `make docker`, `docker pull` del script de Telegram).
- [x] Verificación local en verde: `go build`, `go vet`, suite de integración, ratchet de svelte, `oxlint`,
      `actionlint` y cross-compile de los 5 targets con `CGO_ENABLED=0`.
- [x] La carpeta de trabajo está en `C:\Users\spano\Documents\PROYECTOS\NOX9GO`.

---

## 2. Pendientes que dependen de vos

### 2.1 Borrar la carpeta vieja `9router-go` (vacía)

OpenCode tenía esa carpeta como directorio de trabajo, así que Windows la tenía bloqueada y no se pudo
borrar. Ahora está vacía.

```powershell
# cerrá OpenCode primero, después:
Remove-Item -LiteralPath "C:\Users\spano\Documents\PROYECTOS\9router-go" -Recurse -Force
```

### 2.2 Cerrar el gateway viejo que sigue corriendo

Hay un proceso `9router-go.exe` (PID 16068) del binario anterior, sin ruta accesible. Es el daemon viejo
sirviendo en `:20130`; el nuevo `nox9go` no levanta mientras ese puerto esté ocupado.

```powershell
Get-Process -Name "9router-go" -ErrorAction SilentlyContinue | Stop-Process -Force
```

Ojo: eso mata el daemon viejo. Si tenés sesiones de Claude Code / Codex apuntando a `:20130`, se cortan.

### 2.3 Reiniciar OpenCode

El config global `~/.config/opencode/opencode.jsonc` tenía todos los `model` de agente apuntando al
provider inexistente `9router-go/`. Ya está corregido en disco, pero el proceso de OpenCode cachea el
config al arrancar: **la delegación a subagentes no vuelve hasta que reinicies**.

### 2.4 Decidir la visibilidad del repo — esto rompe el self-update

`nox9go update` consulta la API de releases de GitHub. Con el repo **privado** esa API responde `403/404`
sin token, así que el auto-update no le va a funcionar a nadie.

- **Opción A — repo público**: el self-update funciona para cualquiera, y el badge de Telegram y la página
  de GitHub Pages quedan accesibles. El costo: tu código queda visible (el MIT lo permite).
- **Opción B — repo privado + token**: dejalo privado y guardá un token de lectura en
  `~/.config/9router-go/token` (o la variable que prefieras) para que el updater pueda consultarlo. Hay que
  tocar `internal/updater/updater.go`.

Mientras no elijas, el self-update queda roto — pero solo eso: el resto de la app funciona igual.

### 2.5 Publicar los tags (opcional)

Los 80 tags del proyecto quedaron solo en tu máquina; el force-push de `main` no los sube. Sin un tag no hay
release, y sin release el `version.json` que consulta el updater no anuncia nada nuevo.

```bash
git push origin --tags
```

Recomendado: primero publicá un tag nuevo con la versión actual (v1.9.14) y solo después el resto, para
que la página de releases muestre una historia coherente.

### 2.6 Habilitar GitHub Pages (para el badge de Telegram)

El badge apunta a `https://pacoaldev.github.io/Nox9Go/tg`, que lo publica el workflow `pages.yml`. En el
repo: **Settings → Pages → Source: GitHub Actions**. Sin eso, el badge da 404.

### 2.7 Secrets del repo

El repo nuevo no tiene ningún secret. El workflow de release usa:

| Secret | Para qué | ¿Falta rompe el release? |
| --- | --- | --- |
| `GITHUB_TOKEN` | Lo genera GitHub solo | No |
| `TELEGRAM_BOT_TOKEN` | Aviso de release al grupo | No (`continue-on-error: true`) |
| `TELEGRAM_CHAT_ID` | Aviso de release al grupo | No (`continue-on-error: true`) |
| `TELEGRAM_TOPIC_ID` | Aviso de release al topic | No (`continue-on-error: true`) |

Los de Docker ya no hacen falta: ese job se eliminó. `docs/TELEGRAM_RELEASE_NOTIFICATIONS.md` tiene el
paso a paso para los de Telegram.

Si querés que el aviso salga a **tu** grupo y no al del upstream, cambiá el link en `site/tg.html`
(apunta a `https://t.me/+09wMec_Dc8A0Mzhl`, que es el grupo del autor original).

---

## 3. Bugs y fricción que ya están identificados

### 3.1 Bun local no parsea el lockfile (Windows)

`bun install --frozen-lockfile` falla con `UnknownLockfileVersion` porque tu Bun 1.3.14 no entiende el
`bun.lock` (`lockfileVersion: 2`). El CI usa Bun 1.4.2 y ahí funciona.

```powershell
cd web
bun install          # sin --frozen-lockfile
bun run build
git checkout -- web/bun.lock   # devolvé el lockfile, no lo commitees
```

Para no repetirlo: instalá Bun 1.4.x en tu máquina.

### 3.2 Dos tests que fallan en tu Windows (preexistentes, NO del rename)

- `TestBrowserNamesCoverTheBrowsersTheFlowCanDrive` — `browserNames()` devuelve `chrome/msedge/brave/chromium`
  en Windows, y el test espera los nombres largos de Linux (`brave-browser`, `microsoft-edge`).
- `web/src/components/analytics/types.test.ts` — el separador de miles depende de la locale: en Windows
  sale `369.000` y el test espera `369,000`.

Ninguno lo introdujo el rebrand, pero los verás fallar en local. En Linux el primero pasa.

---

## 4. CI: qué es obligatorio y qué es opcional

El `ci.yml` que se está ejecutando tiene 7 jobs. Para un proyecto personal, **no todos hacen falta**:

| Job | Qué protege | ¿Lo necesito? |
| --- | --- | --- |
| `Lint` | `oxlint` + `gofmt` + `go vet` | **Sí** — barato y atrapa ruido |
| `test` | `go test ./...` + suite del dashboard | **Sí** — es tu red de seguridad |
| `Cross-compile` | Que los 5 targets compilen | **Sí** — el release lo necesita igual |
| `Integration tests` | Routing real contra upstreams falsos | Recomendado — cubre la parte que más importa |
| `Race detector` | Carreras con `-race` | Opcional, es el que más tarda |
| `Service tests (Windows)` | Instalación de servicio en Windows | Opcional si no usás `service install` |
| `Changelog fragment` | Que cada PR deje su fragmento en `.changes/` | **Opcional en personal** — es una convención del proyecto upstream |

**Lo único que sí necesitás para publicar releases** es el workflow de `release.yml` (que se dispara con un
tag), no el `ci.yml`. `ci.yml` es calidad de vida.

Si querés aligerarlo, decime y dejo el `ci.yml` con `lint` + `test` + `cross-compile`, y quito el chequeo
de fragmento de changelog (que es lo que te va a frenar cada PR sin valor para un repo personal).

---

## 5. Primer arranque, en orden

```powershell
cd C:\Users\spano\Documents\PROYECTOS\NOX9GO

# 1. Cerrar el daemon viejo (libera el puerto 20130)
Get-Process -Name "9router-go" -ErrorAction SilentlyContinue | Stop-Process -Force

# 2. Construir el dashboard (el binario lo embebe)
cd web; bun install; bun run build; cd ..

# 3. Compilar
go build -o nox9go.exe ./cmd/nox9go/

# 4. Levantar
.\nox9go.exe

# 5. En otra terminal
Start-Process http://localhost:20130
```

Si el puerto 20130 está ocupado, probá con otro:

```powershell
$env:PORT=20131; .\nox9go.exe
```

El password por defecto es `123456` y **solo funciona en localhost**. Desde otra máquina te va a pedir
`INITIAL_PASSWORD` (CVE-2026-56679, el gateway bloquea el default en accesos remotos).

---

## 6. Lo que NO se tocó (a propósito)

- **El origen upstream.** NOX9GO sigue alimentándose de [`decolua/9router`](https://github.com/decolua/9router):
  se conserva la atribución, el nombre del producto "9Router", los IDs de proveedor y los prefijos de modelo
  (`ag`, `cc`, `cx`, `gh`, `oc`, `kr`...). Eso es contrato de compatibilidad, no identidad.
- **`CHANGELOG.md` y `.changes/`.** Son historia de releases; reescribirlos sería mentir.
- **La carpeta `9router`** de al lado: es el checkout del upstream y sirve para comparar.

---

## 7. Comandos de referencia

```bash
# gates completos, como los corre el CI
go vet ./...
go test -count=1 ./...
make test-integration
cd web && bun test src scripts && bun run ratchet:svelte
make cross

# release (dispara release.yml)
git tag v1.9.14 && git push origin v1.9.14

# ver el CI
gh run list --repo Pacoaldev/Nox9Go
gh run watch --repo Pacoaldev/Nox9Go
```
