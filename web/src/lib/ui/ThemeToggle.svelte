<script lang="ts">
  // Port of decolua/9router src/shared/components/ThemeToggle.js
  let theme = $state<'light' | 'dark'>('dark')

  $effect(() => {
    const stored = localStorage.getItem('nox9go-theme')
    if (stored === 'light' || stored === 'dark') theme = stored
    apply(theme)
  })

  function apply(t: 'light' | 'dark') {
    document.documentElement.classList.toggle('dark', t === 'dark')
    document.documentElement.classList.toggle('light', t === 'light')
    localStorage.setItem('nox9go-theme', t)
  }

  function toggle() {
    theme = theme === 'dark' ? 'light' : 'dark'
    apply(theme)
  }
</script>

<button
  type="button"
  onclick={toggle}
  class="w-8 h-8 rounded-[8px] flex items-center justify-center text-text-muted hover:text-text-main hover:bg-surface-2 transition-colors cursor-pointer"
  aria-label="Toggle theme"
>
  {#if theme === 'dark'}
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4">
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41" />
    </svg>
  {:else}
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4">
      <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
    </svg>
  {/if}
</button>