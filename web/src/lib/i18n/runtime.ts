/**
 * Runtime DOM translator — port of decolua/9router src/i18n/runtime.js.
 * English source strings stay in the Svelte markup; a locale map rewrites
 * text nodes. Only `en` and `es` are supported in NOX9GO.
 */
import {
  DEFAULT_LOCALE,
  LOCALE_COOKIE,
  normalizeLocale,
  type Locale,
} from './config'

let translationMap: Record<string, string> = {}
let currentLocale: Locale = DEFAULT_LOCALE
let reloadCallbacks: Array<() => void> = []
let observer: MutationObserver | null = null

type MarkedText = Text & { _originalText?: string; _translated?: string }

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

function processTextNode(node: MarkedText) {
  const current = node.nodeValue
  if (!current || !current.trim()) return

  const parent = node.parentElement
  if (!parent) return

  let element: HTMLElement | null = parent
  while (element) {
    if (element.hasAttribute?.('data-i18n-skip')) return
    element = element.parentElement
  }

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

function processElement(root: Element | null) {
  if (!root) return
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
  await loadTranslations(currentLocale)
  processElement(document.body)

  if (observer) observer.disconnect()
  observer = new MutationObserver((mutations) => {
    for (const mutation of mutations) {
      if (mutation.type === 'characterData') {
        processTextNode(mutation.target as MarkedText)
        continue
      }
      mutation.addedNodes.forEach((n) => {
        if (n.nodeType === Node.ELEMENT_NODE) processElement(n as Element)
        else if (n.nodeType === Node.TEXT_NODE) processTextNode(n as MarkedText)
      })
    }
  })
  observer.observe(document.body, { childList: true, subtree: true, characterData: true })
}

export async function setLocale(next: Locale) {
  persistLocale(next)
  currentLocale = next
  await loadTranslations(currentLocale)
  reloadCallbacks.forEach((cb) => cb())
  processElement(document.body)
  document.documentElement.lang = next === 'es' ? 'es-ES' : 'en'
}

export async function reloadTranslations() {
  currentLocale = readStoredLocale()
  await loadTranslations(currentLocale)
  reloadCallbacks.forEach((cb) => cb())
  processElement(document.body)
}
