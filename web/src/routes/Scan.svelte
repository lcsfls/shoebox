<script>
  import { onMount, tick } from 'svelte';
  import { upload } from '../lib/api.js';
  import { toast, counts, libraries, intakeLibrary, showIntake } from '../lib/stores.js';
  import { navigate } from '../lib/router.js';
  import { detectQuad, fullQuad, sharpness, toGray, autoShot } from '../lib/scan.js';
  import { straighten } from '../lib/straighten.js';
  import Icon from '../components/Icon.svelte';

  const MAX_SIDE = 3400; // photos are scaled down to this before straightening

  let mode = $state('start'); // start | camera | crop | busy
  let liveCamera = $state(false);
  let cameraError = $state('');
  // { key, quad, filter, src (the photo, for adjusting the corners later), blob, url, busy, error }
  let pages = $state([]);
  let pageKey = 0;
  const pending = new Map(); // key → the page being straightened
  let editing = $state(-1); // the page whose corners are being adjusted again, -1: a new one
  let filter = $state(readFilter());
  let video = $state();
  let viewport = $state();
  let frame = $state({ w: 0, h: 0 });
  let liveQuad = $state(null);
  let shot = $state(null); // { canvas, quad }
  let cropBox = $state();
  let cropView = $state({ w: 0, h: 0, url: '' });
  let dragging = -1;
  let stream;
  let loop;
  let fileInput = $state();
  let galleryInput = $state();
  let sending = $state(0);
  let torchAvailable = $state(false); // the phone's light, where the browser allows it (Chrome on Android)
  let torch = $state(false);
  let videoReady = $state(false); // the shutter waits for the picture (the video is reattached after each page)
  let auto = $state(readAuto()); // take the photo by itself once the page is sharp and still
  let look = $state('none'); // none | taken | steady | ready (what the live outline shows)
  let preview = $state(-1); // the scanned page shown large, -1 for none
  const shooter = autoShot();

  const canCamera = typeof navigator !== 'undefined' && !!navigator.mediaDevices?.getUserMedia && window.isSecureContext;

  function readFilter() {
    try { return localStorage.getItem('sb-filter') || 'doc'; } catch { return 'doc'; }
  }
  $effect(() => { try { localStorage.setItem('sb-filter', filter); } catch {} });
  function readAuto() {
    try { return localStorage.getItem('sb-autoshot') !== 'off'; } catch { return true; }
  }
  $effect(() => { try { localStorage.setItem('sb-autoshot', auto ? 'on' : 'off'); } catch {} });

  onMount(() => {
    if (canCamera) startCamera();
    else mode = 'start';
    const onResize = () => { fitFrame(); fitCrop(); };
    addEventListener('resize', onResize);
    return () => {
      stopCamera();
      removeEventListener('resize', onResize);
      pages.forEach((p) => p.url && URL.revokeObjectURL(p.url));
      if (cropView.url) URL.revokeObjectURL(cropView.url);
    };
  });

  async function startCamera() {
    cameraError = '';
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: false,
        video: { facingMode: { ideal: 'environment' }, width: { ideal: 3840 }, height: { ideal: 2160 } },
      });
      // keep focusing while the phone moves, where the camera allows it
      const t0 = stream.getVideoTracks()[0];
      if (t0?.getCapabilities?.().focusMode?.includes('continuous')) t0.applyConstraints({ advanced: [{ focusMode: 'continuous' }] }).catch(() => {});
      liveCamera = true;
      const track = stream.getVideoTracks()[0];
      torchAvailable = !!track?.getCapabilities?.().torch;
      await resumeCamera();
    } catch (e) {
      liveCamera = false;
      mode = 'start';
      cameraError = e?.name === 'NotAllowedError' ? 'Camera access was refused. Allow it in the browser settings, or take a photo with the camera app below.' : 'The camera could not be started. Take a photo with the camera app instead.';
    }
  }

  // (re)attach the stream: the video element is recreated after cropping
  // again: the same page is taken once more (Retake), so it may go by itself
  async function resumeCamera(again = false) {
    shot = null;
    mode = 'camera';
    shooter.reset(again);
    videoReady = false;
    await tick();
    if (video && stream) {
      video.srcObject = stream;
      await video.play().catch(() => {});
    }
    videoReady = !!video?.videoWidth;
    keepTorch();
    fitFrame();
    detectLoop();
  }

  // The light: phones switch it off after a photo or when the video is
  // attached again, so it is switched on again, and the button shows what
  // the camera really does.
  const track0 = () => stream?.getVideoTracks()[0];
  async function setTorch(on) {
    const t = track0();
    if (!t) return;
    try {
      await t.applyConstraints({ advanced: [{ torch: on }] });
    } catch {
      torchAvailable = false;
      return;
    }
    torch = t.getSettings?.().torch ?? on;
  }
  function toggleTorch() {
    return setTorch(!(track0()?.getSettings?.().torch ?? torch));
  }
  function keepTorch() {
    if (torch && track0()?.getSettings?.().torch !== true) setTorch(true); // also where the browser doesn't tell
  }

  function stopCamera() {
    clearTimeout(loop);
    stream?.getTracks().forEach((t) => t.stop());
    stream = null;
  }

  // the visible video area, so the outline sits exactly on the picture
  function fitFrame() {
    if (!video?.videoWidth || !viewport) return;
    const cw = viewport.clientWidth, ch = viewport.clientHeight;
    const s = Math.min(cw / video.videoWidth, ch / video.videoHeight);
    frame = { w: Math.round(video.videoWidth * s), h: Math.round(video.videoHeight * s) };
  }

  const small = typeof document !== 'undefined' ? document.createElement('canvas') : null;
  const probe = typeof document !== 'undefined' ? document.createElement('canvas') : null;
  let lastGray = null;
  function detectLoop() {
    clearTimeout(loop);
    if (mode !== 'camera' || !video?.videoWidth) { loop = setTimeout(detectLoop, 300); return; }
    const w = 180, h = Math.round((180 * video.videoHeight) / video.videoWidth);
    small.width = w; small.height = h;
    const ctx = small.getContext('2d', { willReadFrequently: true });
    ctx.drawImage(video, 0, 0, w, h);
    const px = ctx.getImageData(0, 0, w, h).data;
    liveQuad = detectQuad(px, w, h);
    lastGray = toGray(px, w, h);
    if (preview >= 0) shooter.reset(); // start over once the page view is closed
    look = shooter.step(liveQuad, liveQuad ? pageSharpness(liveQuad) : 0, lastGray);
    if (look === 'shoot') {
      look = 'ready';
      if (auto && preview < 0 && !capturing && !sending) { capture(); return; }
    }
    loop = setTimeout(detectLoop, 280);
  }

  // sharpness of the page itself (not the table around it), in a finer
  // picture than the one used to find the page, so small print counts
  function pageSharpness(q) {
    const xs = q.map((p) => p.x), ys = q.map((p) => p.y);
    const vw = video.videoWidth, vh = video.videoHeight;
    const x0 = Math.min(...xs) * vw, y0 = Math.min(...ys) * vh;
    const bw = (Math.max(...xs) - Math.min(...xs)) * vw, bh = (Math.max(...ys) - Math.min(...ys)) * vh;
    // the middle of the page: its edges are the table's, not the print's
    const sx = x0 + bw * 0.2, sy = y0 + bh * 0.2, sw = bw * 0.6, sh = bh * 0.6;
    if (sw < 8 || sh < 8) return 0;
    const pw = Math.min(400, Math.round(sw)), ph = Math.max(8, Math.round((pw * sh) / sw));
    probe.width = pw; probe.height = ph;
    const pctx = probe.getContext('2d', { willReadFrequently: true });
    pctx.drawImage(video, sx, sy, sw, sh, 0, 0, pw, ph);
    return sharpness(pctx.getImageData(0, 0, pw, ph).data, pw, ph);
  }

  let capturing = $state(false);

  // A sharp picture: a real photo from the camera (full resolution, focused)
  // where the browser offers it, otherwise the sharpest of the next frames
  // of the video, so a shaking hand doesn't blur the page.
  async function capture() {
    if (!video?.videoWidth || capturing) return;
    capturing = true;
    clearTimeout(loop);
    const quad = liveQuad;
    shooter.taken(lastGray);
    try {
      const photo = await takePhoto();
      if (photo) { openCrop(photo, autoQuad(photo)); return; } // the photo may have another shape than the video
      openCrop(await sharpestFrame(), quad);
    } finally {
      capturing = false;
      keepTorch();
      if (mode === 'camera') detectLoop();
    }
  }

  // Is a real photo bigger than the video picture? Asked once: taking one
  // stalls the preview on many phones, so it is only done when it pays.
  let photoBigger = null;
  async function photoPays(track) {
    if (photoBigger !== null) return photoBigger;
    photoBigger = false;
    const side = Math.max(video.videoWidth, video.videoHeight);
    if (side >= 3000) return false; // 4K video: its frames are enough
    try {
      const caps = await new ImageCapture(track).getPhotoCapabilities();
      photoBigger = (caps.imageWidth?.max || 0) > side * 1.2;
    } catch {}
    return photoBigger;
  }

  async function takePhoto() {
    const track = track0();
    if (!track || typeof ImageCapture === 'undefined' || !(await photoPays(track))) return null;
    try {
      const ic = new ImageCapture(track);
      const blob = await Promise.race([
        ic.takePhoto(torch ? { fillLightMode: 'off' } : {}),
        new Promise((_, no) => setTimeout(() => no(new Error('slow')), 3000)),
      ]);
      const bmp = await createImageBitmap(blob);
      if (Math.max(bmp.width, bmp.height) <= Math.max(video.videoWidth, video.videoHeight) * 1.2) return null;
      const s = Math.min(1, MAX_SIDE / Math.max(bmp.width, bmp.height));
      const c = document.createElement('canvas');
      c.width = Math.round(bmp.width * s);
      c.height = Math.round(bmp.height * s);
      c.getContext('2d').drawImage(bmp, 0, 0, c.width, c.height);
      return c;
    } catch {
      return null;
    }
  }

  async function sharpestFrame() {
    const vw = video.videoWidth, vh = video.videoHeight;
    const s = Math.min(1, MAX_SIDE / Math.max(vw, vh));
    let bestScore = -1;
    // each frame is judged small; only a sharper one is copied in full
    const best = document.createElement('canvas');
    best.width = Math.round(vw * s);
    best.height = Math.round(vh * s);
    const bctx = best.getContext('2d');
    const probe = document.createElement('canvas');
    probe.width = 320; probe.height = Math.round((320 * vh) / vw);
    const pctx = probe.getContext('2d', { willReadFrequently: true });
    for (let i = 0; i < 6; i++) {
      pctx.drawImage(video, 0, 0, probe.width, probe.height);
      const score = sharpness(pctx.getImageData(0, 0, probe.width, probe.height).data, probe.width, probe.height);
      if (score > bestScore) { bestScore = score; bctx.drawImage(video, 0, 0, best.width, best.height); }
      await new Promise((r) => setTimeout(r, 70));
    }
    return best;
  }

  // photo from the camera app or the gallery (also the way without HTTPS)
  async function onPhoto(e) {
    const files = [...e.target.files];
    e.target.value = '';
    for (const file of files) {
      const bmp = await createImageBitmap(file).catch(() => null);
      if (!bmp) { toast('That image could not be read.', 'error'); continue; }
      const s = Math.min(1, MAX_SIDE / Math.max(bmp.width, bmp.height));
      const c = document.createElement('canvas');
      c.width = Math.round(bmp.width * s);
      c.height = Math.round(bmp.height * s);
      c.getContext('2d').drawImage(bmp, 0, 0, c.width, c.height);
      if (files.length > 1) {
        addPage(c, autoQuad(c), filter);
      } else {
        openCrop(c, autoQuad(c));
      }
    }
  }

  function autoQuad(c) {
    const w = 180, h = Math.round((180 * c.height) / c.width);
    small.width = w; small.height = h;
    const ctx = small.getContext('2d', { willReadFrequently: true });
    ctx.drawImage(c, 0, 0, w, h);
    return detectQuad(ctx.getImageData(0, 0, w, h).data, w, h);
  }

  function openCrop(canvas, quad) {
    shot = { canvas, quad: (quad || fullQuad()).map((p) => ({ ...p })) };
    mode = 'crop';
    requestAnimationFrame(fitCrop);
    // encoded on the side, not in the way of the tap (toDataURL stalls)
    canvas.toBlob((b) => {
      if (!b || shot?.canvas !== canvas) return;
      if (cropView.url) URL.revokeObjectURL(cropView.url);
      cropView = { ...cropView, url: URL.createObjectURL(b) };
    }, 'image/jpeg', 0.8);
  }

  function fitCrop() {
    if (!shot || !cropBox) return;
    const cw = cropBox.clientWidth, ch = cropBox.clientHeight;
    const s = Math.min(cw / shot.canvas.width, ch / shot.canvas.height);
    cropView = { ...cropView, w: Math.round(shot.canvas.width * s), h: Math.round(shot.canvas.height * s) };
  }

  function rotate() {
    const src = shot.canvas;
    const c = document.createElement('canvas');
    c.width = src.height; c.height = src.width;
    const ctx = c.getContext('2d');
    ctx.translate(c.width, 0);
    ctx.rotate(Math.PI / 2);
    ctx.drawImage(src, 0, 0);
    // corners turn with the picture
    const q = shot.quad.map((p) => ({ x: 1 - p.y, y: p.x }));
    openCrop(c, [q[3], q[0], q[1], q[2]]);
  }

  function handleDown(i, e) {
    dragging = i;
    e.currentTarget.setPointerCapture(e.pointerId);
    e.preventDefault();
  }
  function handleMove(e) {
    if (dragging < 0) return;
    const r = e.currentTarget.getBoundingClientRect();
    const x = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
    const y = Math.min(1, Math.max(0, (e.clientY - r.top) / r.height));
    shot.quad[dragging] = { x, y };
  }
  function handleUp() { dragging = -1; }

  // The page is straightened in the background: the camera is back at once
  // for the next one.
  async function usePage() {
    const { canvas, quad } = shot;
    const back = editing;
    if (editing >= 0) {
      const p = pages[editing];
      editing = -1;
      if (p) process(p, canvas, quad, filter);
    } else {
      addPage(canvas, quad, filter);
    }
    if (liveCamera) await resumeCamera();
    else { shot = null; mode = 'start'; }
    if (back >= 0) preview = back;
  }

  function addPage(canvas, quad, look) {
    const page = { key: ++pageKey, quad: null, filter: look, src: null, blob: null, url: '', busy: true, error: '' };
    pages = [...pages, page];
    process(pages[pages.length - 1], canvas, quad, look);
  }

  function process(page, canvas, quad, look) {
    quad = (quad || fullQuad(0)).map((p) => ({ ...p }));
    Object.assign(page, { quad, filter: look, busy: true, error: '' });
    const key = page.key;
    const find = () => pages.find((p) => p.key === key);
    const job = (async () => {
      const src = new Promise((r) => canvas.toBlob(r, 'image/jpeg', 0.92)); // kept for adjusting the corners later
      try {
        const blob = await straighten(canvas, quad, look);
        const p = find();
        if (p) {
          if (p.url) URL.revokeObjectURL(p.url);
          Object.assign(p, { blob, url: URL.createObjectURL(blob), busy: false });
        }
      } catch (e) {
        const p = find();
        if (p) Object.assign(p, { busy: false, error: e.message || 'failed' });
        toast('A page could not be straightened. Take it again.', 'error');
      }
      const p = find();
      if (p) p.src = await src;
    })();
    pending.set(key, job);
    job.finally(() => pending.get(key) === job && pending.delete(key));
  }

  // adjusting the corners of a page that was already taken
  async function adjust(i) {
    const p = pages[i];
    if (!p?.src) return;
    const bmp = await createImageBitmap(p.src);
    const c = document.createElement('canvas');
    c.width = bmp.width;
    c.height = bmp.height;
    c.getContext('2d').drawImage(bmp, 0, 0);
    bmp.close?.();
    clearTimeout(loop);
    editing = i;
    filter = p.filter;
    preview = -1;
    openCrop(c, p.quad);
  }
  function cancelAdjust() {
    const back = editing;
    editing = -1;
    if (liveCamera) resumeCamera().then(() => (preview = back));
    else { shot = null; mode = 'start'; preview = back; }
  }

  function removePage(i) {
    if (pages[i].url) URL.revokeObjectURL(pages[i].url);
    pages = pages.filter((_, j) => j !== i);
    preview = pages.length ? Math.min(i, pages.length - 1) : -1;
  }

  function onPreviewKey(e) {
    if (preview < 0) return;
    if (e.key === 'Escape') preview = -1;
    else if (e.key === 'ArrowLeft' && preview > 0) preview--;
    else if (e.key === 'ArrowRight' && preview < pages.length - 1) preview++;
  }

  async function done() {
    if (!pages.length) return;
    sending = 0.01;
    await Promise.all([...pending.values()]); // pages still being straightened
    sending = 0;
    if (pages.some((p) => p.error || !p.blob)) { toast('A page failed. Remove it and take it again.', 'error', 6000); return; }
    const fd = new FormData();
    pages.forEach((p, i) => fd.append('pages', p.blob, `page-${i + 1}.jpg`));
    sending = 0.01;
    try {
      const r = await upload('/api/scan', fd, (p) => (sending = Math.max(p, 0.01)));
      showIntake(r.library);
      counts.update((c) => ({ ...c, busy: c.busy + 1 }));
      toast(`Scan sent (${pages.length} page${pages.length === 1 ? '' : 's'}). Reading the text …`);
      stopCamera();
      navigate('/');
    } catch (e) {
      toast(e.message, 'error', 6000);
    } finally {
      sending = 0;
    }
  }

  function close() {
    if (preview >= 0) { preview = -1; return; }
    if (pages.length && !confirm('Discard the scanned pages?')) return;
    stopCamera();
    navigate('/');
  }

  let polygon = $derived(liveQuad ? liveQuad.map((p) => `${p.x * frame.w},${p.y * frame.h}`).join(' ') : '');
  let cropPolygon = $derived(shot ? shot.quad.map((p) => `${p.x * cropView.w},${p.y * cropView.h}`).join(' ') : '');
</script>

<svelte:window onkeydown={onPreviewKey} />

<div class="scan">
  <header class="bar">
    <button class="round" onclick={close} aria-label="Close the scanner"><Icon name="close" /></button>
    <span class="title">{mode === 'crop' ? 'Adjust the corners' + (editing >= 0 ? ` of page ${editing + 1}` : '') : `Page ${pages.length + 1}`}{#if $libraries.length > 1}<span class="into">into {$intakeLibrary?.name}</span>{/if}</span>
    {#if torchAvailable && mode === 'camera'}
      <button class="round" class:on={torch} onclick={toggleTorch} aria-pressed={torch} aria-label={torch ? 'Turn the light off' : 'Turn the light on'}><Icon name="flash" /></button>
    {:else}
      <span class="round placeholder" aria-hidden="true"></span>
    {/if}
  </header>

  {#if mode === 'camera' || (mode === 'busy' && !shot)}
    <div class="viewport" bind:this={viewport}>
      <div class="frame" style="width:{frame.w}px;height:{frame.h}px">
        <video bind:this={video} playsinline muted onloadedmetadata={() => { fitFrame(); videoReady = true; }}></video>
        {#if polygon}
          <svg class="overlay" class:ready={look === 'ready'} viewBox="0 0 {frame.w} {frame.h}" aria-hidden="true"><polygon points={polygon} /></svg>
        {/if}
      </div>
      {#if mode === 'camera'}
        <button class="auto-chip" class:on={auto} onclick={() => (auto = !auto)} aria-pressed={auto} aria-label="Take the photo automatically">
          <span class="dot"></span>Auto
        </button>
      {/if}
      <p class="hint-pill" class:ready={look === 'ready'}>{capturing ? 'Taking the photo …'
        : !liveQuad ? 'Point the camera at the page, on a darker surface.'
        : look === 'taken' && auto ? 'Got it. Lay down the next page.'
        : look === 'ready' ? (auto ? 'Sharp. Hold still …' : 'Sharp. Take the photo now.')
        : auto ? 'Page found. Hold still.' : 'Page found. Hold still and take the photo.'}</p>
    </div>
  {:else if mode === 'crop' || (mode === 'busy' && shot)}
    <div class="viewport" bind:this={cropBox}>
      <div class="frame" style="width:{cropView.w}px;height:{cropView.h}px">
        <img src={cropView.url} alt="Your page, with handles at the corners" draggable="false" />
        <svg class="overlay edit" viewBox="0 0 {cropView.w} {cropView.h}" role="application" aria-label="Drag the four corners onto the corners of the page"
          onpointermove={handleMove} onpointerup={handleUp} onpointercancel={handleUp}>
          <polygon points={cropPolygon} />
          {#each shot?.quad ?? [] as p, i}
            <circle class="handle" cx={p.x * cropView.w} cy={p.y * cropView.h} r="18" onpointerdown={(e) => handleDown(i, e)} role="slider" aria-label="Corner {i + 1}" aria-valuenow={Math.round(p.x * 100)} tabindex="-1" />
          {/each}
        </svg>
      </div>
      {#if mode === 'busy'}<p class="hint-pill">Straightening …</p>{/if}
    </div>
  {:else}
    <div class="viewport fallback">
      <div class="fallback-box">
        <Icon name="scan" size={44} />
        {#if cameraError}<p>{cameraError}</p>
        {:else if !canCamera}<p>The live camera needs HTTPS. Take the photo with your camera app; Shoebox still finds and straightens the page.</p>{/if}
        <button class="btn primary big" onclick={() => fileInput.click()}><Icon name="scan" />Take a photo</button>
        <button class="btn big" onclick={() => galleryInput.click()}>Choose from the gallery</button>
        {#if canCamera && cameraError}<button class="btn ghost" onclick={startCamera}>Try the camera again</button>{/if}
      </div>
    </div>
  {/if}

  <input bind:this={fileInput} type="file" accept="image/*" capture="environment" hidden onchange={onPhoto} />
  <input bind:this={galleryInput} type="file" accept="image/*" multiple hidden onchange={onPhoto} />

  {#if pages.length && mode !== 'crop'}
    <div class="strip" aria-label="Scanned pages">
      {#each pages as p, i (p.key)}
        <button class="pg" class:failed={p.error} onclick={() => (preview = i)} aria-label="Show page {i + 1}" aria-busy={p.busy}>
          {#if p.url}<img src={p.url} alt="" />{:else}<span class="spin" aria-hidden="true"></span>{/if}<span class="num">{i + 1}</span>
        </button>
      {/each}
      <span class="muted strip-hint">Tap a page to look at it</span>
    </div>
  {/if}

  {#if preview >= 0 && pages[preview]}
    <div class="preview" role="dialog" aria-modal="true" aria-label="Page {preview + 1} of {pages.length}">
      <div class="preview-bar">
        <button class="round" onclick={() => (preview = -1)} aria-label="Back to the camera"><Icon name="back" /></button>
        <span class="title">Page {preview + 1} of {pages.length}</span>
        <span class="round placeholder" aria-hidden="true"></span>
      </div>
      <div class="preview-page">
        {#if pages[preview].url}<img src={pages[preview].url} alt="Scanned page {preview + 1}" />
        {:else}<p class="hint-pill">{pages[preview].error ? 'This page failed.' : 'Straightening …'}</p>{/if}
        {#if preview > 0}<button class="round nav prev" onclick={() => preview--} aria-label="Previous page"><Icon name="back" /></button>{/if}
        {#if preview < pages.length - 1}<button class="round nav next" onclick={() => preview++} aria-label="Next page"><Icon name="back" /></button>{/if}
      </div>
      <div class="preview-actions">
        <button class="btn dark" onclick={() => removePage(preview)}><Icon name="trash" />Remove page</button>
        <button class="btn dark" onclick={() => adjust(preview)} disabled={!pages[preview].src}><Icon name="crop" />Adjust corners</button>
        <button class="btn primary" onclick={() => (preview = -1)}>Keep</button>
      </div>
    </div>
  {/if}

  <footer class="controls">
    {#if mode === 'crop'}
      {#if editing >= 0}
        <button class="btn dark" onclick={cancelAdjust}>Cancel</button>
      {:else}
        <button class="btn dark" onclick={() => (liveCamera ? resumeCamera(true) : ((shot = null), (mode = 'start')))}>Retake</button>
      {/if}
      <button class="round" onclick={rotate} aria-label="Rotate"><Icon name="rotate" /></button>
      <div class="seg" role="radiogroup" aria-label="Look">
        {#each [['doc', 'Document'], ['gray', 'Gray'], ['color', 'Color']] as [k, l]}
          <button role="radio" aria-checked={filter === k} class:on={filter === k} onclick={() => (filter = k)}>{l}</button>
        {/each}
      </div>
      <button class="btn primary" onclick={usePage}><Icon name="check" />Use page</button>
    {:else}
      <button class="btn dark" onclick={() => (liveCamera ? galleryInput : fileInput).click()}>{liveCamera ? 'Gallery' : 'Photo'}</button>
      {#if mode === 'camera'}
        <button class="shutter" onclick={capture} disabled={capturing || !videoReady} aria-busy={capturing} aria-label="Take photo"><span></span></button>
      {:else}
        <span class="shutter-space"></span>
      {/if}
      <button class="btn primary" onclick={done} disabled={!pages.length || sending > 0} title={pending.size ? 'Pages are still being straightened' : undefined}>
        {sending ? `${Math.round(sending * 100)} %` : `Done${pages.length ? ' · ' + pages.length : ''}`}
      </button>
    {/if}
  </footer>
</div>

<style>
  .scan { position: fixed; inset: 0; z-index: 30; display: flex; flex-direction: column; background: #14110D; color: #fff; }
  .bar { display: flex; align-items: center; justify-content: space-between; padding: max(12px, env(safe-area-inset-top)) 14px 8px; }
  .title { font-weight: 600; display: flex; flex-direction: column; align-items: center; line-height: 1.2; }
  .into { font-weight: 400; font-size: 0.78rem; opacity: 0.75; }
  .round { width: 44px; height: 44px; border-radius: 50%; border: 0; background: rgb(255 255 255 / 0.14); color: #fff; display: grid; place-items: center; cursor: pointer; }
  .placeholder { background: none; }
  .round.on { background: #F2B33D; color: #2B2620; }
  .viewport { position: relative; flex: 1; min-height: 0; margin: 0 10px; border-radius: 16px; overflow: hidden; background: #2B2620; display: grid; place-items: center; }
  .frame { position: relative; }
  video, .frame img { width: 100%; height: 100%; display: block; object-fit: fill; user-select: none; -webkit-user-drag: none; }
  .overlay { position: absolute; inset: 0; width: 100%; height: 100%; }
  .overlay polygon { fill: rgb(242 179 61 / 0.14); stroke: #F2B33D; stroke-width: 3; stroke-linejoin: round; transition: fill 0.15s, stroke 0.15s; }
  .overlay.ready polygon { fill: rgb(76 187 108 / 0.2); stroke: #4CBB6C; stroke-width: 4; }
  .edit { touch-action: none; }
  .handle { fill: rgb(242 179 61 / 0.25); stroke: #F2B33D; stroke-width: 3; cursor: grab; }
  .hint-pill { position: absolute; left: 50%; bottom: 14px; transform: translateX(-50%); background: rgb(20 17 13 / 0.75); padding: 8px 14px; border-radius: 999px; font-size: 0.88rem; white-space: nowrap; max-width: 92%; overflow: hidden; text-overflow: ellipsis; }
  .hint-pill.ready { background: rgb(36 110 58 / 0.85); }
  .auto-chip { position: absolute; top: 12px; right: 12px; display: flex; align-items: center; gap: 7px; min-height: 36px; padding: 0 14px; border: 0; border-radius: 999px; background: rgb(20 17 13 / 0.7); color: #fff; font: inherit; font-size: 0.85rem; font-weight: 600; cursor: pointer; }
  .auto-chip .dot { width: 9px; height: 9px; border-radius: 50%; background: rgb(255 255 255 / 0.35); }
  .auto-chip.on .dot { background: #4CBB6C; }
  .preview { position: absolute; inset: 0; z-index: 2; display: flex; flex-direction: column; background: #14110D; }
  .preview-bar { display: flex; align-items: center; justify-content: space-between; padding: max(12px, env(safe-area-inset-top)) 14px 8px; }
  .preview-page { position: relative; flex: 1; min-height: 0; margin: 0 10px; display: grid; place-items: center; }
  .preview-page img { max-width: 100%; max-height: 100%; object-fit: contain; border-radius: 6px; background: #fff; }
  .nav { position: absolute; top: 50%; transform: translateY(-50%); background: rgb(20 17 13 / 0.7); }
  .prev { left: 6px; }
  .next { right: 6px; }
  .next :global(svg) { transform: scaleX(-1); }
  .preview-actions { display: flex; justify-content: space-between; gap: 10px; padding: 14px 16px max(18px, calc(env(safe-area-inset-bottom) + 10px)); }
  .fallback { padding: 24px; }
  .fallback-box { display: flex; flex-direction: column; align-items: center; gap: 14px; text-align: center; max-width: 360px; color: #E3DACA; }
  .btn.big { width: 100%; min-height: 52px; }
  .btn.dark { background: rgb(255 255 255 / 0.12); border-color: transparent; color: #fff; }
  .strip { display: flex; gap: 8px; padding: 12px 14px 0; align-items: center; overflow-x: auto; }
  .pg { position: relative; width: 46px; height: 62px; padding: 0; border: 2px solid rgb(255 255 255 / 0.4); border-radius: 6px; overflow: visible; background: #fff; flex: none; cursor: pointer; }
  .pg img { width: 100%; height: 100%; object-fit: cover; border-radius: 4px; }
  .pg .spin { position: absolute; inset: 0; margin: auto; width: 20px; height: 20px; border-radius: 50%; border: 2px solid #ccc; border-top-color: #8B5A2B; animation: spin 0.8s linear infinite; }
  .pg.failed { border-color: #B3261E; }
  @keyframes spin { to { transform: rotate(360deg); } }
  .preview-actions .btn { flex: 1; min-width: 0; }
  .num { position: absolute; top: -7px; right: -7px; width: 20px; height: 20px; border-radius: 50%; background: #fff; color: #14110D; font-size: 0.7rem; font-weight: 700; display: grid; place-items: center; }
  .strip-hint { font-size: 0.8rem; color: rgb(255 255 255 / 0.7); white-space: nowrap; }
  .controls { display: flex; align-items: center; justify-content: space-between; gap: 10px; padding: 14px 16px max(18px, calc(env(safe-area-inset-bottom) + 10px)); flex-wrap: wrap; }
  .controls .btn { min-width: 92px; }
  .shutter { width: 76px; height: 76px; border-radius: 50%; border: 4px solid #fff; background: transparent; padding: 5px; cursor: pointer; }
  .shutter span { display: block; width: 100%; height: 100%; border-radius: 50%; background: #fff; transition: transform 0.1s; }
  .shutter:active span { transform: scale(0.9); }
  .shutter-space { width: 76px; }
  .seg { display: flex; background: rgb(255 255 255 / 0.12); border-radius: 10px; padding: 3px; }
  .seg button { border: 0; background: transparent; color: #fff; font: inherit; font-size: 0.85rem; min-height: 38px; padding: 0 10px; border-radius: 8px; cursor: pointer; }
  .seg button.on { background: #fff; color: #14110D; font-weight: 600; }
  @media (max-width: 420px) {
    .controls { justify-content: center; }
    .seg { order: -1; width: 100%; justify-content: center; }
  }
  @media (min-width: 900px) {
    .viewport { margin: 0 24px; }
  }
</style>
