<script lang="ts">
  // Port of the toast stack in upstream DashboardLayout.js.
  import { notifications, type NotificationType } from '../notifications'

  function toastStyle(type: NotificationType): { wrapper: string; icon: string } {
    if (type === 'success') {
      return {
        wrapper: 'border-green-500/30 bg-green-500/10 text-green-600 dark:text-green-400',
        icon: 'check_circle',
      }
    }
    if (type === 'error') {
      return {
        wrapper: 'border-red-500/30 bg-red-500/10 text-red-600 dark:text-red-400',
        icon: 'error',
      }
    }
    if (type === 'warning') {
      return {
        wrapper: 'border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400',
        icon: 'warning',
      }
    }
    return {
      wrapper: 'border-blue-500/30 bg-blue-500/10 text-blue-600 dark:text-blue-400',
      icon: 'info',
    }
  }
</script>

<div class="fixed top-4 right-4 z-[80] flex w-[min(92vw,380px)] flex-col gap-2">
  {#each $notifications as n (n.id)}
    {@const style = toastStyle(n.type)}
    <div class="rounded-lg border px-3 py-2 shadow-lg backdrop-blur-sm {style.wrapper}">
      <div class="flex items-start gap-2">
        <span class="material-symbols-outlined text-[18px] leading-5">{style.icon}</span>
        <div class="min-w-0 flex-1">
          {#if n.title}<p class="text-xs font-semibold mb-0.5">{n.title}</p>{/if}
          <p class="text-xs whitespace-pre-wrap break-words">{n.message}</p>
        </div>
        {#if n.dismissible}
          <button
            type="button"
            onclick={() => notifications.removeNotification(n.id)}
            class="text-current/70 hover:text-current cursor-pointer"
            aria-label="Dismiss notification"
          >
            <span class="material-symbols-outlined text-[16px]">close</span>
          </button>
        {/if}
      </div>
    </div>
  {/each}
</div>
