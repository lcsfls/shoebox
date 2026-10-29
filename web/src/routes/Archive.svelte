<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { route, setQuery } from '../lib/router.js';
  import { monthName } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import DocCard from '../components/DocCard.svelte';
  import { slide, fade } from 'svelte/transition';
  import { flip } from 'svelte/animate';
  import { tagInfo, colorFor } from '../lib/tags.js';
  import { motion } from '../lib/motion.js';
  import { remember } from '../lib/browse.js';

  let tree = $state([]);
  let tags = $state([]);
  let senders = $state([]);
  let docs = $state([]);
  // a card dragged into another library leaves this list
  $effect(() => {
    const gone = (e) => (docs = docs.filter((d) => d.id !== e.detail));
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
  $effect(() => { f; load(); });

  // "/" jumps into the search field
  function onKey(e) {
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

  let active = $derived(
    [
      f.year && { label: f.month ? `${monthName(f.year, f.month)} ${f.year}` : f.year === 'undated' ? 'No date' : f.year, clear: { year: '', month: '' } },
      f.tag && { label: f.tag, clear: { tag: '' } },
      f.sender && { label: f.sender, clear: { sender: '' } },
    ].filter(Boolean),
  );
</script>

<svelte:window onkeydown={onKey} />

<div class="page archive">
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
        <h3>Tags</h3>
        <div class="chips">
          {#each tags.filter((t) => t.count) as t (t.id)}
            <button class="chip" data-c={colorFor(t.name, $tagInfo)} class:on={f.tag === t.name} onclick={() => pick({ tag: f.tag === t.name ? '' : t.name })}>{t.name}</button>
          {/each}
        </div>
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

  <div class="results">
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
      {#each docs as d (d.id)}<div class="cell" animate:flip={motion({ duration: 240 })} in:fade={motion({ duration: 160 })}><DocCard doc={d} href="/doc/{d.id}" /></div>{/each}
    </div>
    {#if docs.length < total}
      <div class="more"><button class="btn" onclick={() => load(true)} disabled={loading}>Show more</button></div>
    {/if}
  </div>
</div>
{#if showFilters}<button class="scrim" onclick={() => (showFilters = false)} aria-label="Close filters"></button>{/if}

<style>
  .archive { display: grid; gap: 20px; }
  .results { min-width: 0; }
  .bar { display: flex; gap: 8px; align-items: center; margin-bottom: 10px; flex-wrap: wrap; }
  .search { flex: 1 1 260px; display: flex; align-items: center; gap: 10px; padding: 0 12px; background: var(--card); border: 1px solid var(--line); border-radius: 12px; color: var(--muted); }
  .search:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
  .search input { border: 0; background: transparent; padding: 0; }
  .search input:focus-visible { outline: none; }
  .sort { width: auto; flex: none; }
  .status { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; color: var(--muted); font-size: 0.9rem; margin-bottom: 14px; min-height: 28px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 12px; }
  .more { display: flex; justify-content: center; margin-top: 18px; }
  .empty { padding: 20px; display: flex; flex-direction: column; gap: 6px; }
  .chip { cursor: pointer; }
  .cell { display: flex; min-width: 0; }
  .cell > :global(*) { flex: 1; min-width: 0; }
  .caret { display: inline-block; width: 1em; transition: transform 0.2s; color: var(--muted); }
  .caret.open { transform: rotate(90deg); }
  @media (prefers-reduced-motion: reduce) { .caret { transition: none; } }
  .chip.on { background: var(--accent); color: var(--on-accent); }
  .chips { display: flex; flex-wrap: wrap; gap: 6px; }

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
  @media (prefers-reduced-motion: reduce) { .filters { transition: none; } }
</style>
