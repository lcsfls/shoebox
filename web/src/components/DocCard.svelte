<script>
  import Thumb from './Thumb.svelte';
  import { formatDate, formatAmount, snippetParts, fileVersion } from '../lib/format.js';
  let { doc, href } = $props();
</script>

<a class="doccard card" {href}>
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
  .body { display: flex; flex-direction: column; gap: 2px; padding: 0 4px 4px; min-width: 0; }
  .title { font-weight: 600; font-size: 0.95rem; line-height: 1.3; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
  .meta { color: var(--muted); font-size: 0.82rem; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .snip { font-size: 0.8rem; color: var(--ink-2); line-height: 1.35; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; margin-top: 2px; }
</style>
