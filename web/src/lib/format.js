const lang = typeof navigator !== 'undefined' ? navigator.language || 'en' : 'en';

export function formatDate(iso) {
  if (!iso) return '';
  const [y, m, d] = iso.split('-').map(Number);
  return new Date(y, m - 1, d).toLocaleDateString(lang, { day: '2-digit', month: '2-digit', year: 'numeric' });
}

export function monthName(year, month) {
  return new Date(Number(year), Number(month) - 1, 1).toLocaleDateString(lang, { month: 'long' });
}

export function formatAmount(cents) {
  if (cents == null) return '';
  return (cents / 100).toLocaleString(lang, { style: 'currency', currency: 'EUR' });
}

/** "84,20", "84.20", "1.234,56 €" → cents (or null). */
export function parseAmount(s) {
  s = String(s ?? '').replace(/[^\d.,-]/g, '');
  if (!s) return null;
  const lastSep = Math.max(s.lastIndexOf(','), s.lastIndexOf('.'));
  let num;
  if (lastSep >= 0 && s.length - lastSep - 1 === 2) {
    num = Number(s.slice(0, lastSep).replace(/[.,]/g, '') + '.' + s.slice(lastSep + 1));
  } else {
    num = Number(s.replace(/[.,]/g, ''));
  }
  return Number.isFinite(num) ? Math.round(num * 100) : null;
}

export function amountInput(cents) {
  if (cents == null) return '';
  return (cents / 100).toLocaleString(lang, { minimumFractionDigits: 2, maximumFractionDigits: 2, useGrouping: false });
}

export function formatSize(b) {
  if (!b) return '';
  if (b < 1024 * 1024) return `${Math.max(1, Math.round(b / 1024))} KB`;
  if (b < 1024 ** 3) return `${(b / 1024 / 1024).toFixed(1)} MB`;
  return `${(b / 1024 ** 3).toFixed(1)} GB`;
}

export function ago(iso) {
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return new Date(iso).toLocaleDateString(lang);
}

/** Splits a search snippet (\x02 … \x03 around hits) into parts for <mark>. */
export function snippetParts(s) {
  const out = [];
  let rest = s || '';
  while (rest) {
    const a = rest.indexOf('\x02');
    if (a < 0) { out.push({ text: rest }); break; }
    if (a > 0) out.push({ text: rest.slice(0, a) });
    const b = rest.indexOf('\x03', a);
    out.push({ text: rest.slice(a + 1, b < 0 ? undefined : b), hit: true });
    rest = b < 0 ? '' : rest.slice(b + 1);
  }
  return out;
}

/** Client-side preview of the file name pattern (the server decides in the end). */
export function previewPath(pattern, d) {
  const part = (s) => String(s || '').trim().replace(/[<>:"/\\|?*\x00-\x1f]+/g, '-').split(/\s+/).join('-').replace(/-+/g, '-').replace(/^[-._ ]+|[-._ ]+$/g, '').slice(0, 80);
  const date = d.date || new Date().toISOString().slice(0, 10);
  const map = { '{year}': date.slice(0, 4), '{month}': date.slice(5, 7), '{day}': date.slice(8, 10), '{date}': date,
    '{sender}': part(d.sender), '{title}': part(d.title || 'Document'), '{id}': String(d.id ?? ''), '{tags}': part((d.tags || []).join('-')) };
  return pattern.split('/').map((seg) => {
    for (const [k, v] of Object.entries(map)) seg = seg.split(k).join(v);
    return seg.replace(/_+/g, '_').replace(/^[_\-. ]+|[_\-. ]+$/g, '');
  }).filter((s) => s && s !== '..').join('/') + '.pdf';
}

/** A version of a document's file for image addresses: changes whenever the file does. */
export const fileVersion = (d) => encodeURIComponent(`${d?.updated_at || ''}-${d?.size || 0}-${d?.pages || 0}`);
