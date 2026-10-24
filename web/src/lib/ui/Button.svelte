<script lang="ts">
  // Port of decolua/9router src/shared/components/Button.js
  import type { Snippet } from 'svelte'

  type Variant = 'primary' | 'secondary' | 'outline' | 'ghost' | 'danger' | 'success'
  type Size = 'sm' | 'md' | 'lg'

  let {
    variant = 'primary',
    size = 'md',
    type = 'button',
    icon = '',
    iconRight = '',
    disabled = false,
    loading = false,
    fullWidth = false,
    title = '',
    class: klass = '',
    onclick,
    children
  }: {
    title?: string
    variant?: Variant
    size?: Size
    type?: 'button' | 'submit' | 'reset'
    icon?: string
    iconRight?: string
    disabled?: boolean
    loading?: boolean
    fullWidth?: boolean
    class?: string
    onclick?: (e: MouseEvent) => void
    children?: Snippet
  } = $props()
  const variants: Record<Variant, string> = {
    primary:
      'bg-brand-500 hover:bg-brand-600 text-white shadow-sm disabled:bg-surface-3 disabled:text-text-muted',
    secondary:
      'bg-surface-2 hover:bg-surface-3 text-text-main border border-border disabled:opacity-50',
    outline:
      'border border-border text-text-main hover:bg-surface-2 hover:border-brand-500/40',
    ghost: 'text-text-muted hover:bg-surface-2 hover:text-text-main',
    danger: 'bg-danger hover:bg-danger/80 text-white shadow-sm disabled:bg-surface-3 disabled:text-text-muted',
    success: 'bg-success hover:bg-success/80 text-white shadow-sm disabled:bg-surface-3 disabled:text-text-muted',
  }

  const sizes: Record<Size, string> = {
    sm: 'h-7 px-3 text-xs rounded-[8px]',
    md: 'h-9 px-4 text-sm rounded-[10px]',
    lg: 'h-11 px-6 text-sm rounded-[10px]',
  }
</script>

<button
  {type}
  {title}
  class="inline-flex items-center justify-center gap-2 font-semibold transition-all duration-150 ease-out cursor-pointer active:scale-[0.97] disabled:opacity-50 disabled:cursor-not-allowed disabled:active:scale-100 {variants[variant]} {sizes[size]} {fullWidth ? 'w-full' : ''} {klass}"
  disabled={disabled || loading}
  {onclick}
>
  {#if loading}
    <span class="material-symbols-outlined animate-spin text-[18px]">progress_activity</span>
  {:else if icon}
    <span class="material-symbols-outlined text-[18px]">{icon}</span>
  {/if}
  {@render children?.()}
  {#if iconRight && !loading}
    <span class="material-symbols-outlined text-[18px]">{iconRight}</span>
  {/if}
</button>
