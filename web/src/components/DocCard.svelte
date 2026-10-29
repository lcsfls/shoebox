<script>
  import Thumb from './Thumb.svelte';
  import { formatDate, formatAmount, snippetParts, fileVersion } from '../lib/format.js';
  // selecting: a click (or Space) ticks the card instead of opening it
  // onopen: opens it somewhere else (the archive's side pane); true when it did
  // dragIds: what a drag carries (all selected ones when this card is among them)
  let { doc, href, selecting = false, selected = false, onselect = null, current = false, onopen = null, dragIds = null } = $props();
  function click(e) {
    if (selecting && onselect) {
      e.preventDefault();
      onselect(e);
    } else if (onopen && e.button === 0 && !e.ctrlKey && !e.metaKey && !e.shiftKey && !e.altKey && onopen()) {
      e.preventDefault();
    }
  }
</script>

<a class="doccard card" class:selected class:current {href} onclick={click} aria-current={current ? 'true' : undefined}
  role={selecting ? 'checkbox' : undefined} aria-checked={selecting ? selected : undefined}
  onkeydown={(e) => { if (selecting && e.key === ' ') click(e); }}
  ondragstart={(e) => { e.dataTransfer.setData('application/x-shoebox-doc', (dragIds || [doc.id]).join(',')); e.dataTransfer.effectAllowed = 'move'; }}>
  {#if selecting}<span class="tick" aria-hidden="true">{selected ? '✓' : ''}</span>{/if}
  <Thumb id={doc.id} updated={fileVersion(doc)} />
  <span class="body">
    <span class="title">{doc.title || 'Untitled'}</span>
    <span class="meta">{doc.sender || 'Unknown sender'}{#if doc.date} · {formatDate(doc.date)}{/if}</span>
    {#if doc.snippet}
      <span class="snip">{#each snippetParts(doc.snippet) as p}{#if p.hit}<mark>{p.text}</mark>{:else}{p.text}{/if}{/each}</span>
    {:else if doc.amount_cents != null}
      <span class="meta">{formatAmount(doc.amount_cents)}</span>
    {/if}
  </span>
</a>

<style>
  .doccard { display: flex; flex-direction: column; text-decoration: none; color: var(--ink); overflow: hidden; padding: 8px; gap: 8px;
    transition: border-color 0.15s, transform 0.15s, box-shadow 0.15s; min-width: 0; }
  .doccard:hover { border-color: var(--line-strong); box-shadow: var(--shadow); }
  .doccard:active { transform: scale(0.99); }
  .doccard { position: relative; }
  .doccard.selected { border-color: var(--accent); box-shadow: 0 0 0 2px var(--accent); }
  .doccard.current { border-color: var(--accent); }
  .tick { position: absolute; top: 12px; left: 12px; z-index: 1; width: 24px; height: 24px; border-radius: 50%; border: 2px solid #fff;
    background: rgb(0 0 0 / 0.35); color: #fff; display: grid; place-items: center; font-size: 0.8rem; font-weight: 700; box-shadow: 0 1px 3px rgb(0 0 0 / 0.3); }
  .selected .tick { background: var(--accent); }
  .body { display: flex; flex-direction: column; gap: 2px; padding: 0 4px 4px; min-width: 0; }
  .title { font-weight: 600; font-size: 0.95rem; line-height: 1.3; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
  .meta { color: var(--muted); font-size: 0.82rem; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .snip { font-size: 0.8rem; color: var(--ink-2); line-height: 1.35; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; margin-top: 2px; }
</style>
