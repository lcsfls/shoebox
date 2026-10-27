// Tag colors: chosen in Settings → Tags & senders, otherwise picked from the
// tag's name so the same tag always looks the same.
import { writable, get } from 'svelte/store';
import { api } from './api.js';

export const palette = ['sand', 'clay', 'rose', 'plum', 'sky', 'teal', 'moss', 'olive', 'amber', 'slate'];
export const tagInfo = writable({}); // name (lower case) -> { id, color, count }

let loading = null;
export function loadTags(force = false) {
  if (loading && !force) return loading;
  loading = api.get('/api/tags').then((list) => {
    const m = {};
    for (const t of list) m[t.name.toLowerCase()] = t;
    tagInfo.set(m);
    return list;
  }).catch(() => []);
  return loading;
}

function hash(s) {
  let h = 2166136261;
  for (const c of s.toLowerCase()) { h ^= c.codePointAt(0); h = Math.imul(h, 16777619); }
  return h >>> 0;
}

export function colorFor(name, info = get(tagInfo)) {
  const c = info[name?.toLowerCase()]?.color;
  return palette.includes(c) ? c : palette[hash(name || '') % palette.length];
}
