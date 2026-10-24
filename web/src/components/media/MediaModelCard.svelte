<script lang="ts">
  import { Check, Copy, Play } from 'lucide-svelte'
  import Badge from '../../lib/ui/Badge.svelte'
  import Card from '../../lib/ui/Card.svelte'
  import type { MediaKind } from './mediaTypes'

  interface Props {
    model: { id: string; name?: string }
    kind: MediaKind
    isCopied: boolean
    isTesting: boolean
    testResult?: { ok: boolean; error?: string; latency?: number }
    onCopy: () => void
    onTest: () => void
  }

  let { model, kind, isCopied, isTesting, testResult, onCopy, onTest }: Props = $props()
</script>

<Card class="p-3.5 flex flex-col justify-between gap-2.5 hover:border-brand-500/40 transition-colors">
  <div class="flex items-start justify-between gap-2">
    <div class="min-w-0">
      <p class="font-semibold text-sm text-text-main truncate" title={model.name || model.id}>
        {model.name || model.id}
      </p>
      <p class="font-mono text-xs text-text-muted truncate mt-0.5" title={model.id}>
        {model.id}
      </p>
    </div>
    <Badge variant="default" size="sm" class="shrink-0">{kind}</Badge>
  </div>

  <div class="flex items-center justify-between pt-2 border-t border-border-subtle text-xs">
    <div class="flex items-center gap-1.5 min-w-0">
      {#if testResult}
        <Badge variant={testResult.ok ? 'success' : 'error'} size="sm" dot>
          {testResult.ok ? `${testResult.latency}ms` : 'Failed'}
        </Badge>
      {/if}
    </div>

    <div class="flex items-center gap-1.5 shrink-0">
      <button
        type="button"
        title="Test model endpoint"
        disabled={isTesting}
        onclick={onTest}
        class="p-1.5 rounded-md border border-border hover:bg-surface-2 text-text-muted hover:text-text-main transition-colors cursor-pointer disabled:opacity-50"
      >
        <Play class="w-3.5 h-3.5 {isTesting ? 'animate-spin' : ''}" />
      </button>
      <button
        type="button"
        title="Copy full model identifier"
        onclick={onCopy}
        class="p-1.5 rounded-md border border-border hover:bg-surface-2 text-text-muted hover:text-text-main transition-colors cursor-pointer"
      >
        {#if isCopied}
          <Check class="w-3.5 h-3.5 text-success" />
        {:else}
          <Copy class="w-3.5 h-3.5" />
        {/if}
      </button>
    </div>
  </div>
</Card>
