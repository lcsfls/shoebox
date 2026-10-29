// The archive list a document was opened from (its filters and sort order),
// so ← / → on the document page go to the neighbours in that list.
import { get } from 'svelte/store';
import { api } from './api.js';
import { library } from './stores.js';

const KEY = 'sb-browse';
const PAGE = 60;

/** The archive saves what it shows: query (without limit/offset), ids so far, total. */
export function remember(query, ids, total) {
  try { sessionStorage.setItem(KEY, JSON.stringify({ library: get(library), query, ids, total })); } catch {}
}

export function recall() {
  try {
    const b = JSON.parse(sessionStorage.getItem(KEY));
    return b && b.library === get(library) && Array.isArray(b.ids) ? b : null;
  } catch {
    return null;
  }
}

/** The next page of the list, when the end of what was loaded is reached. */
export async function more(b) {
  if (!b || b.ids.length >= b.total) return b;
  const p = new URLSearchParams(b.query);
  p.set('limit', String(PAGE));
  p.set('offset', String(b.ids.length));
  const r = await api.get('/api/docs?' + p);
  const ids = [...b.ids, ...r.docs.map((d) => d.id).filter((id) => !b.ids.includes(id))];
  remember(b.query, ids, r.total);
  return { ...b, ids, total: r.total };
}
