<script>
  import { api } from '../lib/api.js';
  import { toast, counts } from '../lib/stores.js';
  import { navigate } from '../lib/router.js';
  import { amountInput, parseAmount, previewPath, formatDate } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import DocCard from '../components/DocCard.svelte';
  import DocForm from '../components/DocForm.svelte';
  import Pages from '../components/Pages.svelte';
  import Jobs from '../components/Jobs.svelte';

  let { id = 0 } = $props();
  let list = $state(null);
  let doc = $state(null);
  let form = $state(null);
  let senders = $state([]);
  let tags = $state([]);
  let pattern = $state('{year}/{month}/{date}_{sender}_{title}');
  let busy = $state(false);
  let error = $state('');

  async function loadList() {
    const r = await api.get('/api/docs?status=inbox&sort=added&limit=200');
    list = r.docs;
    counts.update((c) => ({ ...c, inbox: r.total }));
  }

  async function loadDoc(docId) {
    error = '';
    doc = null;
    try {
      const [d, s, t, st] = await Promise.all([
        api.get(`/api/docs/${docId}`), api.get('/api/senders'), api.get('/api/tags'), api.get('/api/settings'),
      ]);
      doc = d.doc;
      senders = s.map((x) => x.name);
      tags = t.map((x) => x.name);
      pattern = st.settings.pattern;
      form = { title: doc.title, date: doc.date, sender: doc.sender, amount: amountInput(doc.amount_cents), tags: [...doc.tags], notes: doc.notes };
    } catch (e) {
      error = e.message;
    }
  }

  $effect(() => {
    if (id) loadDoc(id);
    else loadList().catch((e) => (error = e.message));
  });

  function patch() {
    const cents = parseAmount(form.amount);
    return { title: form.title, date: form.date || '', sender: form.sender, tags: form.tags, notes: form.notes,
      amount_cents: cents ?? undefined, no_amount: cents == null };
  }

  async function fileIt() {
    busy = true;
    try {
      const r = await api.post(`/api/docs/${id}/file`, patch());
      toast(`Filed: ${r.doc.path}`);
      const next = await api.get('/api/docs?status=inbox&sort=added&limit=5');
      counts.update((c) => ({ ...c, inbox: next.total }));
      const other = next.docs.find((d) => d.id !== id);
      navigate(other ? `/review/${other.id}` : '/review', true);
    } catch (e) {
      toast(e.message, 'error', 6000);
    } finally {
      busy = false;
    }
  }

  async function saveLater() {
    busy = true;
    try {
      await api.patch(`/api/docs/${id}`, patch());
      navigate('/review');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function remove() {
    if (!confirm('Delete this document? The PDF goes to the trash folder on the server.')) return;
    await api.del(`/api/docs/${id}`).catch((e) => toast(e.message, 'error'));
    counts.update((c) => ({ ...c, inbox: Math.max(0, c.inbox - 1) }));
    navigate('/review', true);
  }

  let path = $derived(form && doc ? previewPath(pattern, { ...form, id: doc.id }) : '');
</script>

{#if !id}
  <div class="page">
    <div class="page-head"><h1>Review</h1></div>
    <Jobs onChange={() => loadList()} />
    {#if error}<p class="error">{error}</p>{/if}
    {#if list && !list.length}
      <div class="empty card">
        <Icon name="check" size={28} />
        <p><strong>All done.</strong> New scans and uploads that Shoebox couldn’t sort on its own show up here.</p>
      </div>
    {:else if list}
      <p class="muted lead">Check what Shoebox recognized, then file. Documents a sorting rule recognizes are filed without asking.</p>
      <div class="grid">
        {#each list as d (d.id)}<DocCard doc={d} href="/review/{d.id}" />{/each}
      </div>
    {/if}
  </div>
{:else}
  <div class="page item">
    <div class="page-head">
      <a class="btn ghost iconbtn" href="/review" aria-label="Back to the review list"><Icon name="back" /></a>
      <h1>Review</h1>
      {#if doc}<span class="muted small">{doc.source === 'scan' ? 'Scanned' : doc.source === 'paperless' ? 'From Paperless' : 'Uploaded'} · {doc.pages} page{doc.pages === 1 ? '' : 's'}</span>{/if}
    </div>
    {#if error}<p class="error">{error}</p>{/if}
    {#if doc && form}
      <div class="layout">
        <div class="preview"><Pages {doc} /></div>
        <form class="side card" onsubmit={(e) => { e.preventDefault(); fileIt(); }}>
          {#if doc.rule}<p class="matched small"><Icon name="check" size={16} />Rule “{doc.rule}” matched</p>{/if}
          <DocForm bind:form {senders} {tags} idPrefix="rv" />
          <div class="path">
            <span class="muted small">Will be saved as</span>
            <span class="mono">{path}</span>
          </div>
          <div class="buttons">
            <button type="button" class="btn ghost danger iconbtn" onclick={remove} aria-label="Delete"><Icon name="trash" /></button>
            <button type="button" class="btn" onclick={saveLater} disabled={busy}>Later</button>
            <button class="btn primary grow" disabled={busy}><Icon name="check" />File it</button>
          </div>
        </form>
      </div>
    {:else if !error}
      <p class="muted">Loading …</p>
    {/if}
  </div>
{/if}

<style>
  .lead { margin: -6px 0 16px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 12px; }
  .empty { padding: 22px; display: flex; gap: 14px; align-items: center; color: var(--ink-2); max-width: 640px; }
  .layout { display: grid; gap: 16px; }
  .preview { max-height: 46vh; overflow: auto; border-radius: var(--radius); }
  .side { padding: 16px; display: flex; flex-direction: column; gap: 16px; }
  .matched { display: flex; align-items: center; gap: 6px; color: var(--ok); font-weight: 600; }
  .path { display: flex; flex-direction: column; gap: 2px; padding: 10px 12px; background: var(--bg); border-radius: 10px; word-break: break-all; }
  .path .mono { font-size: 0.8rem; }
  .buttons { display: flex; gap: 8px; position: sticky; bottom: calc(var(--nav-h) + var(--safe-b) + 8px); background: var(--card); padding-top: 4px; }
  @media (min-width: 900px) {
    .layout { grid-template-columns: minmax(0, 1.2fr) minmax(360px, 1fr); align-items: start; }
    .preview { max-height: none; overflow: visible; }
    .side { position: sticky; top: 20px; }
    .buttons { position: static; }
  }
  @media (min-width: 1500px) { .layout { grid-template-columns: minmax(0, 1fr) 460px; max-width: 1300px; } }
</style>
