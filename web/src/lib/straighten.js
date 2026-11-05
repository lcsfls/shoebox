// Turns a photo and the four corners of the page into the finished page
// (a JPEG): in scan.worker.js where the browser can, otherwise right here.
/* global __SCAN_WORKER__ */
import { warp, enhance, outputSize } from './scan.js';

const workerURL = typeof __SCAN_WORKER__ === 'string' ? __SCAN_WORKER__ : '';
let worker = null; // null: not tried yet, false: not available
let seq = 0;
const waiting = new Map();

function getWorker() {
  if (worker !== null) return worker;
  worker = false;
  if (!workerURL || typeof Worker === 'undefined' || typeof OffscreenCanvas === 'undefined') return worker;
  try {
    const w = new Worker(workerURL, { type: 'module' });
    w.onmessage = (e) => {
      const p = waiting.get(e.data.id);
      waiting.delete(e.data.id);
      if (!p) return;
      if (e.data.error) p.no(new Error(e.data.error));
      else p.ok(e.data.blob);
    };
    w.onerror = () => {
      worker = false; // done here from now on
      for (const p of waiting.values()) p.no(new Error('worker failed'));
      waiting.clear();
    };
    worker = w;
  } catch {
    worker = false;
  }
  return worker;
}

/** source: a canvas or an image; quad: [tl, tr, br, bl] in 0..1; filter: doc | gray | color. */
export async function straighten(source, quad, filter, maxSide = 3000) {
  const w = getWorker();
  if (w) {
    try {
      const bitmap = await createImageBitmap(source);
      const id = ++seq;
      return await new Promise((ok, no) => {
        waiting.set(id, { ok, no });
        w.postMessage({ id, bitmap, quad, filter, maxSide }, [bitmap]);
      });
    } catch {
      // fall back to doing it here
    }
  }
  return straightenHere(source, quad, filter, maxSide);
}

async function straightenHere(source, quad, filter, maxSide) {
  let canvas = source;
  if (!(source instanceof HTMLCanvasElement)) {
    canvas = document.createElement('canvas');
    canvas.width = source.width;
    canvas.height = source.height;
    canvas.getContext('2d').drawImage(source, 0, 0);
  }
  const W = canvas.width, H = canvas.height;
  const px = quad.map((p) => ({ x: p.x * (W - 1), y: p.y * (H - 1) }));
  const { w, h } = outputSize(px, maxSide);
  const src = canvas.getContext('2d', { willReadFrequently: true }).getImageData(0, 0, W, H).data;
  const out = warp(src, W, H, px, w, h);
  enhance(out, filter, w, h);
  const oc = document.createElement('canvas');
  oc.width = w;
  oc.height = h;
  oc.getContext('2d').putImageData(new ImageData(out, w, h), 0, 0);
  return new Promise((r) => oc.toBlob(r, 'image/jpeg', 0.86));
}
