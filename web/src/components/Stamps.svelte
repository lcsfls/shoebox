<script>
  // The two stamps ("Eingang", "Bezahlt") and the ones a document already has.
  import { api } from '../lib/api.js';
  import { toast } from '../lib/stores.js';
  import { formatDate } from '../lib/format.js';
  import Icon from './Icon.svelte';
  import StampDialog from './StampDialog.svelte';

  let { doc, stamps = [], labels = { in: 'Eingang', paid: 'Bezahlt' }, editable = true, onchange } = $props();
  let open = $state(''); // kind of the stamp being placed

  async function removeAll() {
    if (!confirm('Take the stamps off? The document goes back to how it was before the first stamp (page changes made since then are undone too).')) return;
    try {
      const r = await api.del(`/api/docs/${doc.id}/stamps`);
      toast('Stamps removed');
      onchange?.(r);
    } catch (e) { toast(e.message, 'error'); }
  }
  const has = (kind) => stamps.some((s) => s.kind === kind);
</script>

<div class="stamps">
  {#if editable}
    <div class="row">
      <span class="muted small">Stamp</span>
      <button type="button" class="btn small st in" onclick={() => (open = 'in')}><Icon name="check" size={16} />{labels.in}{has('in') ? ' again' : ''}</button>
      <button type="button" class="btn small st paid" onclick={() => (open = 'paid')}><Icon name="check" size={16} />{labels.paid}{has('paid') ? ' again' : ''}</button>
    </div>
  {/if}
  {#if stamps.length}
    <ul>
      {#each stamps as s (s.id)}
        <li class:paid={s.kind === 'paid'}>
          <strong>{s.label}</strong> {formatDate(s.date)}{s.note ? ` · ${s.note}` : ''}{s.by ? ` · ${s.by}` : ''}
          <span class="mono muted hash" title="SHA-256 of the document before this stamp: {s.hash}">{s.hash.slice(0, 16)}…</span>
        </li>
      {/each}
    </ul>
    {#if editable}<button type="button" class="btn small ghost" onclick={removeAll}>Take the stamps off</button>{/if}
  {/if}
</div>

{#if open}
  <StampDialog {doc} kind={open} label={labels[open]} onclose={() => (open = '')} onstamped={(r) => { open = ''; onchange?.(r); }} />
{/if}

<style>
  .stamps { display: flex; flex-direction: column; gap: 8px; }
  .row { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
  .row .muted { margin-right: 2px; }
  .st.in { color: #1F4F9E; border-color: color-mix(in srgb, #1F4F9E 40%, var(--line)); }
  .st.paid { color: #B3261E; border-color: color-mix(in srgb, #B3261E 40%, var(--line)); }
  :global(:root[data-theme='dark']) .st.in { color: #8FB3F0; }
  :global(:root[data-theme='dark']) .st.paid { color: #F0A097; }
  @media (prefers-color-scheme: dark) {
    :global(:root:not([data-theme='light'])) .st.in { color: #8FB3F0; }
    :global(:root:not([data-theme='light'])) .st.paid { color: #F0A097; }
  }
  ul { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 4px; font-size: 0.88rem; }
  li { border-left: 3px solid #1F4F9E; padding-left: 8px; }
  li.paid { border-left-color: #B3261E; }
  .hash { font-size: 0.75rem; margin-left: 4px; }
</style>
