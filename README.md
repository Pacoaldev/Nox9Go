<div align="center">

<img src="assets/branding/NOX9GO_logo.svg" alt="NOX9GO" width="320"/>

# NOX9GO

### Un gateway de IA ligero, escrito en Go, para no interrumpir tu flujo de desarrollo

[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Svelte](https://img.shields.io/badge/Dashboard-Svelte%205-FF3E00?logo=svelte&logoColor=white)](https://svelte.dev)
[![Runtime](https://img.shields.io/badge/Runtime-Single%20binary-4C1D3D)](#instalacion)
[![Providers](https://img.shields.io/badge/Providers-142%2B-6E56CF)](#proveedores)
[![License](https://img.shields.io/badge/Status-Personal%20fork-8B5CF6)](#sobre-este-fork)
[![Made with GentleAI](https://img.shields.io/badge/Made%20with-GentleAI-8B5CF6?logo=openai&logoColor=white)](https://github.com/Gentleman-Programming/gentle-ai)

**NOX9GO** expone un único endpoint compatible con OpenAI y Anthropic para que tus herramientas de código puedan usar múltiples proveedores de IA, cuotas, claves y modelos sin cambiar de configuración.

Incluye compresión de contexto, failover automático, panel embebido, métricas de uso y soporte para texto, imágenes, voz, embeddings y búsqueda web.

[Empezar](#empezar) · [Arquitectura](#arquitectura) · [Proveedores](#proveedores) · [Funciones](#funciones) · [CLI](#cli) · [Preguntas](#preguntas-frecuentes)

</div>

---

## Sobre este fork

NOX9GO es un fork personal de [9Router](https://github.com/decolua/9router), reescrito en Go como binario autónomo.

Mantiene compatibilidad con el formato de API y la base de datos SQLite del proyecto original, pero elimina la dependencia de Node.js en ejecución y añade un panel Svelte 5 embebido, observabilidad ampliada, más estrategias de enrutado y empaquetado multiplataforma.

> Este repositorio es de uso personal. No publica instaladores ni releases públicos.

---

## Empezar

### Requisitos

- Go 1.27
- Bun 1.x, solo para compilar el dashboard
- Un puerto libre, por defecto `20130`

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

La contraseña inicial es:

```text
123456
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
| `sticky` | Mantiene temporalmente un cliente en el mismo modelo |
| `capacity` | Elige la entrada con más capacidad disponible |
| `fusion` | Combina respuestas de varias entradas |

También puedes rotar varias cuentas del mismo proveedor y asociar pools de proxy a una conexión concreta o a varias a la vez.

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

## Proveedores

NOX9GO incluye 142 proveedores registrados y permite añadir endpoints compatibles con OpenAI o Anthropic desde el panel.

### Suscripciones y OAuth

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
| `kr` | Kiro AI | Aproximadamente 50 créditos mensuales |
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
| `nox9go` | Arranca el gateway en primer plano |
| `nox9go start` | Arranca en segundo plano |
| `nox9go stop` | Detiene el proceso en segundo plano |
| `nox9go restart` | Reinicia el gateway |
| `nox9go status` | Muestra estado, PID, panel y logs |
| `nox9go logs -n 100` | Muestra las últimas 100 líneas de log |
| `nox9go service install` | Registra el arranque automático |
| `nox9go service status` | Consulta el servicio registrado |
| `nox9go service uninstall` | Elimina el arranque automático |
| `nox9go version` | Muestra versión, sistema y arquitectura |
| `nox9go mitm enable` | Activa los comandos de proxy MITM |

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
| `DATA_DIR` | `~/.nox9go` | Directorio de datos |
| `DB_PATH` | `DATA_DIR/db/data.sqlite` | Ruta de la base SQLite |
| `RTK_ENABLED` | `true` | Activa o desactiva RTK |
| `CAVEMAN_ENABLED` | `false` | Activa el estilo Caveman |
| `PONYTAIL_ENABLED` | `false` | Activa el estilo Ponytail |
| `ADHD_ENABLED` | `false` | Activa el estilo ADHD |
| `INJECTION_GUARD_DISABLED` | `false` | Desactiva la protección contra prompt injection |
| `AUTO_UPDATE` | `false` | Permite actualizaciones automáticas |

---

## Base de datos compartida

NOX9GO puede usar el mismo directorio de datos que 9Router.

```bash
export DATA_DIR="/ruta/compartida/con/9router"
./nox9go start
```

Al apuntar ambos proyectos al mismo `DATA_DIR`, comparten:

- Proveedores
- Conexiones
- API keys
- Combos
- Historial de uso

No requiere importación ni migración.

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
