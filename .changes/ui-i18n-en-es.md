### Dashboard language: English + Español (España)

- Ported upstream `decolua/9router` runtime DOM i18n into `web/src/lib/i18n/`,
  limited to **English** and **Español (España)**.
- Runtime also rewrites `title` / `placeholder` / `aria-label` / `alt`.
- `es.json` expanded to ~1.4k UI literals extracted from the dashboard
  (Spain Spanish tweaks for coste/ordenador and key nav strings).
- Language switch in the TopBar account menu; preference in
  `nox9go-locale` + `locale` cookie. Router serves `/i18n/*`.
