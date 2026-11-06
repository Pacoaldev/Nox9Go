// Pure helpers for the model-health loop's settings.
//
// The server clamps the interval and the threshold to its own bounds
// (internal/modelhealth/modelhealth.go). The UI mirrors those bounds here so
// the form never shows a value the server would silently rewrite on the next
// read, and so the operator sees the effective number instead of the one they
// typed and lost.
import type { ModelHealthConfig } from '../api/client'

/** Server bounds, mirrored from internal/modelhealth/modelhealth.go. */
export const MODEL_HEALTH_MIN_INTERVAL_MIN = 5
export const MODEL_HEALTH_MAX_INTERVAL_MIN = 1440
export const MODEL_HEALTH_MIN_THRESHOLD = 2
export const MODEL_HEALTH_MAX_THRESHOLD = 100

export const MODEL_HEALTH_DEFAULT_INTERVAL_MIN = 60
export const MODEL_HEALTH_DEFAULT_THRESHOLD = 3

export interface ModelHealthSettings {
  enabled: boolean
  intervalMin: number
  threshold: number
  autoEnable: boolean
}

/** Clamps one number into [min, max], falling back to fallback when unusable. */
function clamp(value: unknown, min: number, max: number, fallback: number): number {
  const n = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(n)) return fallback
  return Math.min(Math.max(Math.trunc(n), min), max)
}

/**
 * Reads the stored settings value into the form's shape.
 *
 * A missing or damaged value yields the server's defaults, which are off: a
 * row the dashboard cannot read must not look like an enabled feature.
 */
export function normalizeModelHealth(value: unknown): ModelHealthSettings {
  const raw = (value && typeof value === 'object' ? value : {}) as ModelHealthConfig
  return {
    enabled: raw.enabled === true,
    intervalMin: clamp(
      raw.intervalMin,
      MODEL_HEALTH_MIN_INTERVAL_MIN,
      MODEL_HEALTH_MAX_INTERVAL_MIN,
      MODEL_HEALTH_DEFAULT_INTERVAL_MIN
    ),
    threshold: clamp(
      raw.threshold,
      MODEL_HEALTH_MIN_THRESHOLD,
      MODEL_HEALTH_MAX_THRESHOLD,
      MODEL_HEALTH_DEFAULT_THRESHOLD
    ),
    autoEnable: raw.autoEnable === true,
  }
}

/**
 * The object the panel saves. Every field is written, so a value the operator
 * cleared falls back to the server's default instead of staying absent and
 * leaving the stored row ambiguous.
 */
export function modelHealthSetting(value: ModelHealthSettings): ModelHealthConfig {
  return {
    enabled: value.enabled,
    intervalMin: clamp(
      value.intervalMin,
      MODEL_HEALTH_MIN_INTERVAL_MIN,
      MODEL_HEALTH_MAX_INTERVAL_MIN,
      MODEL_HEALTH_DEFAULT_INTERVAL_MIN
    ),
    threshold: clamp(
      value.threshold,
      MODEL_HEALTH_MIN_THRESHOLD,
      MODEL_HEALTH_MAX_THRESHOLD,
      MODEL_HEALTH_DEFAULT_THRESHOLD
    ),
    autoEnable: value.autoEnable,
  }
}
