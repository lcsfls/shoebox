<script>
  import { api } from '../lib/api.js';
  import { toast, libraries } from '../lib/stores.js';
  import { tagInfo, colorFor } from '../lib/tags.js';
  import { navigate, setQuery } from '../lib/router.js';
  import { amountInput, parseAmount, formatDate, formatSize, formatAmount, fileVersion } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import DocForm from '../components/DocForm.svelte';
  import Pages from '../components/Pages.svelte';
  import PageEditor from '../components/PageEditor.svelte';
  import Stamps from '../components/Stamps.svelte';
  import MoveTo from '../components/MoveTo.svelte';
  import { stampConfig } from '../lib/stamps.js';
  import { recall, more } from '../lib/browse.js';

  // embedded: shown in the archive next to the list (?doc=), with a way to the full page
  let { id, embedded = false, onclose = null } = $props();
  let doc = $state(null);
  let text = $state('');
  let form = $state(null);
  let senders = $state([]);
  let tags = $state([]);
  let editing = $state(false);
  let editPages = $state(false);
  let mayEdit = $derived(!!doc && $libraries.find((l) => l.id === doc.library_id)?.role === 'editor');
  let libName = $derived(doc && $libraries.length > 1 ? $libraries.find((l) => l.id === doc.library_id)?.name || '' : '');
  let showText = $state(false);
  let busy = $state(false);
  let error = $state('');
  let stamps = $state([]);
  let stampCfg = $state(stampConfig(null));

  async function load() {
    try {
      const [d, s, t, st] = await Promise.all([api.get(`/api/docs/${id}`), api.get('/api/senders'), api.get('/api/tags?suggest=1'), api.get('/api/settings').catch(() => null)]);
      doc = d.doc;
      text = d.text;
      stamps = d.stamps || [];
      if (st) stampCfg = stampConfig(st.settings);
      senders = s.map((x) => x.name);
      tags = t.map((x) => x.name);
      resetForm();
    } catch (e) {
      error = e.message;
    }
  }
  function resetForm() {
    form = { title: doc.title, date: doc.date, sender: doc.sender, amount: amountInput(doc.amount_cents), tags: [...doc.tags], notes: doc.notes };
  }
  $effect(() => { id; editing = false; load(); });

  // ← / →: the neighbours in the archive list this document was opened from
  let browse = $state(recall());
  $effect(() => { id; if (embedded) browse = recall(); }); // the archive next to it may have changed its list
  let pos = $derived(browse ? browse.ids.indexOf(Number(id)) : -1);
  let prevId = $derived(pos > 0 ? browse.ids[pos - 1] : 0);
  let nextId = $derived(pos >= 0 && pos < browse.ids.length - 1 ? browse.ids[pos + 1] : 0);
  $effect(() => {
    // at the end of what the archive loaded: fetch the next page of the list
    if (pos >= 0 && pos === browse.ids.length - 1 && browse.ids.length < browse.total) more(browse).then((b) => (browse = b)).catch(() => {});
  });
  // leaving the form: ask only when something was changed
  function mayLeave() {
    if (!editing || !doc) return true;
    const was = { title: doc.title, date: doc.date, sender: doc.sender, amount: amountInput(doc.amount_cents), tags: [...doc.tags], notes: doc.notes };
    return JSON.stringify(form) === JSON.stringify(was) || confirm('Discard the changes?');
  }
  function go(to) {
    if (!to || !mayLeave()) return;
    if (embedded) setQuery({ doc: String(to) });
    else navigate(`/doc/${to}`, true);
  }
  function cancelEdit() {
    if (mayLeave()) { resetForm(); editing = false; }
  }
  function onKey(e) {
    if (e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
    if (e.target.closest?.('input, textarea, select, [contenteditable], [role="dialog"]') || document.querySelector('[role="dialog"]')) {
      if (e.key === 'Escape' && editing && !document.querySelector('[role="dialog"]')) cancelEdit();
      return;
    }
    if (e.key === 'ArrowLeft' && prevId) { e.preventDefault(); go(prevId); }
    else if (e.key === 'ArrowRight' && nextId) { e.preventDefault(); go(nextId); }
    else if (e.key === 'e' && mayEdit && !editing) { e.preventDefault(); editing = true; }
    else if (e.key === 'Escape' && editing) cancelEdit();
    else if (e.key === 'Escape' && embedded) onclose?.();
  }

  async function save(e) {
    e.preventDefault();
    busy = true;
    const cents = parseAmount(form.amount);
    try {
      const r = await api.patch(`/api/docs/${id}`, { title: form.title, date: form.date || '', sender: form.sender, tags: form.tags,
        notes: form.notes, amount_cents: cents ?? undefined, no_amount: cents == null });
      doc = r.doc;
      editing = false;
      toast('Saved');
    } catch (err) {
      toast(err.message, 'error', 6000);
    } finally {
      busy = false;
    }
  }

  async function remove() {
    if (!confirm('Delete this document? The PDF goes to the trash folder on the server.')) return;
    try {
      await api.del(`/api/docs/${id}`);
      toast('Deleted');
      if (embedded) { dispatchEvent(new CustomEvent('sb-doc-moved', { detail: Number(id) })); onclose?.(); }
      else history.length > 1 ? history.back() : navigate('/archive', true);
    } catch (err) {
      toast(err.message, 'error');
    }
  }

  const sources = { scan: 'Scanned', upload: 'Uploaded', folder: 'Found in the archive folder', paperless: 'Imported from Paperless', letter: 'Written in Shoebox' };
  const ocrNames = { pdf: 'text already in the PDF', tesseract: 'Tesseract', paperless: 'Paperless', 'tesseract+ai': 'Tesseract and the AI model', 'pdf+ai': 'AI model' };
</script>

{#if editPages && doc}
  <PageEditor {doc} onclose={() => (editPages = false)} onsaved={(d) => { editPages = false; load(); }} />
{/if}

<svelte:window onkeydown={onKey} />

<div class="page docview" class:embedded>
  <div class="page-head">
    {#if embedded}
      <button class="btn ghost iconbtn" onclick={() => onclose?.()} aria-label="Close the document" title="Close (Esc)"><Icon name="close" /></button>
    {:else}
      <button class="btn ghost iconbtn" onclick={() => (history.length > 1 ? history.back() : navigate('/archive'))} aria-label="Back"><Icon name="back" /></button>
    {/if}
    <h1 class="title">{doc ? doc.title || 'Untitled' : ''}</h1>
    {#if pos >= 0 && browse.total > 1}
      <div class="nav">
        <span class="muted small">{pos + 1} of {browse.total}</span>
        <button type="button" class="btn small iconbtn" onclick={() => go(prevId)} disabled={!prevId} aria-label="Previous document" title="Previous (←)"><Icon name="back" /></button>
        <button type="button" class="btn small iconbtn next" onclick={() => go(nextId)} disabled={!nextId} aria-label="Next document" title="Next (→)"><Icon name="back" /></button>
      </div>
    {/if}
    {#if embedded}
      <a class="btn small iconbtn" href="/doc/{id}" aria-label="Open as a full page" title="Full page"><Icon name="expand" /></a>
    {/if}
  </div>
  {#if error}<p class="error">{error}</p>{/if}
  {#if doc}
    {#if doc.status === 'missing'}
      <p class="notice">The file is no longer in the archive folder (moved outside it or deleted). It comes back when the file reappears.</p>
    {/if}
    <div class="layout">
      <div class="preview"><Pages {doc} /></div>
      <div class="side">
        {#if editing}
          <form class="card box stack" onsubmit={save}>
            <DocForm bind:form {senders} {tags} idPrefix="dv" docId={doc.id} pages={doc.pages} updated={fileVersion(doc)} />
            <p class="hint">Saving renames the file to match{doc.source === 'folder' ? ', except for files you put into the archive folder yourself: those keep their name' : ''}.</p>
            <div class="row">
              <button type="button" class="btn" onclick={() => { resetForm(); editing = false; }}>Cancel</button>
              <button class="btn primary grow" disabled={busy}>Save</button>
            </div>
          </form>
        {:else}
          <div class="card box stack">
            <dl>
              <dt>Sender</dt><dd>{doc.sender || '—'}</dd>
              <dt>Date</dt><dd>{doc.date ? formatDate(doc.date) : '—'}</dd>
              {#if doc.amount_cents != null}<dt>Amount</dt><dd>{formatAmount(doc.amount_cents)}</dd>{/if}
              <dt>Tags</dt><dd>{#if doc.tags.length}<span class="chips">{#each doc.tags as t}<a class="chip" data-c={colorFor(t, $tagInfo)} href="/archive?tag={encodeURIComponent(t)}">{t}</a>{/each}</span>{:else}—{/if}</dd>
              {#each Object.entries(doc.fields || {}) as [k, v]}<dt>{k}</dt><dd>{v}</dd>{/each}
              {#if doc.notes}<dt>Notes</dt><dd class="pre">{doc.notes}</dd>{/if}
            </dl>
            <div class="row">
              {#if mayEdit}<button class="btn" onclick={() => (editing = true)} title="Edit (E)"><Icon name="edit" />Edit</button>{/if}
              <a class="btn" href="/api/docs/{doc.id}/pdf?dl=1" download><Icon name="download" />Download</a>
              <a class="btn" href="/api/docs/{doc.id}/pdf" target="_blank" rel="noopener"><Icon name="file" />PDF</a>
            </div>
          </div>
        {/if}
        <div class="card box info small">
          <p class="mono path">{libName ? `${libName} · ` : ''}{doc.status === 'inbox' ? 'In the review inbox' : doc.path}</p>
          <p class="muted">{sources[doc.source] || doc.source}{doc.added_by ? ` by ${doc.added_by}` : ''} · {doc.pages} page{doc.pages === 1 ? '' : 's'} · {formatSize(doc.size)} · text: {ocrNames[doc.ocr] || doc.ocr}{doc.rule ? ` · rule “${doc.rule}”` : ''}</p>
          <div class="row">
            <button class="btn small" onclick={() => (showText = !showText)}><Icon name="text" />{showText ? 'Hide the text' : 'Show the recognized text'}</button>
            {#if mayEdit}
              <button class="btn small" onclick={() => (editPages = true)}><Icon name="pages" />Edit pages</button>
              <button class="btn small ghost danger" onclick={remove}><Icon name="trash" />Delete</button>
            {/if}
          </div>
          {#if mayEdit}<MoveTo {doc} onmoved={() => (embedded ? onclose?.() : load())} />{/if}
          {#if mayEdit || stamps.length}
            <Stamps {doc} {stamps} config={stampCfg} editable={mayEdit} onchange={() => load()} />
          {/if}
          {#if showText}<pre class="ocr">{text || 'No text recognized.'}</pre>{/if}
        </div>
      </div>
    </div>
  {:else if !error}
    <p class="muted">Loading …</p>
  {/if}
</div>

<style>
  .title { overflow: hidden; text-overflow: ellipsis; }
  .page-head .nav { margin-left: auto; display: flex; align-items: center; gap: 6px; flex: none; }
  .nav .next :global(svg) { transform: scaleX(-1); }
  .docview { container-type: inline-size; }
  .docview.embedded { padding: 0; max-width: none; margin: 0; }
  .embedded .page-head { margin-bottom: 12px; }
  .embedded .page-head h1 { font-size: 1.2rem; }
  .layout { display: grid; gap: 16px; }
  .side { display: flex; flex-direction: column; gap: 12px; }
  .box { padding: 16px; }
  dl { display: grid; grid-template-columns: auto 1fr; gap: 8px 16px; margin: 0; }
  dt { color: var(--muted); font-size: 0.88rem; }
  dd { margin: 0; font-weight: 500; min-width: 0; overflow-wrap: anywhere; }
  .pre { white-space: pre-wrap; font-weight: 400; }
  .chips { display: flex; flex-wrap: wrap; gap: 6px; }
  .chips .chip { text-decoration: none; }
  .info { display: flex; flex-direction: column; gap: 8px; }
  .path { word-break: break-all; }
  .ocr { white-space: pre-wrap; font-family: var(--mono); font-size: 0.78rem; max-height: 50vh; overflow: auto; background: var(--bg); padding: 10px; border-radius: 8px; margin: 0; }
  /* two columns where there is room: the full page, or the archive's side pane */
  @container (min-width: 720px) {
    .layout { grid-template-columns: minmax(0, 1.25fr) minmax(300px, 1fr); align-items: start; max-width: 1400px; }
    .side { position: sticky; top: 20px; }
  }
</style>
