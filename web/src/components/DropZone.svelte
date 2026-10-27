<script>
  // Drop PDFs and photos anywhere on the page to upload them into the
  // current library (desktop, for accounts that may add documents).
  import { canEdit, currentLibrary, libraries } from '../lib/stores.js';
  import { uploadFiles } from '../lib/uploads.js';
  import { dragging } from '../lib/drop.js';
  import { fade } from 'svelte/transition';
  import { motion } from '../lib/motion.js';
  import Icon from './Icon.svelte';

  let depth = 0;
  const hasFiles = (e) => [...(e.dataTransfer?.types || [])].includes('Files');

  function enter(e) {
    if (!$canEdit || !hasFiles(e) || document.querySelector('[role="dialog"]')) return;
    e.preventDefault();
    depth++;
    dragging.set(true);
  }
  function over(e) {
    if ($dragging) { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; }
  }
  function leave() {
    if (!$dragging) return;
    depth = Math.max(0, depth - 1);
    if (depth === 0) dragging.set(false);
  }
  async function drop(e) {
    if (!$dragging) return;
    e.preventDefault();
    depth = 0;
    dragging.set(false);
    await uploadFiles(e.dataTransfer.files);
  }
</script>

<svelte:window ondragenter={enter} ondragover={over} ondragleave={leave} ondrop={drop} />

{#if $dragging}
  <div class="drop" transition:fade={motion({ duration: 120 })} aria-hidden="true">
    <div class="inner">
      <Icon name="upload" size={34} />
      <strong>Drop to upload</strong>
      <span>PDFs and photos{$libraries.length > 1 ? ` into “${$currentLibrary?.name}”` : ''}; each file becomes a document.</span>
    </div>
  </div>
{/if}

<style>
  .drop { position: fixed; inset: 0; z-index: 80; background: rgb(26 23 20 / 0.35); display: grid; place-items: center; pointer-events: none; padding: 24px; }
  .inner { width: min(520px, 100%); padding: 40px 28px; border: 3px dashed var(--accent); border-radius: 22px; background: var(--card);
    display: flex; flex-direction: column; align-items: center; gap: 8px; text-align: center; color: var(--ink); box-shadow: 0 20px 60px rgb(0 0 0 / 0.25); }
  .inner :global(svg) { color: var(--accent); }
  strong { font-size: 1.25rem; }
  span { color: var(--muted); }
</style>
