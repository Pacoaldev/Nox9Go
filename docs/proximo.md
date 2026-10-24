# Guía de Uso Personal — NOX9GO

Este proyecto es tu bifurcación privada y personal de 9router-go.
No hay GitHub Actions corriendo, no hay web pública ni lanzamientos que publicar.

---

## 1. Tareas de limpieza única

### 1.1 Matar el proceso del gateway viejo
Hay un proceso `9router-go.exe` corriendo en segundo plano que ocupa el puerto `20130`. Matarlo para liberar el puerto:

```powershell
Get-Process -Name "9router-go" -ErrorAction SilentlyContinue | Stop-Process -Force
```

### 1.2 Borrar la carpeta vieja vacía
La carpeta anterior `C:\Users\spano\Documents\PROYECTOS\9router-go` quedó vacía. Cierra OpenCode/VSCode y bórrala:

```powershell
Remove-Item -LiteralPath "C:\Users\spano\Documents\PROYECTOS\9router-go" -Recurse -Force
```

### 1.3 Reiniciar OpenCode
Reinicia OpenCode para que recargue `~/.config/opencode/opencode.jsonc` (se corrigió el prefijo de modelo para habilitar los subagentes).

---

## 2. Cómo compilar y arrancar NOX9GO

Desde `C:\Users\spano\Documents\PROYECTOS\NOX9GO`:

### Compilar el binario:
```powershell
go build -o nox9go.exe ./cmd/nox9go/
```

*(Si alguna vez tocas el frontend en `web/`, ejecuta `cd web; bun run build; cd ..` antes de compilar Go).*

### Ejecutarlo:
```powershell
.\nox9go.exe
```

Abre tu navegador en: **http://localhost:20130**
- Password inicial por defecto: `123456`
- Base de datos: Usa automáticamente `~/.nox9go` (o hereda `~/.9router` si ya tenías configuraciones).

---

## 3. Flujo de trabajo diario

1. Haces tus cambios en el código local.
2. Verificas compilando: `go build -o nox9go.exe ./cmd/nox9go/`
3. Subes tu backup a GitHub:
   ```powershell
   git add .
   git commit -m "mis cambios"
   git push origin main
   ```
   *(Cero esperas de CI, cero minutos gastados).*
