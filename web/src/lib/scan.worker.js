// Straightens and cleans up a scanned page away from the page's thread, so
// the camera stays smooth while earlier pages are still being processed.
import { warp, enhance, outputSize } from './scan.js';

self.onmessage = async (e) => {
  const { id, bitmap, quad, filter, maxSide } = e.data;
  try {
    const W = bitmap.width, H = bitmap.height;
    const c = new OffscreenCanvas(W, H);
    const ctx = c.getContext('2d', { willReadFrequently: true });
    ctx.drawImage(bitmap, 0, 0);
    bitmap.close();
    const src = ctx.getImageData(0, 0, W, H).data;
    const px = quad.map((p) => ({ x: p.x * (W - 1), y: p.y * (H - 1) }));
    const { w, h } = outputSize(px, maxSide);
    const out = warp(src, W, H, px, w, h);
    enhance(out, filter, w, h);
    const oc = new OffscreenCanvas(w, h);
    oc.getContext('2d').putImageData(new ImageData(out, w, h), 0, 0);
    const blob = await oc.convertToBlob({ type: 'image/jpeg', quality: 0.86 });
    self.postMessage({ id, blob });
  } catch (err) {
    self.postMessage({ id, error: String(err?.message || err) });
  }
};
