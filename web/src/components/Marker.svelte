<script>
  // Mark a spot on the page (drag a box, then move or resize it) and use the
  // words inside it for a field: the date, the amount, the sender …
  import { onMount } from 'svelte';
  import { fade, scale } from 'svelte/transition';
  import { api } from '../lib/api.js';
  import { formatDate, formatAmount } from '../lib/format.js';
  import { motion } from '../lib/motion.js';
  import Icon from './Icon.svelte';

  let { docId, pages = 1, kind = 'date', updated = '', onpick, onclose } = $props();
  const what = { date: 'the date', amount: 'the amount', sender: 'the sender', title: 'the title', tags: 'a tag' };

  let page = $state(1);
  let words = $state([]);
  let loading = $state(true);
  let box = $state(null); // { x, y, w, h } as shares of the page
  let img = $state();
  let parsed = $state(null);
  let drag = null;

  async function loadWords() {
    loading = true;
    box = null;
    parsed = null;
    try { words = await api.get(`/api/docs/${docId}/words/${page}`); } catch { words = []; }
    loading = false;
  }
  onMount(() => {
    loadWords();
    const key = (e) => {
      if (e.key === 'Escape') onclose?.();
      if (e.key === 'Enter' && usable) use();
    };
    addEventListener('keydown', key);
    return () => removeEventListener('keydown', key);
  });

  const inside = (w, b) => {
    const cx = w.x + w.w / 2, cy = w.y + w.h / 2;
    return cx >= b.x && cx <= b.x + b.w && cy >= b.y && cy <= b.y + b.h;
  };
  let picked = $derived(box ? words.filter((w) => inside(w, box)) : []);

  // reading order: lines from top to bottom, words left to right
  function readText(ws) {
    const sorted = [...ws].sort((a, b) => a.y + a.h / 2 - (b.y + b.h / 2));
    const lines = [];
    for (const w of sorted) {
      const cy = w.y + w.h / 2;
      const line = lines.find((l) => Math.abs(l.cy - cy) < Math.max(l.h, w.h) * 0.6);
      if (line) line.ws.push(w);
      else lines.push({ cy, h: w.h, ws: [w] });
    }
    return lines.map((l) => l.ws.sort((a, b) => a.x - b.x).map((w) => w.t).join(' ')).join('\n');
  }
  let text = $derived(readText(picked));

  let timer;
  $effect(() => {
    const t = text;
    clearTimeout(timer);
    if (!t) { parsed = null; return; }
    timer = setTimeout(async () => {
      try { parsed = await api.post('/api/parse', { kind, text: t }); } catch { parsed = null; }
    }, 120);
  });

  let usable = $derived(parsed && parsed.value !== undefined && parsed.value !== '' && parsed.value !== null);
  let shown = $derived(!usable ? '' : kind === 'date' ? formatDate(parsed.value) : kind === 'amount' ? formatAmount(parsed.value) : parsed.value);

  function use() {
    if (!usable) return;
    onpick?.(parsed.value, text);
  }

  const pos = (e) => {
    const r = img.getBoundingClientRect();
    return { x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)), y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)) };
  };

  function down(e, mode = 'new') {
    if (e.button !== undefined && e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    const p = pos(e);
    drag = { mode, start: p, orig: box ? { ...box } : null };
    if (mode === 'new') box = { x: p.x, y: p.y, w: 0, h: 0 };
    e.currentTarget.setPointerCapture?.(e.pointerId);
  }
  function move(e) {
    if (!drag) return;
    const p = pos(e);
    const { mode, start, orig } = drag;
    if (mode === 'new') {
      box = { x: Math.min(start.x, p.x), y: Math.min(start.y, p.y), w: Math.abs(p.x - start.x), h: Math.abs(p.y - start.y) };
    } else if (mode === 'move') {
      const dx = p.x - start.x, dy = p.y - start.y;
      box = { ...orig, x: Math.min(1 - orig.w, Math.max(0, orig.x + dx)), y: Math.min(1 - orig.h, Math.max(0, orig.y + dy)) };
    } else {
      // a corner: the opposite corner stays put
      let x0 = orig.x, y0 = orig.y, x1 = orig.x + orig.w, y1 = orig.y + orig.h;
      if (mode.includes('w')) x0 = p.x; else x1 = p.x;
      if (mode.includes('n')) y0 = p.y; else y1 = p.y;
      box = { x: Math.min(x0, x1), y: Math.min(y0, y1), w: Math.abs(x1 - x0), h: Math.abs(y1 - y0) };
    }
  }
  function up() {
    if (drag && box && box.w < 0.005 && box.h < 0.005) {
      // a click: take the word under the pointer
      const hit = words.find((w) => box.x >= w.x && box.x <= w.x + w.w && box.y >= w.y && box.y <= w.y + w.h);
      box = hit ? { x: hit.x - 0.004, y: hit.y - 0.004, w: hit.w + 0.008, h: hit.h + 0.008 } : null;
    }
    drag = null;
  }
  function pickWord(w) {
    box = { x: w.x - 0.004, y: w.y - 0.004, w: w.w + 0.008, h: w.h + 0.008 };
  }
  function go(d) {
    page = Math.min(pages, Math.max(1, page + d));
    loadWords();
  }
</script>

<div class="scrim" role="presentation" transition:fade={motion({ duration: 150 })} onclick={onclose}></div>
<div class="marker card" role="dialog" aria-modal="true" aria-label="Mark {what[kind]} on the page" transition:scale={motion({ start: 0.97, duration: 180 })}>
  <header>
    <h2>Mark {what[kind]}</h2>
    {#if pages > 1}
      <div class="pager">
        <button type="button" class="btn small ghost iconbtn" onclick={() => go(-1)} disabled={page <= 1} aria-label="Previous page"><Icon name="back" /></button>
        <span class="small">Page {page} of {pages}</span>
        <button type="button" class="btn small ghost iconbtn flip" onclick={() => go(1)} disabled={page >= pages} aria-label="Next page"><Icon name="back" /></button>
      </div>
    {/if}
    <button type="button" class="btn ghost iconbtn" onclick={onclose} aria-label="Close"><Icon name="close" /></button>
  </header>
  <p class="help muted small">Drag a box around it on the page, or click a word. Drag the box or its corners to adjust.</p>

  <div class="stage">
    <div class="sheet" role="presentation" onpointerdown={(e) => down(e)} onpointermove={move} onpointerup={up} onpointercancel={up}>
      <img bind:this={img} src="/api/docs/{docId}/page/{page}?v={encodeURIComponent(updated)}" alt="Page {page}" draggable="false" />
      {#each words as w, i (i)}
        <button type="button" class="word" class:hit={box && inside(w, box)} tabindex="-1" aria-hidden="true"
          style="left:{w.x * 100}%;top:{w.y * 100}%;width:{w.w * 100}%;height:{w.h * 100}%"
          ondblclick={() => pickWord(w)}></button>
      {/each}
      {#if box}
        <div class="box" style="left:{box.x * 100}%;top:{box.y * 100}%;width:{box.w * 100}%;height:{box.h * 100}%"
          role="presentation" onpointerdown={(e) => down(e, 'move')}>
          {#each ['nw', 'ne', 'sw', 'se'] as c (c)}
            <span class="handle {c}" role="presentation" onpointerdown={(e) => down(e, c)}></span>
          {/each}
        </div>
      {/if}
      {#if loading}<div class="busy small">Reading the page …</div>{/if}
      {#if !loading && !words.length}<div class="busy small">This page has no text to pick from.</div>{/if}
    </div>
  </div>

  <footer>
    <div class="result" aria-live="polite">
      {#if text}
        <span class="muted small">Marked</span>
        <span class="marked">{text}</span>
        {#if kind === 'date' || kind === 'amount'}
          <span class="arrow" aria-hidden="true">→</span>
          {#if usable}<strong>{shown}</strong>{:else}<span class="error">no {kind} in it</span>{/if}
        {/if}
      {:else}
        <span class="muted small">Nothing marked yet.</span>
      {/if}
    </div>
    <button type="button" class="btn" onclick={onclose}>Cancel</button>
    <button type="button" class="btn primary" onclick={use} disabled={!usable}><Icon name="check" />Use it</button>
  </footer>
</div>

<style>
  .scrim { position: fixed; inset: 0; background: rgb(26 23 20 / 0.55); z-index: 60; }
  .marker { position: fixed; z-index: 61; inset: 3vh max(16px, calc(50vw - 560px)); display: flex; flex-direction: column; box-shadow: 0 20px 60px rgb(0 0 0 / 0.3); overflow: hidden; }
  header { display: flex; align-items: center; gap: 12px; padding: 12px 12px 0 18px; }
  header h2 { flex: 1; margin: 0; font-size: 1.1rem; }
  .pager { display: flex; align-items: center; gap: 4px; }
  .flip :global(svg) { transform: scaleX(-1); }
  .help { margin: 2px 18px 10px; }
  .stage { flex: 1; min-height: 0; background: var(--thumb); display: grid; place-items: center; padding: 14px; overflow: auto; }
  .sheet { position: relative; height: 100%; max-height: 100%; aspect-ratio: auto; touch-action: none; cursor: crosshair; user-select: none; display: inline-block; }
  .sheet img { display: block; height: 100%; max-width: 100%; object-fit: contain; background: #fff; box-shadow: var(--shadow); border-radius: 2px; pointer-events: none; }
  .word { position: absolute; border: 0; padding: 0; margin: 0; background: transparent; border-radius: 2px; cursor: crosshair; }
  .word:hover { background: rgb(242 179 61 / 0.18); }
  .word.hit { background: rgb(242 179 61 / 0.38); }
  .box { position: absolute; border: 2px solid #E0A82E; background: rgb(242 179 61 / 0.08); border-radius: 3px; cursor: move; }
  .handle { position: absolute; width: 14px; height: 14px; background: #fff; border: 2px solid #E0A82E; border-radius: 50%; }
  .handle.nw { left: -8px; top: -8px; cursor: nwse-resize; }
  .handle.ne { right: -8px; top: -8px; cursor: nesw-resize; }
  .handle.sw { left: -8px; bottom: -8px; cursor: nesw-resize; }
  .handle.se { right: -8px; bottom: -8px; cursor: nwse-resize; }
  .busy { position: absolute; left: 50%; top: 12px; transform: translateX(-50%); background: var(--card); padding: 6px 10px; border-radius: 8px; box-shadow: var(--shadow); }
  footer { display: flex; align-items: center; gap: 10px; padding: 12px 18px; border-top: 1px solid var(--line); }
  .result { flex: 1; min-width: 0; display: flex; align-items: baseline; gap: 8px; flex-wrap: wrap; }
  .marked { font-family: var(--mono); font-size: 0.85rem; white-space: pre-wrap; max-height: 3.2em; overflow: hidden; color: var(--ink-2); }
</style>
