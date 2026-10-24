<script lang="ts">
  // Port of decolua/9router src/shared/components/Modal.js ConfirmModal
  import Button from './Button.svelte'
  import Modal from './Modal.svelte'

  let {
    isOpen = false,
    onClose,
    onConfirm,
    title = 'Confirm',
    message = '',
    confirmText = 'Confirm',
    cancelText = 'Cancel',
    variant = 'danger',
    loading = false,
  }: {
    isOpen?: boolean
    onClose?: () => void
    onConfirm?: () => void
    title?: string
    message?: string
    confirmText?: string
    cancelText?: string
    variant?: 'primary' | 'secondary' | 'outline' | 'ghost' | 'danger' | 'success'
    loading?: boolean
  } = $props()
</script>

<Modal {isOpen} {onClose} {title} size="sm">
  <p class="text-text-muted">{message}</p>
  {#snippet footer()}
    <Button variant="ghost" onclick={() => onClose?.()} disabled={loading}>
      {cancelText}
    </Button>
    <Button {variant} onclick={() => onConfirm?.()} {loading}>
      {confirmText}
    </Button>
  {/snippet}
</Modal>
