<script>
  // "Move to …": puts a document into another library, without dragging
  // (on a phone there is nothing to drag).
  import { api } from '../lib/api.js';
  import { libraries, toast } from '../lib/stores.js';
  import Icon from './Icon.svelte';

  let { doc, before = null, onmoved = () => {} } = $props();
  let open = $state(false);
  let busy = $state(false);
  let targets = $derived($libraries.filter((l) => l.id !== doc.library_id && l.role === 'editor'));

  async function move(lib) {
    busy = true;
    try {
      if (before) await before();
      await api.post(`/api/docs/${doc.id}/move`, { library: lib.id });
      toast(`Moved to “${lib.name}”`);
      open = false;
      dispatchEvent(new CustomEvent('sb-doc-moved', { detail: doc.id }));
      onmoved(lib);
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      busy = false;
    }
  }
</script>

{#if targets.length}
  <div class="moveto">
    <button type="button" class="btn small" onclick={() => (open = !open)} aria-expanded={open}><Icon name="folder" />Move to …</button>
    {#if open}
      <div class="targets" role="group" aria-label="Move to another library">
        {#each targets as l (l.id)}
          <button type="button" class="btn small" disabled={busy} onclick={() => move(l)} aria-label="Move to {l.name}">{l.name}</button>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  .moveto { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
  .targets { display: flex; flex-wrap: wrap; gap: 6px; }
  .targets .btn { border-style: dashed; }
</style>
