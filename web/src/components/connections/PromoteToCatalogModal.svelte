<script lang="ts">
  import { api, ApiError, type ProviderNode } from '../../api/client'
  import Badge from '../../lib/ui/Badge.svelte'
  import Button from '../../lib/ui/Button.svelte'
  import Input from '../../lib/ui/Input.svelte'
  import Modal from '../../lib/ui/Modal.svelte'

  interface Props {
    isOpen: boolean
    node: ProviderNode | null
    onClose: () => void
    onPromoted: () => void
  }

  interface PromotionResult {
    files: string[]
    build: string
    buildOk: boolean
    moved: number
    modelsMoved: number
    warning?: string
  }

  let { isOpen, node, onClose, onPromoted }: Props = $props()

  let formId = $state('')
  let formName = $state('')
  let formAlias = $state('')
  let formCategory = $state<'apikey' | 'oauth' | 'free' | 'freeTier' | 'webCookie'>('apikey')
  let formWebsite = $state('')
  let formColor = $state('#64748B')
  let formIcon = $state('hub')
  let formChatUrl = $state('')
  let formModelsUrl = $state('')
  let formNotice = $state('')
  let formApiKeyUrl = $state('')

  let previewing = $state(false)
  let promoting = $state(false)
  let previewFiles = $state<string[] | null>(null)
  let error = $state<string | null>(null)
  // A conflict is not a transient error: the id is taken, so retrying the same
  // form cannot succeed. It gets its own state so the dialog can disable the
  // submit and tell the user what to change instead of offering a retry.
  let conflict = $state<{ message: string; sources: string[] } | null>(null)
  let result = $state<PromotionResult | null>(null)

  function slugOf(value: string): string {
    return value
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
  }

  $effect(() => {
    if (!isOpen || !node) return
    const basename = (url: string) => url.replace(/\/+$/, '').replace(/\/chat\/completions$/, '')
    const base = basename(node.baseUrl || '')
    formName = node.name || ''
    formId = slugOf(node.prefix || node.name || '')
    formAlias = formId
    formCategory = 'apikey'
    formWebsite = ''
    formColor = '#64748B'
    formIcon = 'hub'
    formChatUrl = base ? base + '/chat/completions' : ''
    formModelsUrl = base ? base + '/models' : ''
    formNotice = ''
    formApiKeyUrl = ''
    previewFiles = null
    result = null
    error = null
    conflict = null
    promoting = false
    previewing = false
  })

  // Editing the id is the fix for a conflict, so the warning has to clear as
  // soon as the field changes rather than leaving a stale block on screen.
  $effect(() => {
    formId
    conflict = null
  })

  let payload = $derived({
    id: formId.trim(),
    nodeId: node ? node.id : '',
    name: formName.trim(),
    alias: formAlias.trim() || formId.trim(),
    color: formColor,
    icon: formIcon,
    category: formCategory,
    website: formWebsite.trim(),
    noticeText: formNotice.trim(),
    apiKeyUrl: formApiKeyUrl.trim(),
    serviceKinds: ['llm'],
    chatUrl: formChatUrl.trim(),
    modelsUrl: formModelsUrl.trim(),
    models: [],
  })

  let canSubmit = $derived(
    !promoting &&
      conflict === null &&
      node !== null &&
      payload.id !== '' &&
      payload.name !== '' &&
      payload.chatUrl.startsWith('https://')
  )

  /**
   * Splits a failed promotion into "the id is taken" and everything else.
   *
   * The backend answers 409 with the list of places the id already lives, which
   * is the actionable part: the fix is a different id, not a retry. Any other
   * failure is left in the plain error slot.
   */
  function noteConflict(err: unknown) {
    if (err instanceof ApiError && err.status === 409) {
      conflict = { message: err.message, sources: [] }
      error = null
      return
    }
    conflict = null
    error = err instanceof Error ? err.message : String(err)
  }

  async function handlePreview() {
    previewing = true
    error = null
    try {
      const res = await api.promoteProviderNode(Object.assign({}, payload, { dryRun: true }))
      previewFiles = res.files || []
    } catch (err) {
      noteConflict(err)
      previewFiles = null
    } finally {
      previewing = false
    }
  }

  async function handlePromote() {
    if (!canSubmit) return
    promoting = true
    error = null
    try {
      const res = await api.promoteProviderNode(Object.assign({}, payload, { rebuild: true }))
      result = {
        files: res.files || [],
        build: res.build || '',
        buildOk: res.buildOk !== false,
        moved: res.moved || 0,
        modelsMoved: res.modelsMoved || 0,
        warning: res.warning,
      }
      if (res.buildOk !== false) onPromoted()
    } catch (err) {
      noteConflict(err)
    } finally {
      promoting = false
    }
  }
</script>

<Modal {isOpen} title="Mover a Catálogo" {onClose}>
  <div class="flex flex-col gap-4">
    {#if node}
      <p class="text-xs text-text-muted">
        Converts <span class="font-mono text-text-main">{node.id}</span> into a built-in catalog
        provider. Writes the six source destinations, rebuilds both binaries, then moves its
        connections and models onto the new id and removes the custom node.
      </p>

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Input label="Provider ID" bind:value={formId} placeholder="moonshot" inputClass="font-mono" hint="Lowercase letters, digits and dashes. Used as the model prefix." />
        <Input label="Name" bind:value={formName} placeholder="Moonshot AI" />
        <Input label="Alias" bind:value={formAlias} placeholder="moonshot" inputClass="font-mono" />
        <div>
          <label for="promote-category" class="text-sm font-medium text-text-main mb-1.5 block">Category</label>
          <select id="promote-category" bind:value={formCategory} class="w-full py-2.5 px-3 text-sm text-text-main bg-surface-2 rounded-[10px] border border-transparent focus:outline-none focus:ring-2 focus:ring-brand-500/30">
            <option value="apikey">API Key</option>
            <option value="oauth">OAuth</option>
            <option value="free">Free</option>
            <option value="freeTier">Free Tier</option>
            <option value="webCookie">Web Cookie</option>
          </select>
        </div>
      </div>

      <Input label="Chat URL" bind:value={formChatUrl} placeholder="https://api.moonshot.cn/v1/chat/completions" inputClass="font-mono" required hint="Goes to KnownProviders. Must be https and answer 401 to an invalid key." />
      <Input label="Models URL" bind:value={formModelsUrl} placeholder="https://api.moonshot.cn/v1/models" inputClass="font-mono" hint="Goes to modelsListURL and enables Import from /models. Leave empty when the provider has no OpenAI-shaped catalogue." />

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Input label="Website" bind:value={formWebsite} placeholder="https://platform.moonshot.ai" inputClass="font-mono" />
        <Input label="API key URL" bind:value={formApiKeyUrl} placeholder="https://platform.moonshot.ai/console/api-keys" inputClass="font-mono" />
        <Input label="Color" bind:value={formColor} placeholder="#0F172A" inputClass="font-mono" />
        <Input label="Icon" bind:value={formIcon} placeholder="hub" inputClass="font-mono" hint="Material symbol name." />
      </div>

      <Input label="Notice" bind:value={formNotice} placeholder="Moonshot's open platform (Kimi models)." hint="Optional. Shown on the catalog card." />

      <div class="flex flex-wrap items-center gap-2">
        <Button onclick={handlePreview} disabled={previewing || promoting} variant="secondary">
          {previewing ? 'Checking...' : 'Preview changes'}
        </Button>
        {#if previewFiles}
          <span class="text-xs text-text-muted">
            {previewFiles.length === 0
              ? 'Nothing to write - the catalog already has these entries.'
              : previewFiles.length + ' file(s) would change'}
          </span>
        {/if}
      </div>

      {#if previewFiles && previewFiles.length > 0}
        <div class="rounded-lg border border-border bg-surface-2 p-2">
          {#each previewFiles as file (file)}
            <code class="block truncate font-mono text-[11px] text-text-muted">{file}</code>
          {/each}
        </div>
      {/if}

      {#if conflict}
        <div class="flex flex-col gap-1 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3">
          <span class="text-sm font-medium text-amber-600">Ese ID ya está en el catálogo</span>
          <span class="text-xs text-text-muted">{conflict.message}</span>
          <span class="text-xs text-text-muted">
            Cambia el Provider ID para continuar. No se ha escrito ningún fichero ni se ha
            tocado el nodo.
          </span>
        </div>
      {/if}

      {#if error}
        <span class="text-sm text-red-500">{error}</span>
      {/if}

      {#if result}
        <div class="flex flex-col gap-1 rounded-lg border border-border bg-surface-2 p-3">
          <div class="flex items-center gap-2">
            <Badge tone={result.buildOk ? 'success' : 'error'}>{result.buildOk ? 'Build OK' : 'Build failed'}</Badge>
            <span class="text-xs text-text-muted">{result.build}</span>
          </div>
          <span class="text-xs text-text-muted">
            {result.files.length} file(s) written - {result.moved} connection(s) and
            {result.modelsMoved} model(s) moved to <span class="font-mono">{formId}</span>
          </span>
          {#if result.warning}
            <span class="text-xs text-amber-500">{result.warning}</span>
          {/if}
          <span class="text-xs text-text-muted">Restart the binary to serve the new catalog.</span>
        </div>
      {/if}

      <div class="flex justify-end gap-2">
        <Button variant="secondary" onclick={onClose} disabled={promoting}>Close</Button>
        <Button onclick={handlePromote} disabled={!canSubmit}>
          {promoting ? 'Promoting...' : 'Mover a Catálogo'}
        </Button>
      </div>
    {:else}
      <p class="text-sm text-text-muted">Select a custom provider first.</p>
    {/if}
  </div>
</Modal>
