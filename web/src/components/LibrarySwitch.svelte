<script>
  // Which library the app shows: only there when the account has more than one.
  import { libraries, library } from '../lib/stores.js';
  import Icon from './Icon.svelte';
  let { compact = false } = $props();
</script>

{#if $libraries.length > 1}
  <label class="libswitch" class:compact>
    <Icon name="folder" size={16} />
    <span class="sr-only">Library</span>
    <select bind:value={$library} aria-label="Library">
      {#each $libraries as l (l.id)}
        <option value={l.id}>{l.name}{l.role === 'reader' ? ' (read only)' : ''}</option>
      {/each}
    </select>
  </label>
{/if}

<style>
  .libswitch { display: flex; align-items: center; gap: 6px; padding: 0 10px; min-height: 40px; border: 1px solid var(--line); border-radius: 10px;
    background: var(--card); color: var(--ink-2); }
  .libswitch select { flex: 1; min-width: 0; border: 0; background: transparent; color: var(--ink); font: inherit; font-weight: 600; min-height: 38px; cursor: pointer; outline: none; }
  .libswitch:focus-within { outline: 2px solid var(--accent); outline-offset: 1px; }
  .libswitch.compact { min-height: 36px; }
  .libswitch.compact select { min-height: 34px; }
</style>
