<script lang="ts">
  import CombosView from './combos/CombosView.svelte'
  import type { Combo, ProviderConnection, ProviderNode } from '../api/client'

  interface Props {
    combos?: Combo[]
    connections?: ProviderConnection[]
    providerNodes?: ProviderNode[]
    onRefresh: () => void
    isCreatingOpen?: boolean
  }

  let {
    combos = [],
    connections = [],
    providerNodes = [],
    onRefresh,
    isCreatingOpen = $bindable(false),
  }: Props = $props()
</script>

<CombosView
  {combos}
  {connections}
  {providerNodes}
  {onRefresh}
  bind:isCreatingOpen
/>
