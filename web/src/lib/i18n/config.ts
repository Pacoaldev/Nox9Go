/** Dashboard UI locales. Only English + Spanish (Spain) — operator choice. */
export const LOCALES = ['en', 'es'] as const
export type Locale = (typeof LOCALES)[number]

export const DEFAULT_LOCALE: Locale = 'en'
export const LOCALE_COOKIE = 'locale'

export const LOCALE_NAMES: Record<Locale, string> = {
  en: 'English',
  es: 'Español (España)',
}

export function isSupportedLocale(locale: string): locale is Locale {
  return (LOCALES as readonly string[]).includes(locale)
}

/** Map browser / legacy codes onto the two supported locales. */
export function normalizeLocale(locale: string | null | undefined): Locale {
  if (!locale) return DEFAULT_LOCALE
  const raw = locale.trim()
  if (raw === 'en' || raw.toLowerCase().startsWith('en-')) return 'en'
  // es, es-ES, es-MX, … → Spain Spanish pack (only pack we ship)
  if (raw === 'es' || raw.toLowerCase().startsWith('es-')) return 'es'
  return DEFAULT_LOCALE
}
