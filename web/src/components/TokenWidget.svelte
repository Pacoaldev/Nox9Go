<script lang="ts">
  // TokenWidget: shows used/limit tokens today
  let used = $state<number>(0)
  let limit = $state<number>(0)
  let error = $state<boolean>(false)

  $effect(() => {
    const fetchTokens = async () => {
      try {
        const res = await fetch('/api/usage/stats?period=today')
        if (!res.ok) throw new Error()
        const data = await res.json()
        // Expected shape: { tokens: number, limit: number }
        if (typeof data.tokens === 'number') used = data.tokens
        if (typeof data.limit === 'number') limit = data.limit
        error = false
      } catch {
        error = true
      }
    }
    fetchTokens()
    const id = setInterval(fetchTokens, 30000)
    return () => clearInterval(id)
  })
</script>

<div class="flex items-center gap-1">
  {#if error}
    <span class="text-xs text-red-500">⚠</span>
  {:else}
    <span class="text-xs">{used}/{limit}</span>
  {/if}
</div>
