<script>
  // Place a stamp ("Eingang", "Bezahlt") on a page and burn it into the PDF.
  import { onMount } from 'svelte';
  import { fade, scale } from 'svelte/transition';
  import { api } from '../lib/api.js';
  import { toast, user } from '../lib/stores.js';
  import { motion } from '../lib/motion.js';
  import Icon from './Icon.svelte';

  let { doc, kind = 'in', label = 'Eingang', onstamped, onclose } = $props();
  const color = kind === 'paid' ? '#B3261E' : '#1F4F9E';
  const today = new Date();
  const iso = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;

  let page = $state(1);
  let size = $state(null); // { w, h, stamp_w, stamp_ratio } in points, as shown
  let pos = $state({ x: 0.7, y: 0.04 });
  let date = $state(iso(today));
  let note = $state('');
  let busy = $state(false);
  let img = $state();
  let drag = null;

  // the stamp's size as shares of the page
  let sw = $derived(size ? size.stamp_w : 0.27);
  let sh = $derived(size ? (size.stamp_w * size.stamp_ratio * size.w) / size.h : 0.14);
  let printed = $derived(date ? date.split('-').reverse().join('.') : '');

  // a free spot: corners first, away from the text
  function place(words) {
    const m = 0.035;
    const spots = [
      { x: 1 - sw - m, y: m }, { x: 1 - sw - m, y: 0.12 }, { x: 1 - sw - m, y: 0.22 }, { x: m, y: m },
      { x: 1 - sw - m, y: 1 - sh - m }, { x: m, y: 1 - sh - m }, { x: (1 - sw) / 2, y: 1 - sh - m },
    ];
    const pad = 0.01;
    const free = (p) => !words.some((w) => w.x < p.x + sw + pad && w.x + w.w > p.x - pad && w.y < p.y + sh + pad && w.y + w.h > p.y - pad);
    return spots.find(free) || spots[0];
  }

  async function loadPage() {
    try {
      const [sz, words] = await Promise.all([api.get(`/api/docs/${doc.id}/pagesize/${page}`), api.get(`/api/docs/${doc.id}/words/${page}`).catch(() => [])]);
      size = sz;
      pos = place(words);
    } catch (e) { toast(e.message, 'error'); }
  }
  onMount(() => {
    loadPage();
    const key = (e) => e.key === 'Escape' && onclose?.();
    addEventListener('keydown', key);
    return () => removeEventListener('keydown', key);
  });

  function down(e) {
    e.preventDefault();
    const r = img.getBoundingClientRect();
    drag = { sx: e.clientX, sy: e.clientY, x: pos.x, y: pos.y, w: r.width, h: r.height };
    e.currentTarget.setPointerCapture(e.pointerId);
  }
  function move(e) {
    if (!drag) return;
    pos = {
      x: Math.min(1 - sw, Math.max(0, drag.x + (e.clientX - drag.sx) / drag.w)),
      y: Math.min(1 - sh, Math.max(0, drag.y + (e.clientY - drag.sy) / drag.h)),
    };
  }
  function up() { drag = null; }
  function nudge(e) {
    const d = e.shiftKey ? 0.05 : 0.01;
    const k = { ArrowLeft: [-d, 0], ArrowRight: [d, 0], ArrowUp: [0, -d], ArrowDown: [0, d] }[e.key];
    if (!k) return;
    e.preventDefault();
    pos = { x: Math.min(1 - sw, Math.max(0, pos.x + k[0])), y: Math.min(1 - sh, Math.max(0, pos.y + k[1])) };
  }

  async function stamp() {
    busy = true;
    try {
      const r = await api.post(`/api/docs/${doc.id}/stamp`, { kind, page, x: pos.x, y: pos.y, date, note });
      toast(`Stamped “${label}”`);
      onstamped?.(r);
    } catch (e) { toast(e.message, 'error', 6000); }
    busy = false;
  }
  // the dialog may sit inside a form (Review): Enter stamps instead of filing
  function enterStamps(e) {
    if (e.key === 'Enter') { e.preventDefault(); if (!busy && size && date) stamp(); }
  }
  function go(d) { page = Math.min(doc.pages || 1, Math.max(1, page + d)); loadPage(); }
</script>

<div class="scrim" role="presentation" transition:fade={motion({ duration: 150 })} onclick={onclose}></div>
<div class="dlg card" role="dialog" aria-modal="true" aria-label="Stamp “{label}”" transition:scale={motion({ start: 0.97, duration: 180 })}>
  <header>
    <h2>Stamp “{label}”</h2>
    {#if doc.pages > 1}
      <div class="pager">
        <button type="button" class="btn small ghost iconbtn" onclick={() => go(-1)} disabled={page <= 1} aria-label="Previous page"><Icon name="back" /></button>
        <span class="small">Page {page} of {doc.pages}</span>
        <button type="button" class="btn small ghost iconbtn flip" onclick={() => go(1)} disabled={page >= doc.pages} aria-label="Next page"><Icon name="back" /></button>
      </div>
    {/if}
    <button type="button" class="btn ghost iconbtn" onclick={onclose} aria-label="Close"><Icon name="close" /></button>
  </header>
  <div class="body">
    <div class="stage">
      <div class="sheet">
        <img bind:this={img} src="/api/docs/{doc.id}/page/{page}?v={encodeURIComponent(doc.updated_at)}" alt="Page {page}" draggable="false" />
        {#if size}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <div class="stamp" role="img" tabindex="0" aria-label="The stamp; drag it or move it with the arrow keys"
            style="left:{pos.x * 100}%;top:{pos.y * 100}%;width:{sw * 100}%;height:{sh * 100}%;--c:{color}"
            onpointerdown={down} onpointermove={move} onpointerup={up} onpointercancel={up} onkeydown={nudge}>
            <span class="l">{label.toUpperCase()}</span>
            <span class="d">{printed}</span>
            <span class="n">{note ? `${note} · ` : ''}{$user?.name}</span>
            <span class="h">SHA-256 ···· ···· ···· ····</span>
          </div>
        {/if}
      </div>
    </div>
    <aside class="opts">
      <label class="field">Date<input type="date" bind:value={date} required onkeydown={enterStamps} /></label>
      <label class="field">Note <span class="hint">optional, e.g. “per Überweisung”</span><input type="text" bind:value={note} maxlength="40" onkeydown={enterStamps} /></label>
      <p class="hint">Drag the stamp to its place. It goes into the PDF with the date, your name and the fingerprint (SHA-256) of the document as it is now. The version without stamps is kept, and the stamp's words become searchable.</p>
    </aside>
  </div>
  <footer>
    <button type="button" class="btn" onclick={onclose}>Cancel</button>
    <button type="button" class="btn primary" onclick={stamp} disabled={busy || !size || !date}><Icon name="check" />{busy ? 'Stamping …' : 'Stamp it'}</button>
  </footer>
</div>

<style>
  .scrim { position: fixed; inset: 0; background: rgb(26 23 20 / 0.55); z-index: 60; }
  .dlg { position: fixed; z-index: 61; inset: 3vh max(12px, calc(50vw - 600px)); display: flex; flex-direction: column; overflow: hidden; box-shadow: 0 20px 60px rgb(0 0 0 / 0.3); }
  @media (max-width: 760px) { .dlg { inset: 0; border-radius: 0; } }
  header { display: flex; align-items: center; gap: 12px; padding: 12px 12px 8px 18px; }
  header h2 { flex: 1; margin: 0; font-size: 1.1rem; }
  .pager { display: flex; align-items: center; gap: 4px; }
  .flip :global(svg) { transform: scaleX(-1); }
  .body { flex: 1; min-height: 0; display: grid; grid-template-columns: minmax(0, 1fr) 260px; }
  @media (max-width: 760px) { .body { grid-template-columns: 1fr; grid-template-rows: minmax(0, 1fr) auto; } }
  .stage { min-height: 0; background: var(--thumb); display: grid; place-items: center; padding: 14px; overflow: auto; }
  .sheet { position: relative; height: 100%; max-height: 100%; display: inline-block; touch-action: none; }
  .sheet img { display: block; height: 100%; max-width: 100%; object-fit: contain; background: #fff; box-shadow: var(--shadow); user-select: none; }
  .stamp { position: absolute; border: 2px solid var(--c); outline: 1px solid var(--c); outline-offset: -5px; color: var(--c); background: rgb(255 255 255 / 0.25);
    display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.1em; cursor: grab; container-type: inline-size; font-family: Helvetica, Arial, sans-serif; }
  .stamp:active { cursor: grabbing; }
  .stamp:focus-visible { box-shadow: 0 0 0 3px var(--accent); }
  .stamp .l { font-weight: 700; font-size: 13cqw; line-height: 1; }
  .stamp .d { font-weight: 700; font-size: 7cqw; border-top: 1px solid var(--c); padding-top: 0.15em; width: 80%; text-align: center; }
  .stamp .n { font-size: 5cqw; }
  .stamp .h { font-family: Courier, monospace; font-size: 3.6cqw; opacity: 0.8; }
  .opts { padding: 14px 18px; display: flex; flex-direction: column; gap: 12px; border-left: 1px solid var(--line); overflow: auto; }
  @media (max-width: 760px) { .opts { border-left: 0; border-top: 1px solid var(--line); } }
  footer { display: flex; justify-content: flex-end; gap: 10px; padding: 12px 18px; border-top: 1px solid var(--line); }
</style>
