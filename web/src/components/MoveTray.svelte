<script>
  // While a document card is dragged: the other libraries to drop it into.
  import { fly } from 'svelte/transition';
  import { api } from '../lib/api.js';
  import { libraries, library, toast } from '../lib/stores.js';
  import { motion } from '../lib/motion.js';
  import Icon from './Icon.svelte';

  const TYPE = 'application/x-shoebox-doc';
  let dragging = $state(false);
  let over = $state(0);
  let targets = $derived($libraries.filter((l) => l.id !== $library && l.role === 'editor'));

  function start(e) { if ([...(e.dataTransfer?.types || [])].includes(TYPE) && targets.length) dragging = true; }
  function end() { dragging = false; over = 0; }
  async function drop(e, lib) {
    e.preventDefault();
    const id = Number(e.dataTransfer.getData(TYPE));
    end();
    if (!id) return;
    try {
      await api.post(`/api/docs/${id}/move`, { library: lib.id });
      toast(`Moved to “${lib.name}”`);
      dispatchEvent(new CustomEvent('sb-doc-moved', { detail: id }));
    } catch (err) { toast(err.message, 'error'); }
  }
</script>

<svelte:window ondragstart={start} ondragend={end} />

{#if dragging}
  <div class="tray card" transition:fly={motion({ y: 16, duration: 160 })}>
    <span class="muted small">Move to</span>
    {#each targets as l (l.id)}
      <div class="target" class:over={over === l.id} role="button" tabindex="-1" aria-label="Move to {l.name}"
        ondragenter={(e) => { e.preventDefault(); over = l.id; }} ondragover={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'move'; }}
        ondragleave={() => over === l.id && (over = 0)} ondrop={(e) => drop(e, l)}>
        <Icon name="folder" />{l.name}
      </div>
    {/each}
  </div>
{/if}

<style>
  .tray { position: fixed; z-index: 40; left: 0; right: 0; margin: 0 auto; width: fit-content; bottom: 24px; display: flex; align-items: center; gap: 8px;
    padding: 8px 10px; box-shadow: var(--shadow); max-width: calc(100vw - 32px); flex-wrap: wrap; }
  .target { display: inline-flex; align-items: center; gap: 8px; min-height: 48px; padding: 0 18px; border-radius: 10px;
    border: 1.5px dashed var(--line-strong); font-weight: 600; transition: background 0.12s, border-color 0.12s; }
  .target.over { background: var(--chip); color: var(--chip-ink); border-color: var(--accent); border-style: solid; }
</style>
