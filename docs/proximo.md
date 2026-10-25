# Guía de Uso Personal — NOX9GO

Bifurcación privada y personal de 9router-go en `Pacoaldev/Nox9Go`.
Sin GitHub Actions, sin web pública ni dependencias de publicación externa.

---

## 1. Gestión del Servicio en Windows

En Windows, un ejecutable en ejecución bloquea su sobreescritura. Si vas a recompilar, primero detené los procesos.

### Detener el gateway y tray:
```powershell
Stop-Process -Name "nox9go","nox9go-tray" -Force -ErrorAction SilentlyContinue
```

### Iniciar (Gateway + Tray + Dashboard sin parpadeo de consola):
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\start-nox9go.ps1
```
*(O abrí el acceso directo `NOX9GO.lnk` de tu Escritorio).*

---

## 2. Cómo Compilar

### 2.1 Si modificaste el Frontend (`web/`):
El frontend Svelte 5 se compila y se embebe dentro del binario Go (`//go:embed all:dist`). Si tocás archivos en `web/`:
```powershell
cd web
bun run build
cd ..
```

### 2.2 Compilar el Gateway (`nox9go.exe`):
```powershell
go build -ldflags "-X nox9go/proxy/internal/updater.CurrentVersion=1.0.0" -o nox9go.exe ./cmd/nox9go/
```

### 2.3 Compilar el Tray (`nox9go-tray.exe`):
El flag `-H=windowsgui` es obligatorio para evitar que abra ventana de Terminal:
```powershell
go build -ldflags "-H=windowsgui" -o nox9go-tray.exe ./cmd/nox9go-tray/
```

---

## 3. Datos y Accesos

- **Dashboard:** [http://localhost:20130](http://localhost:20130) (o `/dashboard`)
- **Password inicial:** `123456`
- **Base de datos y estado:** En esta máquina resuelve a `%APPDATA%\9router\db\data.sqlite` (heredada de la instalación previa; tiene prioridad automática sobre `%USERPROFILE%\.nox9go`).
- **Túnel Cloudflare:** Administrable desde el Dashboard (Endpoint). Genera URL corta en `https://r<id>.abc-tunnel.us` y guarda estado en `%APPDATA%\9router\tunnel\state.json`.

---

## 4. Flujo de Trabajo Diario con Git

`rtk` no está instalado en este entorno de Windows. Usar comandos `git` estándar:

```powershell
git status
git add .
git commit -m "feat/fix: descripción del cambio"
git push origin main
```

*(O enviar "C&P" en el chat para ejecutar commit + push automático).*

### Tags:
Los 80 tags de upstream fueron purgados. Existe únicamente la etiqueta personal:
- `v1.0.0` (apunta al commit actual).
- Para sincronizar el tag en GitHub: `git push origin v1.0.0`
