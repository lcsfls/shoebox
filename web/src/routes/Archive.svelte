<script>
  import { onMount, untrack } from 'svelte';
  import { api } from '../lib/api.js';
  import { route, setQuery } from '../lib/router.js';
  import { monthName } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import DocCard from '../components/DocCard.svelte';
  import DocView from './DocView.svelte';
  import { slide, fade } from 'svelte/transition';
  import { flip } from 'svelte/animate';
  import { tagInfo, colorFor } from '../lib/tags.js';
  import { motion } from '../lib/motion.js';
  import { remember } from '../lib/browse.js';
  import { libraries, library, toast } from '../lib/stores.js';

  let tree = $state([]);
  let tags = $state([]);
  let senders = $state([]);
  let docs = $state([]);
  // a card dragged into another library leaves this list
  $effect(() => {
    const gone = (e) => {
      if (!docs.some((d) => d.id === e.detail)) return;
      docs = docs.filter((d) => d.id !== e.detail);
      sel = sel.filter((x) => x !== e.detail);
      total = Math.max(0, total - 1);
    };
    addEventListener('sb-doc-moved', gone);
    return () => removeEventListener('sb-doc-moved', gone);
  });
  let total = $state(0);
  let loading = $state(false);
  let error = $state('');
  let qInput = $state($route.query.get('q') || '');
  let showFilters = $state(false);
  let collapsed = $state(''); // the selected year whose months are folded away

  function clickYear(y) {
    if (f.year === y && !f.month) collapsed = collapsed === y ? '' : y; // second click folds it
    else { collapsed = ''; pick({ year: y, month: '' }); }
  }

  let f = $derived({
    q: $route.query.get('q') || '',
    year: $route.query.get('year') || '',
    month: $route.query.get('month') || '',
    tag: $route.query.get('tag') || '',
    sender: $route.query.get('sender') || '',
    sort: $route.query.get('sort') || '',
  });

  onMount(async () => {
    try {
      [tree, tags, senders] = await Promise.all([api.get('/api/tree'), api.get('/api/tags'), api.get('/api/senders')]);
    } catch (e) {
      error = e.message;
    }
  });

  let seq = 0;
  async function load(append = false) {
    const my = ++seq;
    loading = true;
    const p = new URLSearchParams();
    for (const [k, v] of Object.entries(f)) if (v) p.set(k, v);
    if (f.q && !f.sort) p.set('sort', 'relevance');
    p.set('limit', '60');
    p.set('offset', append ? String(docs.length) : '0');
    try {
      const r = await api.get('/api/docs?' + p);
      if (my !== seq) return;
      docs = append ? [...docs, ...r.docs] : r.docs;
      total = r.total;
      p.delete('limit');
      p.delete('offset');
      remember(p.toString(), docs.map((d) => d.id), total); // for ← / → on a document
      error = '';
    } catch (e) {
      if (my === seq) error = e.message;
    } finally {
      if (my === seq) loading = false;
    }
  }
  // reload only when a filter changes (not when ?doc= opens a document next to the list)
  let fKey = $derived(JSON.stringify(f));
  $effect(() => { fKey; untrack(() => load()); });

  // a document opens next to the list on a wide screen (?doc=), or as a full page:
  // the choice is kept in this browser
  let openId = $derived(Number($route.query.get('doc')) || 0);
  let wide = $state(false);
  let pane = $state(readPane());
  function readPane() {
    try { return localStorage.getItem('sb-doc-open') !== 'page'; } catch { return true; }
  }
  $effect(() => { try { localStorage.setItem('sb-doc-open', pane ? 'pane' : 'page'); } catch {} });
  $effect(() => {
    const mq = matchMedia('(min-width: 1200px)');
    const set = () => (wide = mq.matches);
    set();
    mq.addEventListener('change', set);
    return () => mq.removeEventListener('change', set);
  });
  let showPane = $derived(wide && pane && openId > 0);
  function open(d) {
    if (!wide || !pane) return false; // a normal link to /doc/…
    setQuery({ doc: String(d.id) });
    return true;
  }

  // "/" jumps into the search field
  // choosing several documents: to download them as a ZIP or move them
  let selecting = $state(false);
  let sel = $state([]); // ids
  let anchor = -1; // for Shift+click: from here to there
  let moving = $state(false);
  let moveTargets = $derived($libraries.filter((l) => l.id !== $library && l.role === 'editor'));
  function startSelecting() { selecting = true; sel = []; anchor = -1; }
  function stopSelecting() { selecting = false; sel = []; moving = false; }
  function tick(i, e) {
    const id = docs[i].id;
    if (e.shiftKey && anchor >= 0) {
      const [a, b] = [Math.min(anchor, i), Math.max(anchor, i)];
      sel = [...new Set([...sel, ...docs.slice(a, b + 1).map((d) => d.id)])];
    } else {
      sel = sel.includes(id) ? sel.filter((x) => x !== id) : [...sel, id];
    }
    anchor = i;
  }
  function download() {
    location.href = '/api/docs/zip?ids=' + sel.join(',');
  }
  async function moveSelected(lib) {
    moving = false;
    let ok = 0;
    const failed = [];
    for (const id of sel) {
      try {
        await api.post(`/api/docs/${id}/move`, { library: lib.id });
        ok++;
        dispatchEvent(new CustomEvent('sb-doc-moved', { detail: id }));
      } catch (err) {
        failed.push(err.message);
      }
    }
    toast(`Moved ${ok} document${ok === 1 ? '' : 's'} to “${lib.name}”` + (failed.length ? `; ${failed.length} not: ${failed[0]}` : ''), failed.length ? 'error' : 'info', 6000);
    stopSelecting();
  }

  function onKey(e) {
    if (selecting && e.key === 'Escape' && !document.querySelector('[role="dialog"]')) { stopSelecting(); return; }
    if (selecting && e.key === 'a' && (e.ctrlKey || e.metaKey) && !e.target.closest?.('input, textarea')) { e.preventDefault(); sel = docs.map((d) => d.id); return; }
    if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.target.closest?.('input, textarea, select, [contenteditable]') || document.querySelector('[role="dialog"]')) return;
    e.preventDefault();
    document.getElementById('arch-q')?.focus();
  }

  let typing;
  function onType() {
    clearTimeout(typing);
    typing = setTimeout(() => setQuery({ q: qInput.trim() }), 250);
  }

  function pick(params) {
    setQuery(params, false);
    showFilters = false;
  }

  // several tags at once: a document needs all of them (?tag=a,b)
  let picked = $derived(f.tag ? f.tag.split(',') : []);
  const withTag = (t, on) => (on ? [...picked, t] : picked.filter((x) => x !== t)).join(',');
  function toggleTag(t) {
    setQuery({ tag: withTag(t, !picked.includes(t)) }, false); // the filter panel stays open for the next one
  }

  let active = $derived(
    [
      f.year && { label: f.month ? `${monthName(f.year, f.month)} ${f.year}` : f.year === 'undated' ? 'No date' : f.year, clear: { year: '', month: '' } },
      ...picked.map((t) => ({ label: t, clear: { tag: withTag(t, false) } })),
      f.sender && { label: f.sender, clear: { sender: '' } },
    ].filter(Boolean),
  );
</script>

<svelte:window onkeydown={onKey} />

<div class="page archive" class:with-pane={showPane}>
  <aside class="filters" class:open={showFilters}>
    <div class="filters-head">
      <h2>Filter</h2>
      <button class="btn ghost iconbtn close" onclick={() => (showFilters = false)} aria-label="Close filters"><Icon name="close" /></button>
    </div>
    <section>
      <h3>Folders</h3>
      <ul class="tree">
        <li><button class:on={!f.year} onclick={() => pick({ year: '', month: '' })}>All documents</button></li>
        {#each tree as y (y.year)}
          <li>
            <button class:on={f.year === y.year && !f.month} onclick={() => clickYear(y.year)}
              aria-expanded={y.months.length ? f.year === y.year && collapsed !== y.year : undefined}>
              <span class="mono"><span class="caret" class:open={f.year === y.year && collapsed !== y.year} aria-hidden="true">{y.months.length ? '›' : ''}</span>{y.year === 'undated' ? 'No date' : y.year + '/'}</span><span class="n">{y.count}</span>
            </button>
            {#if f.year === y.year && collapsed !== y.year && y.months.length}
              <ul transition:slide={motion({ duration: 220 })}>
                {#each y.months as m (m.month)}
                  <li><button class:on={f.month === m.month} onclick={() => pick({ year: y.year, month: m.month })}>
                    <span><span class="mono">{m.month}/</span> <span class="muted">{monthName(y.year, m.month)}</span></span><span class="n">{m.count}</span>
                  </button></li>
                {/each}
              </ul>
            {/if}
          </li>
        {/each}
      </ul>
    </section>
    {#if tags.length}
      <section>
        <h3>Tags{#if picked.length > 1} <span class="muted small all">all of them</span>{/if}</h3>
        <div class="chips">
          {#each tags.filter((t) => t.count || picked.includes(t.name)) as t (t.id)}
            <button class="chip" data-c={colorFor(t.name, $tagInfo)} class:on={picked.includes(t.name)} aria-pressed={picked.includes(t.name)} onclick={() => toggleTag(t.name)}>{t.name}</button>
          {/each}
        </div>
        {#if picked.length}<button class="btn small ghost clear-tags" onclick={() => setQuery({ tag: '' }, false)}>Clear tags</button>{/if}
      </section>
    {/if}
    {#if senders.length}
      <section>
        <h3>Senders</h3>
        <ul class="tree">
          {#each senders.filter((s) => s.count) as s (s.id)}
            <li><button class:on={f.sender === s.name} onclick={() => pick({ sender: f.sender === s.name ? '' : s.name })}>
              <span class="ell">{s.name}</span><span class="n">{s.count}</span>
            </button></li>
          {/each}
        </ul>
      </section>
    {/if}
  </aside>

  <div class="results" class:selecting>
    <div class="bar">
      <form class="search" role="search" onsubmit={(e) => { e.preventDefault(); setQuery({ q: qInput.trim() }); }}>
        <Icon name="search" />
        <label class="sr-only" for="arch-q">Search</label>
        <input id="arch-q" type="search" bind:value={qInput} oninput={onType} placeholder="Search everything …" title="Search (/)" enterkeyhint="search" />
      </form>
      <button class="btn mobile-only" onclick={() => (showFilters = true)}><Icon name="folder" />Filter</button>
      <label class="sr-only" for="arch-sort">Sort</label>
      <select id="arch-sort" class="sort" value={f.sort} onchange={(e) => setQuery({ sort: e.currentTarget.value })}>
        <option value="">{f.q ? 'Best match' : 'Newest first'}</option>
        {#if f.q}<option value="date">Newest first</option>{/if}
        <option value="added">Recently added</option>
        <option value="sender">Sender</option>
        <option value="amount">Amount</option>
      </select>
      {#if wide}
        <button class="btn iconbtn" onclick={() => { pane = !pane; if (!pane) setQuery({ doc: '' }); }} aria-pressed={pane}
          aria-label={pane ? 'Open documents as a full page' : 'Open documents next to the list'} title={pane ? 'Documents open next to the list' : 'Documents open as a full page'}>
          <Icon name={pane ? 'columns' : 'expand'} />
        </button>
      {/if}
      {#if docs.length}
        <button class="btn" class:primary={selecting} onclick={() => (selecting ? stopSelecting() : startSelecting())} aria-pressed={selecting}>
          <Icon name="check" />{selecting ? 'Done' : 'Select'}
        </button>
      {/if}
    </div>

    <div class="status">
      <span>{loading && !docs.length ? 'Searching …' : `${total} document${total === 1 ? '' : 's'}`}{f.q ? ` for “${f.q}”` : ''}</span>
      {#each active as a}
        <button class="chip on" onclick={() => setQuery(a.clear)} aria-label="Remove filter {a.label}">{a.label} ×</button>
      {/each}
    </div>
    {#if error}<p class="error">{error}</p>{/if}

    {#if !loading && !docs.length && !error}
      <div class="empty card">
        <p><strong>{f.q ? 'Nothing found.' : 'No documents here yet.'}</strong></p>
        <p class="muted">{f.q ? 'Try fewer or shorter words; the search also finds the beginnings of words.' : 'Scan or upload something, or copy PDFs into the archive folder.'}</p>
      </div>
    {/if}
    <div class="grid">
      {#each docs as d, i (d.id)}<div class="cell" animate:flip={motion({ duration: 240 })} in:fade={motion({ duration: 160 })}>
        <DocCard doc={d} href="/doc/{d.id}" {selecting} selected={sel.includes(d.id)} onselect={(e) => tick(i, e)} onopen={() => open(d)} current={showPane && d.id === openId}
          dragIds={selecting && sel.includes(d.id) ? sel : null} />
      </div>{/each}
    </div>
    {#if docs.length < total}
      <div class="more"><button class="btn" onclick={() => load(true)} disabled={loading}>Show more</button></div>
    {/if}
  </div>
  {#if showPane}
    <section class="pane" aria-label="Document">
      {#key openId}<DocView id={openId} embedded onclose={() => setQuery({ doc: '' })} />{/key}
    </section>
  {/if}
</div>
{#if selecting}
  <div class="selbar card" role="toolbar" aria-label="Selected documents">
    <strong>{sel.length} selected</strong>
    <button class="btn small ghost" onclick={() => (sel = sel.length === docs.length ? [] : docs.map((d) => d.id))}>{sel.length === docs.length ? 'None' : 'All'}{docs.length < total ? ` ${docs.length} shown` : ''}</button>
    <span class="grow"></span>
    <button class="btn small" onclick={download} disabled={!sel.length}><Icon name="download" />Download ZIP</button>
    {#if moveTargets.length}
      <button class="btn small" onclick={() => (moving = !moving)} disabled={!sel.length} aria-expanded={moving}><Icon name="folder" />Move to …</button>
    {/if}
    <button class="btn small ghost iconbtn" onclick={stopSelecting} aria-label="Stop selecting" title="Esc"><Icon name="close" /></button>
    {#if moving}
      <div class="targets" role="group" aria-label="Move the selected documents to">
        {#each moveTargets as l (l.id)}<button class="btn small" onclick={() => moveSelected(l)} aria-label="Move selected to {l.name}">{l.name}</button>{/each}
      </div>
    {/if}
  </div>
{/if}
{#if showFilters}<button class="scrim" onclick={() => (showFilters = false)} aria-label="Close filters"></button>{/if}

<style>
  .archive { display: grid; gap: 20px; }
  .archive.with-pane { max-width: none; }
  .pane { min-width: 0; position: sticky; top: 16px; max-height: calc(100dvh - 32px); overflow: auto; overscroll-behavior: contain; padding-right: 4px; }
  .results { min-width: 0; }
  .results.selecting { padding-bottom: 110px; }
  .bar { display: flex; gap: 8px; align-items: center; margin-bottom: 10px; flex-wrap: wrap; }
  .search { flex: 1 1 260px; display: flex; align-items: center; gap: 10px; padding: 0 12px; background: var(--card); border: 1px solid var(--line); border-radius: 12px; color: var(--muted); }
  .search:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
  .search input { border: 0; background: transparent; padding: 0; }
  .search input:focus-visible { outline: none; }
  .sort { width: auto; flex: none; }
  .status { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; color: var(--muted); font-size: 0.9rem; margin-bottom: 14px; min-height: 28px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 12px; }
  .more { display: flex; justify-content: center; margin-top: 18px; }
  .selbar { position: fixed; z-index: 35; left: 0; right: 0; bottom: max(16px, env(safe-area-inset-bottom)); margin: 0 auto; width: min(720px, calc(100vw - 24px));
    display: flex; flex-wrap: wrap; align-items: center; gap: 8px; padding: 10px 12px; box-shadow: var(--shadow); }
  .selbar .grow { flex: 1; }
  @media (max-width: 899px) { .selbar { bottom: calc(76px + env(safe-area-inset-bottom)); } }
  .selbar .targets { flex-basis: 100%; display: flex; flex-wrap: wrap; gap: 6px; justify-content: flex-end; }
  .selbar .targets .btn { border-style: dashed; }
  .empty { padding: 20px; display: flex; flex-direction: column; gap: 6px; }
  .chip { cursor: pointer; }
  .cell { display: flex; min-width: 0; }
  .cell > :global(*) { flex: 1; min-width: 0; }
  .caret { display: inline-block; width: 1em; transition: transform 0.2s; color: var(--muted); }
  .caret.open { transform: rotate(90deg); }
  @media (prefers-reduced-motion: reduce) { .caret { transition: none; } }
  .chip.on { background: var(--accent); color: var(--on-accent); }
  .chips { display: flex; flex-wrap: wrap; gap: 6px; }
  .all { font-weight: 400; text-transform: none; letter-spacing: 0; }
  .clear-tags { margin-top: 6px; }

  .filters { position: fixed; z-index: 40; inset: auto 0 0 0; max-height: 80dvh; overflow: auto; background: var(--card); border-radius: 18px 18px 0 0;
    padding: 16px 16px calc(24px + var(--safe-b)); box-shadow: 0 -10px 40px rgb(0 0 0 / 0.2); transform: translateY(105%); transition: transform 0.25s ease-out;
    display: flex; flex-direction: column; gap: 18px; }
  .filters.open { transform: none; }
  .filters-head { display: flex; align-items: center; justify-content: space-between; }
  .scrim { position: fixed; inset: 0; z-index: 35; background: rgb(0 0 0 / 0.35); border: 0; }
  h3 { font-size: 0.8rem; font-weight: 650; color: var(--muted); margin: 0 0 6px; letter-spacing: 0.02em; }
  .tree { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 1px; }
  .tree ul { list-style: none; margin: 2px 0 4px 14px; padding: 0; }
  .tree button { width: 100%; display: flex; justify-content: space-between; align-items: center; gap: 8px; min-height: 36px; padding: 0 10px; border: 0; border-radius: 8px;
    background: transparent; color: var(--ink); font: inherit; text-align: left; cursor: pointer; }
  .tree button:hover { background: var(--bg); }
  .tree button.on { background: var(--chip); color: var(--chip-ink); font-weight: 600; }
  .n { color: var(--muted); font-size: 0.82rem; font-family: var(--mono); }
  .ell { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  @media (min-width: 900px) {
    .archive { grid-template-columns: 250px minmax(0, 1fr); align-items: start; }
    .filters { position: sticky; top: 20px; inset: auto; transform: none; max-height: calc(100dvh - 40px); box-shadow: none; border-radius: var(--radius);
      border: 1px solid var(--line); padding: 16px 10px; transition: none; }
    .filters-head .close, .mobile-only, .scrim { display: none; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(170px, 1fr)); gap: 16px; }
  }
  @media (min-width: 1800px) { .grid { grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); } }
  @media (min-width: 1200px) {
    .archive.with-pane { grid-template-columns: 200px minmax(310px, 0.8fr) minmax(0, 1.7fr); gap: 16px; }
    .with-pane .grid { grid-template-columns: repeat(auto-fill, minmax(135px, 1fr)); gap: 10px; }
    /* the narrow middle column: the search on its own line, the buttons below */
    .with-pane .search { flex-basis: 100%; }
    .with-pane .sort { flex: 1; min-width: 0; }
  }
  @media (prefers-reduced-motion: reduce) { .filters { transition: none; } }
</style>
