### Dashboard language: English + Español (España)

- Ported upstream `decolua/9router` runtime DOM i18n (`src/i18n/runtime.js`)
  into `web/src/lib/i18n/`, limited to **English** and **Español (España)**.
- Ships `web/public/i18n/literals/es.json` (upstream pack + NOX9GO nav/UI
  strings). Language switch lives in the TopBar account menu.
- Preference stored in `localStorage` (`nox9go-locale`) and a `locale` cookie.
- Coverage is literal-map based: untranslated English strings stay in English
  until added to `es.json`.
