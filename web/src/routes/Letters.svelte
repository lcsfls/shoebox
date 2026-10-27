<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { toast, canEdit } from '../lib/stores.js';
  import { navigate } from '../lib/router.js';
  import { formatDate } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';

  let letters = $state(null);
  let busy = $state(false);
  onMount(load);
  async function load() {
    try { letters = await api.get('/api/letters'); } catch (e) { toast(e.message, 'error'); letters = []; }
  }
  async function create(from = 0) {
    busy = true;
    try {
      const r = await api.post('/api/letters', { from, lang: navigator.language?.startsWith('de') ? 'de' : 'en' });
      navigate(`/letters/${r.id}`);
    } catch (e) { toast(e.message, 'error'); }
    busy = false;
  }
  const firstLine = (s) => (s || '').split('\n')[0];
  let drafts = $derived((letters || []).filter((l) => l.status === 'draft'));
  let done = $derived((letters || []).filter((l) => l.status !== 'draft'));
</script>

<div class="page letters">
  <div class="page-head">
    <h1>Letters</h1>
    {#if $canEdit}<button class="btn primary" onclick={() => create()} disabled={busy}><Icon name="plus" />New letter</button>{/if}
  </div>

  {#if letters === null}
    <p class="muted">Loading …</p>
  {:else if !letters.length}
    <div class="card empty">
      <Icon name="letter" size={34} />
      <h2>No letters yet</h2>
      <p class="muted">Write a letter in the DIN 5008 layout, print it, and keep a copy in the archive. The sender comes from the letterhead in Settings.</p>
      <div class="actions">
        {#if $canEdit}<button class="btn primary" onclick={() => create()} disabled={busy}>Write the first letter</button>{/if}
        <a class="btn" href="/settings/letterhead">Set up the letterhead</a>
      </div>
    </div>
  {:else}
    {#each [['Drafts', drafts], ['Finished', done]] as [title, list] (title)}
      {#if list.length}
        <h2 class="group">{title}</h2>
        <ul class="list">
          {#each list as l (l.id)}
            <li class="card row">
              <a class="grow main" href="/letters/{l.id}">
                <strong>{l.subject || 'No subject'}</strong>
                <span class="muted small">{firstLine(l.recipient) || 'No recipient'} · {formatDate(l.date)}</span>
              </a>
              {#if l.status === 'final' && l.doc_id}<a class="btn small ghost" href="/doc/{l.doc_id}">In the archive</a>{/if}
              {#if $canEdit}<button class="btn small ghost iconbtn" title="Copy as a new letter" aria-label="Copy as a new letter" onclick={() => create(l.id)}><Icon name="copy" /></button>{/if}
            </li>
          {/each}
        </ul>
      {/if}
    {/each}
  {/if}
</div>

<style>
  .letters { max-width: 900px; }
  .empty { padding: 28px; display: flex; flex-direction: column; gap: 10px; align-items: flex-start; color: var(--ink-2); }
  .empty :global(svg) { color: var(--accent); }
  .actions { display: flex; gap: 8px; flex-wrap: wrap; }
  .group { font-size: 1rem; color: var(--muted); margin: 18px 0 8px; font-weight: 600; }
  .list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .row { display: flex; align-items: center; gap: 6px; padding: 6px 8px 6px 16px; }
  .main { display: flex; flex-direction: column; gap: 2px; padding: 8px 0; color: var(--ink); text-decoration: none; }
  .main:hover strong { text-decoration: underline; }
</style>
