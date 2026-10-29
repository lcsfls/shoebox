<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { uploadFiles, uploading } from '../lib/uploads.js';
  import { dragging } from '../lib/drop.js';
  import { toast, counts, canAdd } from '../lib/stores.js';
  import LibrarySwitch from '../components/LibrarySwitch.svelte';
  import { fly } from 'svelte/transition';
  import { motion } from '../lib/motion.js';
  import { tagInfo, colorFor } from '../lib/tags.js';
  import { navigate } from '../lib/router.js';
  import { formatDate, fileVersion } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';
  import Thumb from '../components/Thumb.svelte';
  import Jobs from '../components/Jobs.svelte';

  let data = $state(null);
  let q = $state('');
  let fileInput = $state();

  async function load() {
    try {
      data = await api.get('/api/overview');
      counts.set({ inbox: data.inbox, busy: data.busy, filed: data.filed, missing: data.missing });
    } catch (e) {
      toast(e.message, 'error');
    }
  }
  onMount(load);

  function search(e) {
    e.preventDefault();
    navigate('/archive' + (q.trim() ? '?q=' + encodeURIComponent(q.trim()) : ''));
  }

  async function onFiles(e) {
    const files = [...e.target.files];
    e.target.value = '';
    await uploadFiles(files);
  }
</script>

<div class="page home">
  <form class="search" role="search" onsubmit={search}>
    <Icon name="search" />
    <label class="sr-only" for="home-q">Search documents</label>
    <input id="home-q" type="search" bind:value={q} placeholder="Search text, sender, tags …" enterkeyhint="search" />
  </form>

  <div class="mobile-only libtop"><LibrarySwitch compact /></div>
  {#if $canAdd}
  <div class="actions">
    <a class="scan" href="/scan">
      <span class="scan-icon"><Icon name="scan" size={30} /></span>
      <span class="scan-text"><strong>Scan a document</strong><span>Several pages, straightened automatically</span></span>
    </a>
    <button class="btn up" class:hover={$dragging} onclick={() => fileInput.click()} disabled={$uploading > 0}>
      <Icon name="upload" />
      <span>{$uploading ? `Uploading ${Math.round($uploading * 100)} %` : 'Upload a PDF or photo'}</span>
      <span class="drop-hint muted small">or drop files here</span>
    </button>
    <input bind:this={fileInput} type="file" accept="application/pdf,image/*" multiple hidden onchange={onFiles} />
  </div>
  {/if}

  <Jobs onChange={load} />

  {#if data}
    {#if data.review.length}
      <section>
        <div class="sec-head">
          <h2>To review</h2>
          <a href="/review">{data.inbox} waiting</a>
        </div>
        <div class="strip">
          {#each data.review as d (d.id)}
            <a class="mini card" href="/review/{d.id}">
              <Thumb id={d.id} updated={fileVersion(d)} />
              <span class="mini-title">{d.title || d.sender || 'New document'}</span>
              <span class="muted small">{d.date ? formatDate(d.date) : 'no date'}</span>
            </a>
          {/each}
        </div>
      </section>
    {/if}

    <section>
      <div class="sec-head">
        <h2>Recently filed</h2>
        {#if data.filed}<a href="/archive">All {data.filed}</a>{/if}
      </div>
      {#if data.recent.length}
        <ul class="recent">
          {#each data.recent as d, i (d.id)}
            <li in:fly={motion({ y: 6, duration: 220, delay: Math.min(i, 8) * 30 })}>
              <a href="/doc/{d.id}">
                <span class="r-thumb"><Thumb id={d.id} updated={fileVersion(d)} /></span>
                <span class="grow">
                  <span class="r-title">{d.title || 'Untitled'}{#if d.sender}{' '}<span class="muted">· {d.sender}</span>{/if}</span>
                  <span class="mono muted path">{d.path}</span>
                  {#if d.tags?.length}<span class="r-tags">{#each d.tags.slice(0, 3) as t (t)}<span class="chip" data-c={colorFor(t, $tagInfo)}>{t}</span>{/each}</span>{/if}
                </span>
                {#if d.date}<span class="r-date muted small">{formatDate(d.date)}</span>{/if}
              </a>
            </li>
          {/each}
        </ul>
      {:else}
        <div class="empty card">
          <p><strong>Nothing filed yet.</strong></p>
          <p class="muted">Scan your first letter, upload PDFs, copy files into the archive folder, or bring everything over from Paperless under <a href="/settings/import">Settings → Import</a>.</p>
        </div>
      {/if}
    </section>
  {/if}
</div>

<style>
  .libtop { margin-bottom: 12px; }
  .home { display: flex; flex-direction: column; gap: 20px; max-width: 1100px; }
  .search { display: flex; align-items: center; gap: 10px; padding: 0 14px; background: var(--card); border: 1px solid var(--line); border-radius: 14px; color: var(--muted); }
  .search input { border: 0; background: transparent; min-height: 52px; padding: 0; }
  .search:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
  .search input:focus-visible { outline: none; }
  .actions { display: grid; gap: 10px; }
  .scan { display: flex; align-items: center; gap: 16px; min-height: 108px; padding: 0 22px; border-radius: 20px; background: var(--accent); color: var(--on-accent); text-decoration: none; transition: transform 0.15s, background 0.15s; }
  .scan:hover { background: var(--accent-hover); }
  .scan:active { transform: scale(0.99); }
  .scan-icon { width: 58px; height: 58px; border-radius: 16px; background: color-mix(in srgb, var(--on-accent) 16%, transparent); display: grid; place-items: center; flex: none; }
  .scan-text { display: flex; flex-direction: column; gap: 2px; }
  .scan-text strong { font-size: 1.25rem; font-weight: 650; }
  .scan-text span { opacity: 0.85; font-size: 0.92rem; }
  .up { min-height: 52px; border-radius: 14px; }
  .up.hover { border: 2px dashed var(--accent); background: var(--chip); }
  .drop-hint { display: none; font-weight: 400; }
  @media (pointer: fine) { .drop-hint { display: block; } }
  @media (min-width: 700px) {
    .actions { grid-template-columns: 2fr 1fr; }
    .up { min-height: 108px; border-radius: 20px; flex-direction: column; }
  }
  section { display: flex; flex-direction: column; gap: 10px; }
  .sec-head { display: flex; align-items: baseline; justify-content: space-between; }
  .sec-head a { font-weight: 500; text-decoration: none; }
  .strip { display: grid; grid-auto-flow: column; grid-auto-columns: minmax(130px, 150px); gap: 10px; overflow-x: auto; padding-bottom: 6px; scroll-snap-type: x proximity; }
  .mini { display: flex; flex-direction: column; gap: 6px; padding: 8px; text-decoration: none; color: var(--ink); scroll-snap-align: start; }
  .mini-title { font-weight: 600; font-size: 0.88rem; line-height: 1.3; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
  .recent { list-style: none; margin: 0; padding: 0; }
  .r-thumb { width: 42px; flex: none; }
  .r-thumb :global(.thumb) { border-radius: 5px; box-shadow: 0 0 0 1px var(--line); }
  .r-tags { display: flex; gap: 4px; margin-top: 4px; overflow: hidden; }
  .r-tags .chip { min-height: 20px; font-size: 0.72rem; padding: 0 7px; }
  .r-date { flex: none; font-variant-numeric: tabular-nums; }
  .recent a { display: flex; align-items: center; gap: 12px; padding: 10px 4px; border-bottom: 1px solid var(--line); color: var(--ink); text-decoration: none; }
  .recent a:hover .r-title { color: var(--accent); }
  .recent .grow { display: flex; flex-direction: column; min-width: 0; }
  .r-title { font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .path { font-size: 0.78rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .empty { padding: 18px; display: flex; flex-direction: column; gap: 6px; }
</style>
