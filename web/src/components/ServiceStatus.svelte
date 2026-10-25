<script lang="ts">
  // ServiceStatus: Gateway health & latency
  let status = $state<'loading'|'ok'|'error'>('loading')
  let latency = $state<number | null>(null)

  $effect(() => {
    const fetchStatus = async () => {
      try {
        const res = await fetch('/api/health')
        if (res.ok) {
          const data = await res.json()
          status = 'ok'
          if (typeof data.latencyMs === 'number') latency = data.latencyMs
        } else {
          status = 'error'
        }
      } catch {
        status = 'error'
      }
    }
    fetchStatus()
    const id = setInterval(fetchStatus, 10000)
    return () => clearInterval(id)
  })
</script>

<div class="flex items-center gap-1">
  {#if status === 'loading'}
    <div class="size-3 rounded-full bg-gray-400"></div>
  {:else if status === 'ok'}
    <div class="size-3 rounded-full bg-green-500"></div>
  {:else}
    <div class="size-3 rounded-full bg-red-500"></div>
  {/if}
  {#if latency !== null}
    <span class="text-xs">{latency}ms</span>
  {/if}
</div>
