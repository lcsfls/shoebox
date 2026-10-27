// Document scanning in the browser, without libraries:
// find the sheet of paper in a photo, straighten it, clean it up.

/** Grayscale (0–255) from RGBA pixels. */
export function toGray(rgba, w, h) {
  const g = new Uint8Array(w * h);
  for (let i = 0, j = 0; i < g.length; i++, j += 4) {
    g[i] = (rgba[j] * 77 + rgba[j + 1] * 150 + rgba[j + 2] * 29) >> 8;
  }
  return g;
}

/** Otsu's threshold: splits the histogram into dark and bright. */
export function otsu(gray) {
  const hist = new Uint32Array(256);
  for (const v of gray) hist[v]++;
  const total = gray.length;
  let sum = 0;
  for (let i = 0; i < 256; i++) sum += i * hist[i];
  let sumB = 0, wB = 0, best = 0, thr = 127;
  for (let i = 0; i < 256; i++) {
    wB += hist[i];
    if (!wB) continue;
    const wF = total - wB;
    if (!wF) break;
    sumB += i * hist[i];
    const mB = sumB / wB, mF = (sum - sumB) / wF;
    const between = wB * wF * (mB - mF) * (mB - mF);
    if (between > best) { best = between; thr = i; }
  }
  return thr;
}

/**
 * Finds the four corners of a sheet of paper (bright area on a darker
 * background). Returns [tl, tr, br, bl] as {x, y} in 0..1, or null.
 */
export function detectQuad(rgba, w, h) {
  const gray = toGray(rgba, w, h);
  const thr = otsu(gray);
  const mask = new Uint8Array(w * h);
  let bright = 0;
  for (let i = 0; i < gray.length; i++) if (gray[i] > thr) { mask[i] = 1; bright++; }
  const frac = bright / gray.length;
  if (frac < 0.08 || frac > 0.95) return null;

  // largest connected bright area (4-neighbourhood)
  const label = new Int32Array(w * h);
  const stack = new Int32Array(w * h);
  let bestLabel = 0, bestSize = 0, cur = 0;
  for (let start = 0; start < mask.length; start++) {
    if (!mask[start] || label[start]) continue;
    cur++;
    let sp = 0, size = 0;
    stack[sp++] = start;
    label[start] = cur;
    while (sp) {
      const p = stack[--sp];
      size++;
      const x = p % w, y = (p / w) | 0;
      if (x > 0 && mask[p - 1] && !label[p - 1]) { label[p - 1] = cur; stack[sp++] = p - 1; }
      if (x < w - 1 && mask[p + 1] && !label[p + 1]) { label[p + 1] = cur; stack[sp++] = p + 1; }
      if (y > 0 && mask[p - w] && !label[p - w]) { label[p - w] = cur; stack[sp++] = p - w; }
      if (y < h - 1 && mask[p + w] && !label[p + w]) { label[p + w] = cur; stack[sp++] = p + w; }
    }
    if (size > bestSize) { bestSize = size; bestLabel = cur; }
  }
  if (bestSize < 0.08 * w * h) return null;

  // extreme points along the diagonals are the corners
  let tl = null, tr = null, br = null, bl = null;
  let minS = Infinity, maxS = -Infinity, minD = Infinity, maxD = -Infinity;
  for (let p = 0; p < label.length; p++) {
    if (label[p] !== bestLabel) continue;
    const x = p % w, y = (p / w) | 0;
    const s = x + y, d = x - y;
    if (s < minS) { minS = s; tl = { x, y }; }
    if (s > maxS) { maxS = s; br = { x, y }; }
    if (d > maxD) { maxD = d; tr = { x, y }; }
    if (d < minD) { minD = d; bl = { x, y }; }
  }
  const quad = [tl, tr, br, bl].map((p) => ({ x: (p.x + 0.5) / w, y: (p.y + 0.5) / h }));
  if (quadArea(quad) < 0.08) return null;
  return quad;
}

/** Area of a quadrilateral (shoelace), in the same units squared. */
export function quadArea(q) {
  let a = 0;
  for (let i = 0; i < 4; i++) {
    const p = q[i], n = q[(i + 1) % 4];
    a += p.x * n.y - n.x * p.y;
  }
  return Math.abs(a) / 2;
}

/** The full image as a quad (when nothing was detected). */
export function fullQuad(inset = 0.04) {
  return [
    { x: inset, y: inset }, { x: 1 - inset, y: inset },
    { x: 1 - inset, y: 1 - inset }, { x: inset, y: 1 - inset },
  ];
}

/** Solves A·x = b (n×n) with Gaussian elimination. */
function solve(A, b) {
  const n = b.length;
  for (let c = 0; c < n; c++) {
    let piv = c;
    for (let r = c + 1; r < n; r++) if (Math.abs(A[r][c]) > Math.abs(A[piv][c])) piv = r;
    [A[c], A[piv]] = [A[piv], A[c]];
    [b[c], b[piv]] = [b[piv], b[c]];
    if (Math.abs(A[c][c]) < 1e-12) return null;
    for (let r = c + 1; r < n; r++) {
      const f = A[r][c] / A[c][c];
      for (let k = c; k < n; k++) A[r][k] -= f * A[c][k];
      b[r] -= f * b[c];
    }
  }
  const x = new Array(n).fill(0);
  for (let r = n - 1; r >= 0; r--) {
    let s = b[r];
    for (let k = r + 1; k < n; k++) s -= A[r][k] * x[k];
    x[r] = s / A[r][r];
  }
  return x;
}

/** Homography mapping the four points `from` onto `to`. */
export function homography(from, to) {
  const A = [], b = [];
  for (let i = 0; i < 4; i++) {
    const { x, y } = from[i], { x: u, y: v } = to[i];
    A.push([x, y, 1, 0, 0, 0, -u * x, -u * y]); b.push(u);
    A.push([0, 0, 0, x, y, 1, -v * x, -v * y]); b.push(v);
  }
  const h = solve(A, b);
  return h && [...h, 1];
}

const dist = (a, b) => Math.hypot(a.x - b.x, a.y - b.y);

/** Output size for a quad in pixel coordinates, long side capped. */
export function outputSize(q, maxSide = 2200) {
  let w = Math.max(dist(q[0], q[1]), dist(q[3], q[2]));
  let h = Math.max(dist(q[0], q[3]), dist(q[1], q[2]));
  const s = Math.min(1, maxSide / Math.max(w, h));
  return { w: Math.max(1, Math.round(w * s)), h: Math.max(1, Math.round(h * s)) };
}

/**
 * Straightens the quad (pixel coordinates in the source) into an upright
 * outW × outH image. Bilinear sampling.
 */
export function warp(src, sw, sh, quad, outW, outH) {
  const to = [{ x: 0, y: 0 }, { x: outW - 1, y: 0 }, { x: outW - 1, y: outH - 1 }, { x: 0, y: outH - 1 }];
  const H = homography(to, quad); // output → source
  const out = new Uint8ClampedArray(outW * outH * 4);
  if (!H) return out;
  for (let y = 0; y < outH; y++) {
    for (let x = 0; x < outW; x++) {
      const d = H[6] * x + H[7] * y + H[8];
      let sx = (H[0] * x + H[1] * y + H[2]) / d;
      let sy = (H[3] * x + H[4] * y + H[5]) / d;
      sx = Math.min(Math.max(sx, 0), sw - 1.001);
      sy = Math.min(Math.max(sy, 0), sh - 1.001);
      const x0 = sx | 0, y0 = sy | 0, fx = sx - x0, fy = sy - y0;
      const i00 = (y0 * sw + x0) * 4, i10 = i00 + 4, i01 = i00 + sw * 4, i11 = i01 + 4;
      const o = (y * outW + x) * 4;
      for (let c = 0; c < 3; c++) {
        const top = src[i00 + c] + (src[i10 + c] - src[i00 + c]) * fx;
        const bot = src[i01 + c] + (src[i11 + c] - src[i01 + c]) * fx;
        out[o + c] = top + (bot - top) * fy;
      }
      out[o + 3] = 255;
    }
  }
  return out;
}

/**
 * How sharp a picture is: the variance of the Laplacian of its gray values
 * (blurred or shaken pictures have weak edges, so a low value).
 */
export function sharpness(rgba, w, h) {
  const g = toGray(rgba, w, h);
  let sum = 0, sq = 0, n = 0;
  for (let y = 1; y < h - 1; y++) {
    for (let x = 1; x < w - 1; x++) {
      const i = y * w + x;
      const l = 4 * g[i] - g[i - 1] - g[i + 1] - g[i - w] - g[i + w];
      sum += l; sq += l * l; n++;
    }
  }
  if (!n) return 0;
  const m = sum / n;
  return sq / n - m * m;
}

/**
 * Evens out the light on a page: the paper's brightness is estimated in
 * blocks (a high percentile, so text doesn't count), smoothed, and every
 * pixel is divided by it. A flash's bright spot and dark corners disappear.
 */
export function flatten(g, w, h) {
  const B = Math.max(16, Math.round(Math.max(w, h) / 40));
  const bw = Math.ceil(w / B), bh = Math.ceil(h / B);
  const hist = new Uint32Array(bw * bh * 64); // 64 bins per block
  const cnt = new Uint32Array(bw * bh);
  for (let y = 0; y < h; y += 2) {
    const by = (y / B) | 0;
    for (let x = 0; x < w; x += 2) {
      const b = by * bw + ((x / B) | 0);
      hist[b * 64 + (g[y * w + x] >> 2)]++;
      cnt[b]++;
    }
  }
  const bg = new Float32Array(bw * bh);
  for (let b = 0; b < bw * bh; b++) {
    const want = cnt[b] * 0.1; // the brightest 10 %
    let acc = 0, v = 63;
    for (; v > 0; v--) { acc += hist[b * 64 + v]; if (acc >= want) break; }
    bg[b] = v * 4 + 2;
  }
  // smooth the blocks (3×3), twice
  let cur = bg;
  for (let pass = 0; pass < 2; pass++) {
    const nx = new Float32Array(bw * bh);
    for (let y = 0; y < bh; y++) {
      for (let x = 0; x < bw; x++) {
        let s = 0, k = 0;
        for (let dy = -1; dy <= 1; dy++) {
          for (let dx = -1; dx <= 1; dx++) {
            const yy = y + dy, xx = x + dx;
            if (yy >= 0 && yy < bh && xx >= 0 && xx < bw) { s += cur[yy * bw + xx]; k++; }
          }
        }
        nx[y * bw + x] = s / k;
      }
    }
    cur = nx;
  }
  // divide by the background, interpolated between block centres
  const out = new Uint8ClampedArray(w * h);
  for (let y = 0; y < h; y++) {
    const fy = Math.min(Math.max(y / B - 0.5, 0), bh - 1);
    const y0 = fy | 0, y1 = Math.min(y0 + 1, bh - 1), ty = fy - y0;
    for (let x = 0; x < w; x++) {
      const fx = Math.min(Math.max(x / B - 0.5, 0), bw - 1);
      const x0 = fx | 0, x1 = Math.min(x0 + 1, bw - 1), tx = fx - x0;
      const top = cur[y0 * bw + x0] * (1 - tx) + cur[y0 * bw + x1] * tx;
      const bot = cur[y1 * bw + x0] * (1 - tx) + cur[y1 * bw + x1] * tx;
      const b = Math.max(24, top * (1 - ty) + bot * ty);
      out[y * w + x] = (g[y * w + x] * 255) / b;
    }
  }
  return out;
}

/** Unsharp mask on gray values: crisper text strokes. */
export function sharpen(g, w, h, amount = 0.7) {
  const blur = new Float32Array(w * h);
  const tmp = new Float32Array(w * h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const i = y * w + x;
      tmp[i] = (g[i - (x > 0 ? 1 : 0)] + g[i] + g[i + (x < w - 1 ? 1 : 0)]) / 3;
    }
  }
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const i = y * w + x;
      blur[i] = (tmp[i - (y > 0 ? w : 0)] + tmp[i] + tmp[i + (y < h - 1 ? w : 0)]) / 3;
    }
  }
  const out = new Uint8ClampedArray(w * h);
  for (let i = 0; i < w * h; i++) out[i] = g[i] + (g[i] - blur[i]) * amount;
  return out;
}

/**
 * Cleans up a page in place. mode: "color" (as photographed),
 * "gray" (even light, gray), "doc" (white paper, dark crisp text).
 */
export function enhance(px, mode, w = 0, h = 0) {
  if (mode === 'color') return px;
  const n = px.length / 4;
  let g = new Uint8ClampedArray(n);
  for (let i = 0, j = 0; i < n; i++, j += 4) g[i] = (px[j] * 77 + px[j + 1] * 150 + px[j + 2] * 29) >> 8;
  if (w && h) {
    g = flatten(g, w, h);
    if (mode === 'doc') g = sharpen(g, w, h, 0.6);
  }
  let lo = 0, hi = 255;
  if (mode === 'doc') {
    const hist = new Uint32Array(256);
    for (let i = 0; i < n; i++) hist[g[i]]++;
    // stretch: the darkest 1 % become black, the brightest 40 % (the paper) white
    let acc = 0;
    for (let v = 0; v < 256; v++) { acc += hist[v]; if (acc > n * 0.01) { lo = v; break; } }
    acc = 0;
    for (let v = 255; v >= 0; v--) { acc += hist[v]; if (acc > n * 0.4) { hi = v; break; } }
    if (hi - lo < 40) { lo = 0; hi = 255; }
  }
  const lut = new Uint8Array(256);
  for (let v = 0; v < 256; v++) {
    let t = (v - lo) / (hi - lo);
    t = Math.min(Math.max(t, 0), 1);
    if (mode === 'doc') t = Math.pow(t, 1.5); // darker text strokes
    lut[v] = Math.round(t * 255);
  }
  for (let i = 0, j = 0; i < n; i++, j += 4) {
    const v = lut[g[i]];
    px[j] = px[j + 1] = px[j + 2] = v;
  }
  return px;
}
