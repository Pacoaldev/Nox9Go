<div align="center">

<img src="assets/branding/NOX9GO_logo.svg" alt="NOX9GO" width="320"/>

# NOX9GO

### Un gateway de IA ligero, escrito en Go, para no interrumpir tu flujo de desarrollo

[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Svelte](https://img.shields.io/badge/Dashboard-Svelte%205-FF3E00?logo=svelte&logoColor=white)](https://svelte.dev)
[![Runtime](https://img.shields.io/badge/Runtime-Single%20binary-4C1D3D)](#instalacion)
[![Providers](https://img.shields.io/badge/Providers-178-6E56CF)](#proveedores)
[![License](https://img.shields.io/badge/License-MIT-8B5CF6)](LICENSE)
[![Gentle-ai](https://img.shields.io/github/v/release/Gentleman-Programming/gentle-ai?label=Gentle-ai&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABUAAAAcCAYAAACOGPReAAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAAWkSURBVEhLrZZZbFRVGMdPZ%2bnc2Zc7M%2ffOnZk7d6Z3OvveTmfrTGcYu1uKZbO0BUGBQqAsCmjBIWGLYi1GEWvdCImJIBhfjEtMNCaaGHnixWjE6JM%2b%2bWBMFAJ%2fMyUQOxKB4C85L%2bd83%2b%2bck%2fOdk0PIHdhBeE9j330xTWfHT7dP%2fDEbXf7VifCyZ18ODaw%2b1lJI1cfeJC7DckKUjTn%2fyTF9tvJqx8RffYkudHvTWOnNY6UjjkkhfXVrtPvrA20jv8z4ll4cJ1q6Mfe2zBjSsZOxkW8fTnWj4ghjM5%2fHXrYT2%2bk0xrh2xFkRLk7AxtQgTniH3mnM%2fxcnPAPH93mr10ejJbisTkz6KqgyQexmO7HbmMMWaxbb2Ry2cnnEGC8OxYbxvFC98IQ%2bkmx0kVUKRph1979xJDsKD%2btCVozCYbSijwuhohEwZo5jkzGJrYYUHqDceNrTgyLtxWhLFntCAzjhHTzb6CQzzuq5t3IbkIqloVCqEGfccGhpDNM%2bRCkbtrJ5TNlLmOAyWEvH0af1YJiJoGhtRUgM42h06S8zuqBpkfQZS3Hv4%2fFBxKIpUM0UVFI5sjoeEYrBiK0NAR2PhNqJoJbDkDmCMWsc65xtUMop%2bHxhbPZXcJqtBhZJzzt623e0VtCezMKsM0FKCCJqDnaVBYKGQdkgYhUdwR62iEl7ARssSTBEDplKhUowgwO%2bnktfOjKLS2yi2dM36ey41hloQ8QfhZJIYNAawautECkzMgY3EowXAYcXdsYJs1wLLZEg6PRhOtKP%2fVx2tu6pESJZENZKJdlj%2blD5IJeb3tmS%2f7UvWUDCE4JaTkEtVYCVamCgLWgRvBAFL8y0FZRUjoiRR587CZfahC2O7JV5ayG6aKX72Y5ATR0NX%2fAvoV9K9qxbH82h1x1HXs9DLVOAolSwGC0w6IywKLQoqF2Iax1QEAkcFideT47jRWd1aqMmZSZzqZT8LFkhrYtBSNPNSQ6L6e8DMg2CejvKrB8Zk2th5YomGVopCwwSBZQSGTgzt1BSp3xLz%2byxpx276PY15OyKG8KbPK4KspOEN77b8VD%2fc94iBKkK9fmclB55nRO6Jjn0pBkcZUDY6sEGdwHHxd73HiRq5pakps5aa7Z84foL3yk%2bahsLn%2fEODbztHhpAsNZ8bsmartdCfReHTAKUEiksEgqCVIOC3o7ljtDlTXz6s5fE%2fqM1YlNtMyTGn6Bz2gXpU6aMfY%2b7NNfrjv9Y4PzXylY%2fRrgE1tgSl44GHuj4MDlUnrQGzmX1tt8tEgViFP3D8UBp%2fNP4SO50sMr%2fY5NkShvxTZmS6251pGziN1YtDQ9lgV1Bg1PS6KV9OOjtWlsfTxgtR4IS3ZWTzR2Bfl3r3qOu4qvzHcMLxf6ku9i5yxRfsVOXMdXbLekjocwEp7egpHZj0tQOu0QDTZMCJT3%2f57lAb%2bLRWHtcoDSX12rEDW%2f7VnP1nL2%2b8voeT%2bzrZY7EJ%2fssCRHArYNe4KfRfcYqG%2fjdJNOCk%2brQ1mRFlXDgJTos0fEHAEhjZu5qxeZaRgiRZM3Cx1vsnSiZWrGKCXXts7Z5thFRsUha5ylH6ZUs7YFfxUIvVYNv0qCTMIg26c98MbiRLztbLtXj5loePFVWecBI9cgZXb%2f%2btnTK0OhaxGxqoGu1kJjPOcTzOcZ9fKVO2L9bET4yHcgWh13%2b90FKsnWa0PW4nEWXTsQk2zaPUk3WWJp3ZD7cG32I93%2bwjPEdfNggljoU5kvdVvHzaU%2f55JuuHqEx%2fq7A3Jw8Z%2bGvlaXMqcO2TPL6zi%2bVl4VRV2PcPXEkUYlGtczPgxJ20wVSDJwleb5GYvEVJNjcGHvXdOtcF9MS02ftxNgzTRIu8XanfC%2fk5MyheJPx%2fHYiDq4nvhvX8H4Y07e6K0pu8zHi%2fX9%2bKfVncEzR6l5PWBdI7cZrfpf8DeRY4wJrYaUrAAAAAElFTkSuQmCC)](https://github.com/Gentleman-Programming/gentle-ai)

![GitHub All Releases](https://img.shields.io/github/downloads/Pacoaldev/Nox9Go/total?style=flat-square&color=blue)
![GitHub Stars](https://img.shields.io/github/stars/Pacoaldev/Nox9Go?style=flat-square)
![GitHub Forks](https://img.shields.io/github/forks/Pacoaldev/Nox9Go?style=flat-square)
![GitHub issues](https://img.shields.io/github/issues/Pacoaldev/Nox9Go?style=flat-square)
![GitHub pull requests](https://img.shields.io/github/issues-pr/Pacoaldev/Nox9Go?style=flat-square)

![GitHub Release](https://img.shields.io/github/v/release/Pacoaldev/Nox9Go?style=flat-square)


**NOX9GO** expone un único endpoint compatible con OpenAI y Anthropic para que tus herramientas de código puedan usar múltiples proveedores de IA, cuotas, claves y modelos sin cambiar de configuración.

Incluye compresión de contexto, failover automático, panel embebido, seguimiento de cuotas y uso (no en todos los proveedores), y soporte para texto, imágenes, voz, embeddings y búsqueda web.

[Empezar](#empezar) · [Arquitectura](#arquitectura) · [Proveedores](#proveedores) · [Funciones](#funciones) · [CLI](#cli) · [Preguntas](#preguntas-frecuentes)

</div>

---

## Sobre este fork

NOX9GO es un fork de [9Router](https://github.com/decolua/9router), reescrito en Go como binario autónomo.

Mantiene compatibilidad con el formato de API y la base de datos SQLite del proyecto original, pero elimina la dependencia de Node.js en ejecución y añade un panel Svelte 5 embebido, observabilidad ampliada, más estrategias de enrutado y empaquetado multiplataforma.

> Repositorio público. Las releases se publican como tags en GitHub.

---

## Empezar

### Requisitos

- Go 1.27
- Bun 1.4.x (se pin a 1.4.4 en `web/bun.lock`; otro major rechaza el lockfile y lo reescribe), solo para compilar el dashboard
- Un puerto libre, por defecto `20130`
- GNU Make 3.82 o superior (`make` lee `VERSION` con `$(file <VERSION)`, que en versiones anteriores se expande vacío)

### Compilar y arrancar

```bash
git clone https://github.com/Pacoaldev/Nox9Go.git
cd Nox9Go

make web-build
make build

./nox9go start
```

Abre el panel:

```text
http://localhost:20130
```

La contraseña inicial se genera **aleatoriamente en el primer arranque** (24 caracteres) y se imprime **una sola vez** en la salida del gateway. El primer login con ella obliga a cambiarla (`403 mustChangePassword`). Si no la ves, busca en el log del gateway o configúrala tú con `INITIAL_PASSWORD`:

```text
INITIAL_PASSWORD=mipassword
```

### Conectar una herramienta

Configura tu CLI o IDE para usar el endpoint local:

```text
Base URL: http://localhost:20130/v1
API Key:  La que generes en Dashboard → API Keys
```

Ejemplo para Claude Code:

```bash
export ANTHROPIC_BASE_URL="http://localhost:20130"
export ANTHROPIC_API_KEY="<tu-api-key>"
```

Ejemplo para Cursor:

```text
Override OpenAI Base URL: http://localhost:20130/v1
OpenAI API Key: <tu-api-key>
```

---

## Arquitectura

NOX9GO actúa como una capa intermedia entre tus herramientas de desarrollo y los proveedores de IA.

```text
┌────────────────────────────┐
│  Claude Code · Codex       │
│  Cursor · Cline · OpenCode │
└─────────────┬──────────────┘
              │
              ▼
┌──────────────────────────────────────────┐
│                NOX9GO                    │
│                                          │
│  • Endpoint unificado                    │
│  • Traducción de formatos                │
│  • Compresión RTK                        │
│  • Enrutado por combos                   │
│  • Control de cuotas y failover          │
│  • Panel y métricas embebidos            │
└─────────────┬────────────────────────────┘
              │
              ▼
┌──────────────────────────────────────────┐
│  Proveedores de IA                       │
│  Suscripciones · APIs · modelos gratis   │
└──────────────────────────────────────────┘
```

En lugar de apuntar cada herramienta a un proveedor distinto, todas apuntan a NOX9GO. El gateway decide a qué modelo o proveedor enviar cada petición según disponibilidad, cuota, coste y estrategia configurada.

---

## Funciones

### Ahorro de contexto

| Función | Descripción |
|---|---|
| **RTK** | Comprime salidas de herramientas como `git diff`, `grep`, `ls` o `tree` antes de enviarlas al modelo. Activado por defecto. |
| **Headroom** | Detiene la generación cuando la respuesta supera el umbral de tokens configurado. |
| **Caveman** | Reduce la verbosidad de las respuestas conservando el contenido técnico. |
| **Ponytail** | Favorece código mínimo, directo y orientado a YAGNI. |
| **ADHD** | Prioriza respuestas orientadas a la siguiente acción. |
| **Injection guard** | Detecta intentos de prompt injection en la salida de herramientas. Activado por defecto. |

RTK está pensado para reducir el consumo de tokens en contextos con mucha salida de terminal, sin romper la petición si un filtro falla.

### Enrutado inteligente

Los **combos** agrupan varios modelos o proveedores bajo un mismo nombre.

| Estrategia | Comportamiento |
|---|---|
| `fallback` | Prueba los modelos en orden hasta que uno responde |
| `round-robin` | Reparte las peticiones entre los modelos del combo |
| `capacity` | Elige la entrada con más capacidad disponible |
| `fusion` | Combina respuestas de varias entradas |

La **rotación de cuentas** del mismo proveedor es independiente de la estrategia del combo y acepta `none`, `round-robin`, `random` y `sticky` (esta última mantiene temporalmente a un cliente en la misma cuenta). También puedes asociar pools de proxy a una conexión concreta o a varias a la vez.

### Observabilidad

- Registro en vivo por SSE con filtros por nivel y búsqueda.
- Inspector de peticiones y respuestas.
- Métricas de tokens, coste estimado, caché y compresión.
- Análisis por modelo, proveedor y periodo personalizado.
- Detección de peticiones bloqueadas o lentas.

### Más allá del chat

El mismo gateway puede enrutar:

- Generación de imágenes
- Vídeo
- Texto a voz
- Voz a texto
- Embeddings
- Búsqueda web
- Lectura de páginas

---

## Screenshots

<details>
<summary>Galería con las principales vistas del panel y la CLI (11 capturas)</summary>

<table>
  <tr>
    <td align="center"><b>1 · Endpoint & API Key</b><br><img src="assets/screenshots/1Endpoint & Key.png" alt="Endpoint & API Key" width="480"/></td>
    <td align="center"><b>2 · Proveedores (1/2)</b><br><img src="assets/screenshots/2Providers1.png" alt="Proveedores 1" width="480"/></td>
  </tr>
  <tr>
    <td align="center"><b>3 · Proveedores (2/2)</b><br><img src="assets/screenshots/3Providers2.png" alt="Proveedores 2" width="480"/></td>
    <td align="center"><b>4 · Combos</b><br><img src="assets/screenshots/4Combos.png" alt="Combos" width="480"/></td>
  </tr>
  <tr>
    <td align="center"><b>5 · Uso (1/2)</b><br><img src="assets/screenshots/5Usage.png" alt="Uso 1" width="480"/></td>
    <td align="center"><b>6 · Uso (2/2)</b><br><img src="assets/screenshots/6Usage2.png" alt="Uso 2" width="480"/></td>
  </tr>
  <tr>
    <td align="center"><b>7 · Seguimiento de cuotas</b><br><img src="assets/screenshots/7Quota_tracker.png" alt="Quota tracker" width="480"/></td>
    <td align="center"><b>8 · Ahorro de tokens</b><br><img src="assets/screenshots/8Token_saver.png" alt="Token saver" width="480"/></td>
  </tr>
  <tr>
    <td align="center"><b>9 · CLI & IDE</b><br><img src="assets/screenshots/9CLI & Ide.png" alt="CLI & IDE" width="480"/></td>
    <td align="center"><b>10 · Combo Flow</b><br><img src="assets/screenshots/10ComboFlow.png" alt="Combo Flow" width="480"/></td>
  </tr>
  <tr>
    <td align="center" colspan="2"><b>11 · Ajustes</b><br><img src="assets/screenshots/11Settings.png" alt="Settings" width="480"/></td>
  </tr>
</table>

</details>

## Proveedores

NOX9GO incluye un catálogo de 178 proveedores registrados —9 de ellos ocultos en los selectores pero totalmente enrutables— y permite añadir endpoints compatibles con OpenAI o Anthropic desde el panel. "Registrado" significa que el proveedor está en el catálogo con su endpoint, su método de autenticación y sus modelos; no que cada integración esté verificada contra el servicio en cada release. Los catálogos de modelos cambian más rápido de lo que se puede revisar, así que un proveedor puede dejar de responder aunque siga listado.

### Suscripciones y OAuth

Algunos de estos servicios exigen credenciales de cliente OAuth propias: si es el caso, el panel lo indica al conectar la cuenta y `.env.example` muestra las variables que acepta.

| Prefijo | Proveedor |
|---|---|
| `cc` | Claude Code |
| `cx` | OpenAI Codex |
| `gh` | GitHub Copilot |
| `cu` | Cursor IDE |
| `ag` | Antigravity |
| `cl` | Cline |
| `xai` | xAI / Grok |
| `kimi` | Kimi |
| `windsurf` | Windsurf |
| `trae` | Trae |

### Opciones gratuitas

| Prefijo | Proveedor | Nota |
|---|---|---|
| `oc` | OpenCode Free | Sin autenticación |
| `kr` | Kiro AI | Cuota gratuita mensual limitada; el importe exacto lo fija el proveedor |
| `gc` | Gemini CLI | Nivel gratuito mediante OAuth |
| `mmf` | MiMo Code Free | Pool gratuito de código |
| `devin-cli` | Devin CLI | Permite usar parte de una suscripción existente |

### APIs y autoalojados

Entre otros: OpenAI, Anthropic, DeepSeek, Groq, Mistral, Together AI, Fireworks, Cerebras, OpenRouter, Azure OpenAI, AWS Bedrock, Vertex AI, Ollama local, Ollama Cloud, ComfyUI, SD Web UI y proveedores de búsqueda como Brave, Tavily, Exa, Serper o Firecrawl.

Los identificadores de modelo usan el formato:

```text
<prefijo>/<modelo>
```

Por ejemplo:

```text
kr/claude-sonnet-4.5
oc/gpt-5
glm/glm-4.6
```

---

## CLI

| Comando | Descripción |
|---|---|
| `nox9go` | Con una terminal abre el selector de interfaz; sin terminal arranca el gateway en primer plano |
| `nox9go start` | Arranca en segundo plano |
| `nox9go stop` | Detiene el proceso en segundo plano |
| `nox9go restart` | Reinicia el gateway |
| `nox9go status` | Muestra estado, PID, panel y logs |
| `nox9go status --json` | Devuelve el estado en JSON para scripts |
| `nox9go logs -n 100` | Muestra las últimas 100 líneas de log |
| `nox9go version` | Muestra versión, sistema y arquitectura |
| `nox9go update` | Comprueba e instala actualizaciones del gateway |
| `nox9go service install` | Registra el arranque automático |
| `nox9go service uninstall` | Elimina el arranque automático |
| `nox9go service start` / `stop` / `restart` | Arranca, detiene o reinicia el servicio registrado |
| `nox9go service status` | Consulta el servicio registrado |
| `nox9go mitm enable` / `disable` / `status` | Activa, desactiva o consulta el proxy MITM (redirección DNS y TLS en `:443`) |

Los modos de ahorro de contexto y las actualizaciones automáticas son **flags globales**, no subcomandos. Van antes del subcomando (`nox9go --caveman start`) y cada uno tiene su variable de entorno equivalente:

| Flag | Efecto |
|---|---|
| `--rtk` / `--rtk=false` | Activa o desactiva RTK (activo por defecto) |
| `--caveman` | Activa el estilo Caveman |
| `--ponytail` | Activa el estilo Ponytail |
| `--adhd` | Activa el estilo ADHD |
| `--auto-update` | Permite actualizaciones automáticas |
| `--no-injection-guard` | Desactiva la protección contra prompt injection |
| `--background`, `-d` | Equivale a `nox9go start` |

### Arranque automático

```bash
nox9go service install
nox9go service start
nox9go service status
```

El comando detecta el mecanismo nativo disponible:

- systemd
- launchd
- OpenRC
- runit
- cron
- Tarea programada de Windows

---

## Configuración

La configuración en ejecución se realiza mediante variables de entorno.

| Variable | Valor por defecto | Descripción |
|---|---|---|
| `PORT` | `20130` | Puerto HTTP del gateway |
| `HOST` | Localhost | Interfaz de escucha |
| `DATA_DIR` | `%APPDATA%\nox9go` (Windows) / `~/.nox9go` (resto) | Directorio de datos |
| `DB_PATH` | `DATA_DIR/db/data.sqlite` | Ruta de la base SQLite |
| `RTK_ENABLED` | `true` | Activa o desactiva RTK |
| `CAVEMAN_ENABLED` | `false` | Activa el estilo Caveman |
| `PONYTAIL_ENABLED` | `false` | Activa el estilo Ponytail |
| `ADHD_ENABLED` | `false` | Activa el estilo ADHD |
| `INJECTION_GUARD_DISABLED` | `false` | Desactiva la protección contra prompt injection |
| `AUTO_UPDATE` | `false` | Permite actualizaciones automáticas |
| `INITIAL_PASSWORD` | aleatoria en primer arranque | Contraseña del panel; si se genera, se imprime una vez y obliga a cambiarla |
| `JWT_SECRET` | aleatoria y persistida en el data dir | Secreto de firma de la sesiones del panel; úsalo para fijar tu propio valor |
| `MINIMAX_CODE_SIGN_SECRET` | valor embebido | Opcional. Secreto de firma de la API de cuenta de MiniMax Code; el valor por defecto es el que publica el cliente oficial. Configúralo solo si MiniMax lo rota |
| `ALLOW_INSECURE_TLS` | `false` | Escape hatch intencional: omite la verificación TLS (no usar en producción) |
| `ANTIGRAVITY_OAUTH_CLIENT_SECRET` | **requerido** | Secreto OAuth de Antigravity; los IDs vienen con defaults públicos, los secretos no |
| `GEMINI_CLI_OAUTH_CLIENT_SECRET` | **requerido** | Secreto OAuth de Gemini CLI |
| `XAI_OAUTH_CLIENT_SECRET` | **requerido** | Secreto OAuth de xAI / Grok |
| `CODEX_OAUTH_CLIENT_SECRET` | **requerido** | Secreto OAuth de OpenAI Codex |
| `GITHUB_OAUTH_CLIENT_SECRET` | **requerido** | Secreto OAuth de GitHub Copilot |
| `IFLOW_OAUTH_CLIENT_SECRET` | **requerido** | Secreto OAuth de iFlow |
| `KIMI_CODING_OAUTH_CLIENT_SECRET` | **requerido** | Secreto OAuth de Kimi |
| `QWEN_OAUTH_CLIENT_SECRET` | **requerido** | Secreto OAuth de Qwen |
| `QODER_OAUTH_CLIENT_SECRET` | **requerido** | Secreto OAuth de Qoder |

---

## Base de datos compartida

NOX9GO usa el mismo esquema SQLite que 9Router, así que puede trabajar directamente sobre el directorio de datos de una instalación existente.

Cuando no defines `DATA_DIR`, la detección es automática y sigue este orden:

1. `DATA_DIR` del entorno
2. `DATA_DIR` del archivo `.env` (el del directorio actual o el que está junto al binario)
3. `%APPDATA%\nox9go` en Windows (`~/.nox9go` en el resto), si ya contiene una base
4. `%APPDATA%\9router` en Windows (`~/.9router` en el resto), si ya contiene una base
5. `%APPDATA%\nox9go` / `~/.nox9go` en caso contrario

Dentro del directorio elegido, el archivo se resuelve en este orden: `db/data.sqlite`, `data.sqlite`, `nox9go.db`, `9router.db`. El nombre que verás en una instalación de 9Router es `db/data.sqlite`.

Para forzar una ruta concreta:

```bash
export DATA_DIR="/ruta/compartida/con/9router"
./nox9go start
```

Al compartir `DATA_DIR` ambos proyectos leen y escriben lo mismo:

- Proveedores
- Conexiones
- API keys
- Combos
- Historial de uso

No requiere importación, pero comparten esquema y no hay migración: antes de apuntar aquí una instalación existente, haz una copia de la base de datos y de la configuración. Ambas versiones deben usar un esquema compatible — un 9Router más reciente puede haber añadido columnas que esta versión no conoce, y al revés. Si el esquema no coincide, restaura la copia.

---

## Compilación cruzada

Puedes generar binarios estáticos para varias plataformas:

```bash
make cross
```

Objetivos disponibles:

| Sistema | Arquitectura |
|---|---|
| Linux | amd64 |
| Linux | arm64 |
| macOS | amd64 |
| macOS | arm64 |
| Windows | amd64 |

La compilación usa `CGO_ENABLED=0`, por lo que el resultado es un binario autónomo sin dependencias externas.

---

## Dos ejecutables

El proyecto publica dos binarios con funciones distintas:

| Binario | Qué hace | Dónde |
|---|---|---|
| `nox9go` | El gateway: el servidor compatible con OpenAI y Anthropic, la CLI y el panel embebido. Es el que se ejecuta en todas las plataformas. | Linux, macOS, Windows |
| `nox9go-tray` | Un icono en la bandeja del sistema de Windows que arranca y para el gateway, y abre el panel. No sirve peticiones por sí mismo: habla con `nox9go`. | Solo Windows |

`make build` produce el primero y `make tray` el segundo. En Windows puedes usar `scripts/start-nox9go.ps1` para arrancar gateway y bandeja juntos y abrir el panel; en Linux y macOS no hay bandeja y el gateway se gestiona con `nox9go start` / `status` / `stop` o con `nox9go service install`.

---

### Herramientas adicionales

- **Túneles:** expone el gateway temporalmente mediante Cloudflare quick tunnel o Tailscale. Se gestiona desde el panel (`GET /api/tunnel/status`, `POST /api/tunnel/enable`, `POST /api/tunnel/disable`, más `GET /api/tunnel/tailscale-check` y `POST /api/tunnel/tailscale-enable` / `tailscale-disable`) y guarda el estado en el directorio de datos; no tiene comando de CLI propio.
- **CliToolsView / adaptador Pi:** conecta herramientas de línea de comandos al mismo catálogo de proveedores, cuentas y combos del panel.

---

## Preguntas frecuentes

### ¿NOX9GO cobra algo?

No. Es software que ejecutas en tu propia máquina. Los posibles costes dependen exclusivamente de los proveedores que configures.

### ¿El coste mostrado en el panel es una factura?

No. El panel calcula un coste estimado equivalente al uso de APIs de pago. Sirve para comparar ahorro, no para facturar.

### ¿RTK altera la respuesta del modelo?

RTK comprime la salida de herramientas antes de enviarla al modelo. Si el filtro falla o el resultado sería mayor que el original, se conserva el texto sin modificar.

### ¿Puedo usarlo sin pagar nada?

Sí. Puedes configurar proveedores gratuitos como OpenCode Free, Kiro AI o Gemini CLI y crear un combo con failover entre ellos.

### ¿Necesito Node.js para ejecutarlo?

No. Node.js y Bun solo son necesarios para compilar el dashboard desde el código fuente. El binario final es autónomo.

---

## Cómo accede a los proveedores (léelo antes de usar los proveedores "web")

NOX9GO habla con cada proveedor por su API oficial siempre que exista una. Pero algunos de los proveedores que ofrece funcionan de otra forma, y conviene saberlo antes de conectarlos y no descubrirlo después:

- **Reutiliza sesiones de navegador.** Para los proveedores que no tienen API pública (por ejemplo `perplexity-web`, `deepseek-web`, `gemini-web`, `chatgpt-web`, `grok-web`, `muse-web` y `uc-web`), el gateway puede leer la cookie de sesión de Chrome o Firefox en tu equipo para enviar las peticiones como lo haría el navegador. Eso significa que el proceso puede leer y descifrar el almacén de cookies de tu perfil.
- **Imita al cliente oficial.** En varios de estos casos el gateway se presenta ante el servicio con la identidad y la huella de red del cliente o CLI oficial (cabeceras de versión, huella TLS, marcas específicas de cada servicio). Es intencionado: es lo que hace que el servicio acepte la petición.
- **Esto puede incumplir los términos del servicio.** Muchos proveedores prohíben expresamente el acceso automatizado fuera de su API, o hacerlo con un cliente que no es el suyo. Usado contra un servicio de pago sin autorización, puede además contravenir normas sobre acceso no autorizado en tu jurisdicción.

Por eso:

- Úsalo con **tu propia cuenta**, en un equipo del que eres responsable.
- No lo uses para revender acceso, para compartir una cuenta de suscripción con terceros ni para eludir un límite de plan.
- Los proveedores con API oficial no tienen ninguna de estas características y son la opción recomendada para cualquier uso que no sea personal.
- Esta sección no es una autorización ni un consejo legal: cada usuario decide qué conecta y asume las consecuencias, como se detalla en el aviso legal de abajo.

---

## Aviso legal / Disclaimer

NOX9GO es un proyecto de software experimental desarrollado para fines de **investigación, interoperabilidad técnica y uso personal privado**.

- El software se proporciona "tal cual" (*as-is*), bajo licencia MIT, sin garantías explícitas ni implícitas de disponibilidad, rendimiento o idoneidad para un fin específico.
- Cada usuario u operador es el único responsable de asegurar que el uso del software cumpla con los Términos y Condiciones de Servicio (ToS), políticas de uso aceptable y límites contractuales de las APIs y proveedores de terceros con los que decida conectarse.
- Los desarrolladores y colaboradores de NOX9GO no asumen ninguna responsabilidad por el mal uso, suspensión de cuentas, cargos de facturación o cualquier otra consecuencia derivada del uso de servicios de terceros a través de este gateway.

---

## Enlaces

- Proyecto original: [9Router](https://github.com/decolua/9router)
- Autor del fork: [Pacoaldev](https://github.com/Pacoaldev)
- Panel local: <http://localhost:20130>

<div align="center">

**NOX9GO** · Un gateway, muchas rutas, menos interrupciones.

</div>

---

<p align="center">
  <a href="https://github.com/Gentleman-Programming/gentle-ai">
    <img width="220" src="https://raw.githubusercontent.com/Gentleman-Programming/gentle-ai/main/docs/assets/brand/built-with-gentle-ai.png" alt="Built with Gentle-AI" />
  </a>
</p>
