// node --test web/test  (no dependencies)
import test from 'node:test';
import assert from 'node:assert/strict';
import { detectQuad, homography, warp, enhance, outputSize, quadArea, fullQuad } from '../src/lib/scan.js';

// A dark table with a slightly rotated white sheet on it.
function photo(w, h, corners) {
  const px = new Uint8ClampedArray(w * h * 4);
  const inside = (x, y) => {
    // point in convex polygon
    let sign = 0;
    for (let i = 0; i < 4; i++) {
      const a = corners[i], b = corners[(i + 1) % 4];
      const c = (b.x - a.x) * (y - a.y) - (b.y - a.y) * (x - a.x);
      if (c !== 0) {
        if (sign === 0) sign = Math.sign(c);
        else if (Math.sign(c) !== sign) return false;
      }
    }
    return true;
  };
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const i = (y * w + x) * 4;
      const v = inside(x, y) ? 235 : 60 + ((x * 7 + y * 13) % 25);
      px[i] = v; px[i + 1] = v - 5; px[i + 2] = v - 10; px[i + 3] = 255;
    }
  }
  return px;
}

test('finds the corners of a rotated sheet', () => {
  const w = 240, h = 320;
  const c = [{ x: 50, y: 40 }, { x: 200, y: 55 }, { x: 185, y: 290 }, { x: 35, y: 275 }];
  const q = detectQuad(photo(w, h, c), w, h);
  assert.ok(q, 'nothing detected');
  for (let i = 0; i < 4; i++) {
    assert.ok(Math.abs(q[i].x * w - c[i].x) < 4, `corner ${i} x: ${q[i].x * w} vs ${c[i].x}`);
    assert.ok(Math.abs(q[i].y * h - c[i].y) < 4, `corner ${i} y: ${q[i].y * h} vs ${c[i].y}`);
  }
});

test('gives up on an empty picture', () => {
  const w = 50, h = 50;
  const px = new Uint8ClampedArray(w * h * 4).fill(128);
  assert.equal(detectQuad(px, w, h), null);
});

test('homography maps corners exactly', () => {
  const from = [{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 1, y: 1 }, { x: 0, y: 1 }];
  const to = [{ x: 10, y: 5 }, { x: 90, y: 12 }, { x: 80, y: 100 }, { x: 5, y: 95 }];
  const H = homography(from, to);
  for (let i = 0; i < 4; i++) {
    const { x, y } = from[i];
    const d = H[6] * x + H[7] * y + H[8];
    assert.ok(Math.abs((H[0] * x + H[1] * y + H[2]) / d - to[i].x) < 1e-6);
    assert.ok(Math.abs((H[3] * x + H[4] * y + H[5]) / d - to[i].y) < 1e-6);
  }
});

test('straightening the sheet gives a white page', () => {
  const w = 240, h = 320;
  const c = [{ x: 50, y: 40 }, { x: 200, y: 55 }, { x: 185, y: 290 }, { x: 35, y: 275 }];
  const src = photo(w, h, c);
  const inner = [{ x: 56, y: 47 }, { x: 193, y: 61 }, { x: 179, y: 283 }, { x: 42, y: 269 }];
  const { w: ow, h: oh } = outputSize(inner, 400);
  assert.ok(oh > ow, 'portrait page expected');
  const out = warp(src, w, h, inner, ow, oh);
  let bright = 0;
  for (let i = 0; i < out.length; i += 4) if (out[i] > 200) bright++;
  assert.ok(bright / (ow * oh) > 0.97, `only ${bright / (ow * oh)} of the page is white`);
});

test('document mode makes paper white and text dark', () => {
  const px = new Uint8ClampedArray(100 * 4);
  for (let i = 0; i < 100; i++) {
    const v = i < 10 ? 70 : 190; // greyish paper, some ink
    px.set([v, v, v, 255], i * 4);
  }
  enhance(px, 'doc');
  assert.equal(px[0], 0);
  assert.equal(px[99 * 4], 255);
});

test('helpers', () => {
  assert.ok(Math.abs(quadArea(fullQuad(0)) - 1) < 1e-9);
  assert.deepEqual(outputSize([{ x: 0, y: 0 }, { x: 1000, y: 0 }, { x: 1000, y: 4000 }, { x: 0, y: 4000 }], 2000), { w: 500, h: 2000 });
});

test('flatten evens out a flash spot', async () => {
  const { flatten } = await import('../src/lib/scan.js');
  const w = 200, h = 200;
  const g = new Uint8ClampedArray(w * h);
  // paper lit from the centre (bright middle, dark corners) with a few dark text lines
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const d = Math.hypot(x - 100, y - 100) / 141;
      let v = 250 - 140 * d;
      if (y % 20 < 3 && x > 20 && x < 180) v *= 0.3;
      g[y * w + x] = v;
    }
  }
  const out = flatten(g, w, h);
  const before = g[12 * w + 12], paperCorner = out[12 * w + 12], paperMiddle = out[110 * w + 100];
  assert.ok(Math.abs(paperCorner - paperMiddle) < 45 && paperMiddle - before > 80, `paper ${paperCorner} vs ${paperMiddle} (before ${before})`);
  assert.ok(out[100 * w + 100] < 120, 'text stays dark');
});

test('sharpness: a blurred picture scores lower', async () => {
  const { sharpness } = await import('../src/lib/scan.js');
  const w = 64, h = 64;
  const sharp = new Uint8ClampedArray(w * h * 4), soft = new Uint8ClampedArray(w * h * 4);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const i = (y * w + x) * 4;
      const v = (x >> 2) % 2 ? 230 : 30;
      const b = 130 + 100 * Math.sin((x / 8) * Math.PI);
      sharp[i] = sharp[i + 1] = sharp[i + 2] = v;
      soft[i] = soft[i + 1] = soft[i + 2] = b;
    }
  }
  assert.ok(sharpness(sharp, w, h) > sharpness(soft, w, h) * 5);
});
