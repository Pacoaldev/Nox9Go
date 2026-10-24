/**
 * Runtime DOM translator — port of decolua/9router src/i18n/runtime.js,
 * extended to also rewrite title / placeholder / aria-label / alt.
 * English source strings stay in the Svelte markup; a locale map rewrites
 * the DOM. Only `en` and `es` are supported in NOX9GO.
 */
import {
  DEFAULT_LOCALE,
  LOCALE_COOKIE,
  normalizeLocale,
  type Locale,
} from './config'

const ATTRS = ['title', 'placeholder', 'aria-label', 'alt'] as const

let translationMap: Record<string, string> = {}
let currentLocale: Locale = DEFAULT_LOCALE
let reloadCallbacks: Array<() => void> = []
let observer: MutationObserver | null = null

type MarkedText = Text & { _originalText?: string; _translated?: string }
type MarkedEl = Element & { _i18nAttrOrig?: Partial<Record<(typeof ATTRS)[number], string>> }

function readStoredLocale(): Locale {
  if (typeof document === 'undefined') return DEFAULT_LOCALE
  const fromStore = localStorage.getItem('nox9go-locale') || localStorage.getItem('locale')
  if (fromStore) return normalizeLocale(fromStore)
  const cookie = document.cookie
    .split(';')
    .find((c) => c.trim().startsWith(`${LOCALE_COOKIE}=`))
  const value = cookie ? decodeURIComponent(cookie.split('=')[1] ?? '') : DEFAULT_LOCALE
  return normalizeLocale(value)
}

function persistLocale(locale: Locale) {
  localStorage.setItem('nox9go-locale', locale)
  localStorage.setItem('locale', locale)
  document.cookie = `${LOCALE_COOKIE}=${encodeURIComponent(locale)};path=/;max-age=${60 * 60 * 24 * 365};SameSite=Lax`
}

async function loadTranslations(locale: Locale) {
  if (locale === 'en') {
    translationMap = {}
    return
  }
  try {
    const response = await fetch(`/i18n/literals/${locale}.json`, { cache: 'no-store' })
    if (!response.ok) {
      translationMap = {}
      return
    }
    translationMap = (await response.json()) as Record<string, string>
  } catch {
    translationMap = {}
  }
}

export function translate(text: string): string {
  if (!text || typeof text !== 'string') return text
  const trimmed = text.trim()
  if (!trimmed) return text
  if (currentLocale === 'en') return text
  return translationMap[trimmed] || text
}

export function getCurrentLocale(): Locale {
  return currentLocale
}

export function onLocaleChange(callback: () => void): () => void {
  reloadCallbacks.push(callback)
  return () => {
    reloadCallbacks = reloadCallbacks.filter((cb) => cb !== callback)
  }
}

function skippedByAncestor(el: Element | null): boolean {
  let cur: Element | null = el
  while (cur) {
    if (cur.hasAttribute?.('data-i18n-skip')) return true
    cur = cur.parentElement
  }
  return false
}

function processTextNode(node: MarkedText) {
  const current = node.nodeValue
  if (!current || !current.trim()) return

  const parent = node.parentElement
  if (!parent || skippedByAncestor(parent)) return

  const tagName = parent.tagName?.toLowerCase()
  const skipTags = [
    'script',
    'style',
    'code',
    'pre',
    'colgroup',
    'table',
    'thead',
    'tbody',
    'tfoot',
    'tr',
    'select',
    'datalist',
    'optgroup',
  ]
  if (skipTags.includes(tagName)) return

  const isOurTranslation = node._translated != null && current === node._translated
  const isSameAsOriginal = current === node._originalText
  if (!isOurTranslation && !isSameAsOriginal) {
    node._originalText = current
  }
  if (node._originalText == null) node._originalText = current

  const translated = translate(node._originalText)
  node._translated = translated
  if (translated !== node.nodeValue) {
    node.nodeValue = translated
  }
}

function processElementAttrs(el: MarkedEl) {
  if (skippedByAncestor(el)) return
  if (!el._i18nAttrOrig) el._i18nAttrOrig = {}

  for (const attr of ATTRS) {
    if (!el.hasAttribute(attr)) continue
    const current = el.getAttribute(attr) || ''
    if (!current.trim()) continue

    const stored = el._i18nAttrOrig[attr]
    const translatedOfStored = stored != null ? translate(stored) : null
    // Fresh source text from Svelte (neither original nor our last translation).
    if (stored == null || (current !== stored && current !== translatedOfStored)) {
      el._i18nAttrOrig[attr] = current
    }
    const orig = el._i18nAttrOrig[attr] || current
    const next = translate(orig)
    if (next !== current) el.setAttribute(attr, next)
  }
}

function processElement(root: Element | null) {
  if (!root) return

  if (root.nodeType === Node.ELEMENT_NODE) {
    processElementAttrs(root as MarkedEl)
    const all = root.querySelectorAll('*')
    all.forEach((el) => processElementAttrs(el as MarkedEl))
  }

  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  const nodes: MarkedText[] = []
  let node: Node | null
  while ((node = walker.nextNode())) {
    nodes.push(node as MarkedText)
  }
  nodes.forEach(processTextNode)
}

export async function initRuntimeI18n() {
  if (typeof window === 'undefined') return

  currentLocale = readStoredLocale()
  document.documentElement.lang = currentLocale === 'es' ? 'es-ES' : 'en'
  await loadTranslations(currentLocale)
  processElement(document.body)

  if (observer) observer.disconnect()
  observer = new MutationObserver((mutations) => {
    for (const mutation of mutations) {
      if (mutation.type === 'characterData') {
        processTextNode(mutation.target as MarkedText)
        continue
      }
      if (mutation.type === 'attributes' && mutation.target.nodeType === Node.ELEMENT_NODE) {
        processElementAttrs(mutation.target as MarkedEl)
        continue
      }
      mutation.addedNodes.forEach((n) => {
        if (n.nodeType === Node.ELEMENT_NODE) processElement(n as Element)
        else if (n.nodeType === Node.TEXT_NODE) processTextNode(n as MarkedText)
      })
    }
  })
  observer.observe(document.body, {
    childList: true,
    subtree: true,
    characterData: true,
    attributes: true,
    attributeFilter: [...ATTRS],
  })
}

export async function setLocale(next: Locale) {
  persistLocale(next)
  currentLocale = next
  document.documentElement.lang = next === 'es' ? 'es-ES' : 'en'
  await loadTranslations(currentLocale)
  reloadCallbacks.forEach((cb) => cb())
  processElement(document.body)
}

export async function reloadTranslations() {
  currentLocale = readStoredLocale()
  await loadTranslations(currentLocale)
  reloadCallbacks.forEach((cb) => cb())
  processElement(document.body)
}
