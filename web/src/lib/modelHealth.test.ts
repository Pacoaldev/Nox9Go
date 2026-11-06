import { describe, expect, it } from 'bun:test'
import {
  MODEL_HEALTH_DEFAULT_INTERVAL_MIN,
  MODEL_HEALTH_DEFAULT_THRESHOLD,
  MODEL_HEALTH_MAX_INTERVAL_MIN,
  MODEL_HEALTH_MAX_THRESHOLD,
  MODEL_HEALTH_MIN_INTERVAL_MIN,
  MODEL_HEALTH_MIN_THRESHOLD,
  modelHealthSetting,
  normalizeModelHealth,
} from './modelHealth'

describe('normalizeModelHealth', () => {
  it('reads the stored values', () => {
    expect(
      normalizeModelHealth({
        enabled: true,
        intervalMin: 15,
        threshold: 5,
        autoEnable: true,
      })
    ).toEqual({ enabled: true, intervalMin: 15, threshold: 5, autoEnable: true })
  })

  it('treats a missing value as the server defaults, which are off', () => {
    expect(normalizeModelHealth(undefined)).toEqual({
      enabled: false,
      intervalMin: MODEL_HEALTH_DEFAULT_INTERVAL_MIN,
      threshold: MODEL_HEALTH_DEFAULT_THRESHOLD,
      autoEnable: false,
    })
  })

  it('treats a damaged value as the server defaults', () => {
    expect(normalizeModelHealth('nonsense')).toEqual({
      enabled: false,
      intervalMin: MODEL_HEALTH_DEFAULT_INTERVAL_MIN,
      threshold: MODEL_HEALTH_DEFAULT_THRESHOLD,
      autoEnable: false,
    })
  })

  it('clamps the interval to the server bounds', () => {
    expect(normalizeModelHealth({ intervalMin: 1 }).intervalMin).toBe(
      MODEL_HEALTH_MIN_INTERVAL_MIN
    )
    expect(normalizeModelHealth({ intervalMin: 99999 }).intervalMin).toBe(
      MODEL_HEALTH_MAX_INTERVAL_MIN
    )
  })

  it('clamps the threshold to the server bounds', () => {
    expect(normalizeModelHealth({ threshold: 0 }).threshold).toBe(MODEL_HEALTH_MIN_THRESHOLD)
    expect(normalizeModelHealth({ threshold: 1000 }).threshold).toBe(
      MODEL_HEALTH_MAX_THRESHOLD
    )
  })

  it('reads a non-boolean enabled as off', () => {
    expect(normalizeModelHealth({ enabled: 'yes' }).enabled).toBe(false)
  })
})

describe('modelHealthSetting', () => {
  it('writes every field so the stored row is never ambiguous', () => {
    expect(
      modelHealthSetting({ enabled: true, intervalMin: 30, threshold: 4, autoEnable: true })
    ).toEqual({ enabled: true, intervalMin: 30, threshold: 4, autoEnable: true })
  })

  it('clamps an out-of-range interval before writing it', () => {
    expect(
      modelHealthSetting({
        enabled: false,
        intervalMin: 0,
        threshold: 3,
        autoEnable: false,
      }).intervalMin
    ).toBe(MODEL_HEALTH_MIN_INTERVAL_MIN)
  })

  it('clamps an out-of-range threshold before writing it', () => {
    expect(
      modelHealthSetting({
        enabled: false,
        intervalMin: 60,
        threshold: 999,
        autoEnable: false,
      }).threshold
    ).toBe(MODEL_HEALTH_MAX_THRESHOLD)
  })
})
