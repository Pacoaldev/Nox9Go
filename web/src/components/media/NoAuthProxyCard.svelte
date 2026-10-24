<script lang="ts">
  import { onMount } from 'svelte'
  import { api } from '../../api/client'
  import Badge from '../../lib/ui/Badge.svelte'
  import Card from '../../lib/ui/Card.svelte'

  interface Props {
    providerId: string
  }

  let { providerId }: Props = $props()

  const NONE_PROXY_POOL_VALUE = '__none__'
  const STRATEGIES = [
    { value: 'none', label: 'None (single pool)' },
    { value: 'round-robin', label: 'Round-robin' },
    { value: 'random', label: 'Random' },
  ]

  let proxyPools = $state<Array<{ id: string; name: string }>>([])
  let proxyPoolId = $state(NONE_PROXY_POOL_VALUE)
  let rotateStrategy = $state('none')
  let saving = $state(false)
  let savedFlash = $state(false)

  onMount(() => {
    let cancelled = false
    Promise.all([
      api.getProxyPools ? api.getProxyPools() : fetch('/api/proxy-pools?isActive=true').then((r) => r.ok ? r.json() : { proxyPools: [] }),
      api.getSettings(),
    ]).then(([poolData, settingsData]) => {
      if (cancelled) return
      proxyPools = (poolData as any)?.proxyPools || poolData || []
      const override = (settingsData?.providerStrategies || {})[providerId] || {}
      proxyPoolId = override.proxyPoolId || NONE_PROXY_POOL_VALUE
      rotateStrategy = override.rotateStrategy || 'none'
    }).catch(() => {})

    return () => { cancelled = true }
  })

  async function save(poolId: string, strategy: string) {
    saving = true
    try {
      const res = await fetch('/api/settings', { cache: 'no-store' })
      const data = res.ok ? await res.json() : {}
      const current = data.providerStrategies || {}
      const override = { ...(current[providerId] || {}) }
      if (poolId === NONE_PROXY_POOL_VALUE) delete override.proxyPoolId
      else override.proxyPoolId = poolId
      if (strategy === 'none') delete override.rotateStrategy
      else override.rotateStrategy = strategy
      const updated = { ...current }
      if (Object.keys(override).length === 0) delete updated[providerId]
      else updated[providerId] = override
      await fetch('/api/settings', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ providerStrategies: updated }),
      })
      savedFlash = true
      setTimeout(() => { savedFlash = false }, 1500)
    } catch (e) {
      console.error('Save proxy config error:', e)
    } finally {
      saving = false
    }
  }

  function handlePoolChange(newPoolId: string) {
    proxyPoolId = newPoolId
    save(newPoolId, rotateStrategy)
  }

  function handleStrategyChange(newStrategy: string) {
    rotateStrategy = newStrategy
    save(proxyPoolId, newStrategy)
  }

  let canRotate = $derived(proxyPools.length >= 2)
  let isRotation = $derived(rotateStrategy !== 'none')
</script>

<Card>
  <div class="flex items-center gap-3 mb-4">
    <div class="inline-flex items-center justify-center w-10 h-10 rounded-full bg-green-500/10 text-green-500">
      <span class="material-symbols-outlined text-[20px]">lock_open</span>
    </div>
    <div class="flex-1">
      <p class="text-sm font-medium text-text-main">No authentication required</p>
      <p class="text-xs text-text-muted">This provider is ready to use. Optionally route requests through a proxy pool to bypass IP-based limits.</p>
    </div>
    {#if savedFlash}
      <Badge variant="success" size="sm">Saved</Badge>
    {/if}
  </div>

  <div class="flex flex-col gap-1.5 mb-4">
    <label class="text-sm font-medium text-text-main">Proxy Pool</label>
    <select
      value={proxyPoolId}
      onchange={(e) => handlePoolChange(e.currentTarget.value)}
      disabled={saving || isRotation}
      class="py-2 px-3 text-sm text-text-main bg-white dark:bg-white/5 border border-black/10 dark:border-white/10 rounded-md focus:ring-1 focus:ring-primary/30 focus:border-primary/50 focus:outline-none transition-all disabled:opacity-50"
    >
      <option value={NONE_PROXY_POOL_VALUE}>None (direct)</option>
      {#each proxyPools as pool}
        <option value={pool.id}>{pool.name}</option>
      {/each}
    </select>
    {#if isRotation}
      <p class="text-xs text-text-muted">Pool selector is ignored when rotation is active — all active pools are used.</p>
    {/if}
  </div>

  <div class="flex flex-col gap-2">
    <label class="text-sm font-medium text-text-main">Rotation Strategy</label>
    <select
      value={rotateStrategy}
      onchange={(e) => handleStrategyChange(e.currentTarget.value)}
      disabled={saving}
      class="py-2 px-3 text-sm text-text-main bg-white dark:bg-white/5 border border-black/10 dark:border-white/10 rounded-md focus:ring-1 focus:ring-primary/30 focus:border-primary/50 focus:outline-none transition-all disabled:opacity-50"
    >
      {#each STRATEGIES as s}
        <option value={s.value} disabled={s.value !== 'none' && !canRotate}>
          {s.label}
        </option>
      {/each}
    </select>
    <p class="text-xs text-text-muted">
      {#if !canRotate}
        Need at least 2 active proxy pools for rotation.
      {:else if isRotation}
        {#if rotateStrategy === 'round-robin'}
          Rotating through all {proxyPools.length} active pools in order. State is in-memory (resets on restart).
        {:else}
          Picking a random pool from {proxyPools.length} active pools each request.
        {/if}
      {:else}
        Uses the selected pool above. Set to Round-robin or Random to rotate across all active pools.
      {/if}
    </p>
  </div>
</Card>
