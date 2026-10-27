<script>
  // Change the pages of a document afterwards: order, turn, remove, crop.
  import { flip } from 'svelte/animate';
  import { fade, scale } from 'svelte/transition';
  import { upload } from '../lib/api.js';
  import { toast } from '../lib/stores.js';
  import { fullQuad, outputSize, warp } from '../lib/scan.js';
  import { motion } from '../lib/motion.js';
  import Icon from './Icon.svelte';

  let { doc, onsaved, onclose } = $props();
  // one entry per page as it will be: where it comes from, how it's turned,
  // and a new picture when it was cropped
  let items = $state(Array.from({ length: Math.max(doc.pages, 1) }, (_, i) => ({ key: `p${i + 1}`, src: i + 1, rotate: 0, blob: null, url: '' })));
  let saving = $state(false);
  let cropping = $state(null); // { index, canvas, quad, url, w, h }
  let cropBox = $state();
  let dragIdx = -1;
  let dragOver = $state(-1);
  let corner = -1;

  const imgUrl = (it) => it.url || `/api/docs/${doc.id}/page/${it.src}?v=${encodeURIComponent(doc.updated_at)}`;
  let changed = $derived(items.length !== doc.pages || items.some((it, i) => it.src !== i + 1 || it.rotate || it.blob));

  function moveBy(i, d) {
    const j = i + d;
    if (j < 0 || j >= items.length) return;
    const next = [...items];
    [next[i], next[j]] = [next[j], next[i]];
    items = next;
  }
  function turn(i) { items[i].rotate = (items[i].rotate + 90) % 360; }
  function remove(i) {
    if (items.length === 1) return toast('A document needs at least one page.', 'error');
    items = items.filter((_, j) => j !== i);
  }

  // drag and drop on the desktop
  function dragStart(i, e) { dragIdx = i; e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', String(i)); }
  function dragEnter(i) { if (dragIdx >= 0) dragOver = i; }
  function drop(i, e) {
    e.preventDefault();
    if (dragIdx < 0 || dragIdx === i) { dragOver = -1; return; }
    const next = [...items];
    const [it] = next.splice(dragIdx, 1);
    next.splice(i, 0, it);
    items = next;
    dragIdx = -1; dragOver = -1;
  }

  // cropping: the page as a big picture with four corners to drag
  async function startCrop(i) {
    const it = items[i];
    const im = new Image();
    im.src = it.url || `/api/docs/${doc.id}/page/${it.src}?w=2400&v=${encodeURIComponent(doc.updated_at)}`;
    try { await im.decode(); } catch { return toast('The page could not be loaded.', 'error'); }
    let c = document.createElement('canvas');
    c.width = im.naturalWidth; c.height = im.naturalHeight;
    c.getContext('2d').drawImage(im, 0, 0);
    for (let r = it.blob ? 0 : it.rotate; r > 0; r -= 90) c = rotated(c); // crop what you see
    cropping = { index: i, canvas: c, quad: fullQuad(0.02), url: c.toDataURL('image/jpeg', 0.85), w: 0, h: 0 };
    requestAnimationFrame(fitCrop);
  }
  function rotated(src) {
    const c = document.createElement('canvas');
    c.width = src.height; c.height = src.width;
    const ctx = c.getContext('2d');
    ctx.translate(c.width, 0);
    ctx.rotate(Math.PI / 2);
    ctx.drawImage(src, 0, 0);
    return c;
  }
  function fitCrop() {
    if (!cropping || !cropBox) return;
    const s = Math.min(cropBox.clientWidth / cropping.canvas.width, cropBox.clientHeight / cropping.canvas.height);
    cropping.w = Math.round(cropping.canvas.width * s);
    cropping.h = Math.round(cropping.canvas.height * s);
  }
  function cornerDown(i, e) { corner = i; e.currentTarget.setPointerCapture(e.pointerId); e.preventDefault(); }
  function cornerMove(e) {
    if (corner < 0) return;
    const r = e.currentTarget.getBoundingClientRect();
    cropping.quad[corner] = { x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)), y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)) };
  }
  async function applyCrop() {
    const { canvas, quad, index } = cropping;
    const W = canvas.width, H = canvas.height;
    const px = quad.map((p) => ({ x: p.x * (W - 1), y: p.y * (H - 1) }));
    const { w, h } = outputSize(px, 2400);
    const out = warp(canvas.getContext('2d', { willReadFrequently: true }).getImageData(0, 0, W, H).data, W, H, px, w, h);
    const oc = document.createElement('canvas');
    oc.width = w; oc.height = h;
    oc.getContext('2d').putImageData(new ImageData(out, w, h), 0, 0);
    const blob = await new Promise((r) => oc.toBlob(r, 'image/jpeg', 0.85));
    const it = items[index];
    if (it.url) URL.revokeObjectURL(it.url);
    items[index] = { ...it, blob, url: URL.createObjectURL(blob), rotate: 0 };
    cropping = null;
  }

  async function save() {
    saving = true;
    const fd = new FormData();
    const plan = items.map((it, i) => {
      if (it.blob) fd.append(`img${i}`, it.blob, `page-${i + 1}.jpg`);
      return { src: it.blob ? 0 : it.src, rotate: it.rotate, replace: it.blob ? `img${i}` : '' };
    });
    fd.append('plan', JSON.stringify(plan));
    try {
      const d = await upload(`/api/docs/${doc.id}/pages`, fd);
      toast('Pages saved');
      onsaved?.(d);
    } catch (e) {
      toast(e.message, 'error', 6000);
    } finally {
      saving = false;
    }
  }
  let cropPolygon = $derived(cropping ? cropping.quad.map((p) => `${p.x * cropping.w},${p.y * cropping.h}`).join(' ') : '');
</script>

<svelte:window onresize={fitCrop} />

<div class="scrim" role="presentation" transition:fade={motion({ duration: 150 })}></div>
<div class="editor card" role="dialog" aria-modal="true" aria-label="Edit pages" transition:scale={motion({ start: 0.97, duration: 180 })}>
  {#if cropping}
    <header><h2>Crop page {cropping.index + 1}</h2><button class="btn ghost iconbtn" onclick={() => (cropping = null)} aria-label="Back to the pages"><Icon name="close" /></button></header>
    <p class="muted small help">Drag the four corners onto the corners of the page. Shoebox straightens it.</p>
    <div class="cropstage" bind:this={cropBox}>
      <div class="frame" style="width:{cropping.w}px;height:{cropping.h}px">
        <img src={cropping.url} alt="Page {cropping.index + 1}, with handles at the corners" draggable="false" />
        <svg viewBox="0 0 {cropping.w} {cropping.h}" onpointermove={cornerMove} onpointerup={() => (corner = -1)} role="application" aria-label="Corners of the page">
          <polygon points={cropPolygon} />
          {#each cropping.quad as p, i (i)}
            <circle cx={p.x * cropping.w} cy={p.y * cropping.h} r="14" onpointerdown={(e) => cornerDown(i, e)} role="presentation" />
          {/each}
        </svg>
      </div>
    </div>
    <footer><button class="btn" onclick={() => (cropping = null)}>Cancel</button><button class="btn primary" onclick={applyCrop}><Icon name="crop" />Crop</button></footer>
  {:else}
    <header><h2>Edit pages</h2><button class="btn ghost iconbtn" onclick={onclose} aria-label="Close"><Icon name="close" /></button></header>
    <p class="muted small help">Drag pages into a new order, or use the arrows. Changes are saved into the PDF; the first version is kept on the server.</p>
    <ol class="grid">
      {#each items as it, i (it.key)}
        <li class="tile" class:over={dragOver === i} animate:flip={motion({ duration: 220 })} draggable="true"
          ondragstart={(e) => dragStart(i, e)} ondragenter={() => dragEnter(i)} ondragover={(e) => e.preventDefault()} ondrop={(e) => drop(i, e)} ondragend={() => { dragIdx = -1; dragOver = -1; }}>
          <div class="pic"><img src={imgUrl(it)} alt="Page {i + 1}" style="transform: rotate({it.rotate}deg)" draggable="false" /></div>
          <div class="tools">
            <span class="num">{i + 1}</span>
            <button class="btn small ghost iconbtn" onclick={() => moveBy(i, -1)} disabled={i === 0} aria-label="Move page {i + 1} earlier"><Icon name="back" size={16} /></button>
            <button class="btn small ghost iconbtn flip" onclick={() => moveBy(i, 1)} disabled={i === items.length - 1} aria-label="Move page {i + 1} later"><Icon name="back" size={16} /></button>
            <button class="btn small ghost iconbtn" onclick={() => turn(i)} aria-label="Turn page {i + 1}"><Icon name="rotate" size={16} /></button>
            <button class="btn small ghost iconbtn" onclick={() => startCrop(i)} aria-label="Crop page {i + 1}"><Icon name="crop" size={16} /></button>
            <button class="btn small ghost iconbtn danger" onclick={() => remove(i)} aria-label="Remove page {i + 1}"><Icon name="trash" size={16} /></button>
          </div>
        </li>
      {/each}
    </ol>
    <footer>
      <span class="muted small grow">{items.length} page{items.length === 1 ? '' : 's'}{changed ? ' · not saved yet' : ''}</span>
      <button class="btn" onclick={onclose}>Cancel</button>
      <button class="btn primary" onclick={save} disabled={!changed || saving}><Icon name="check" />{saving ? 'Saving …' : 'Save pages'}</button>
    </footer>
  {/if}
</div>

<style>
  .scrim { position: fixed; inset: 0; background: rgb(26 23 20 / 0.55); z-index: 60; }
  .editor { position: fixed; z-index: 61; inset: 3vh max(12px, calc(50vw - 560px)); display: flex; flex-direction: column; overflow: hidden; box-shadow: 0 20px 60px rgb(0 0 0 / 0.3); }
  @media (max-width: 640px) { .editor { inset: 0; border-radius: 0; } }
  header { display: flex; align-items: center; padding: 12px 12px 0 18px; }
  header h2 { flex: 1; margin: 0; font-size: 1.1rem; }
  .help { margin: 2px 18px 10px; }
  .grid { list-style: none; margin: 0; padding: 14px; flex: 1; min-height: 0; overflow: auto; display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 14px; align-content: start; background: var(--thumb); }
  .tile { background: var(--card); border: 1px solid var(--line); border-radius: 10px; padding: 8px; display: flex; flex-direction: column; gap: 6px; cursor: grab; }
  .tile.over { outline: 2px dashed var(--accent); outline-offset: 2px; }
  .pic { aspect-ratio: 210 / 297; display: grid; place-items: center; overflow: hidden; background: #fff; border-radius: 4px; }
  .pic img { max-width: 100%; max-height: 100%; transition: transform 0.2s; }
  .tools { display: flex; align-items: center; gap: 0; flex-wrap: wrap; }
  .tools .iconbtn { width: 30px; min-height: 30px; padding: 0; }
  .num { font-weight: 700; font-size: 0.85rem; width: 22px; text-align: center; }
  .flip :global(svg) { transform: scaleX(-1); }
  footer { display: flex; align-items: center; gap: 10px; padding: 12px 18px; border-top: 1px solid var(--line); }
  .cropstage { flex: 1; min-height: 0; display: grid; place-items: center; background: #2B2620; padding: 18px; }
  .frame { position: relative; }
  .frame img, .frame svg { position: absolute; inset: 0; width: 100%; height: 100%; }
  .frame svg { touch-action: none; }
  polygon { fill: rgb(242 179 61 / 0.12); stroke: #F2B33D; stroke-width: 2.5; }
  circle { fill: rgb(255 255 255 / 0.9); stroke: #F2B33D; stroke-width: 3; cursor: grab; }
</style>
