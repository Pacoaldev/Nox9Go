<script lang="ts">
  // AlertsBadge: red dot if any provider alerts
  let hasAlert = $state<boolean>(false)

  $effect(() => {
    const fetchAlerts = async () => {
      try {
        const res = await fetch('/api/providers/alerts')
        if (!res.ok) throw new Error()
        const data = await res.json()
        // Expect array of alerts
        hasAlert = Array.isArray(data) && data.length > 0
      } catch {
        hasAlert = false
      }
    }
    fetchAlerts()
    const id = setInterval(fetchAlerts, 15000)
    return () => clearInterval(id)
  })
</script>

<div class="relative">
  <svg xmlns="http://www.w3.org/2000/svg" class="size-5 text-text-main" fill="none" viewBox="0 0 24 24" stroke="currentColor">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6 6 0 00-5-5.917V5a3 3 0 10-6 0v.083A6 6 0 002 11v3.159c0 .538-.214 1.055-.595 1.436L0 17h5m10 0a3 3 0 11-6 0h6z" />
  </svg>
  {#if hasAlert}
    <span class="absolute top-0 right-0 block size-2 rounded-full bg-red-500"></span>
  {/if}
</div>
