// A tiny router on the History API: links with href="/…" stay in the app.
import { writable } from 'svelte/store';

const read = () => ({ path: location.pathname, query: new URLSearchParams(location.search) });
export const route = writable(read());

export function navigate(href, replace = false) {
  if (href === location.pathname + location.search) return;
  history[replace ? 'replaceState' : 'pushState']({}, '', href);
  route.set(read());
  window.scrollTo(0, 0);
}

export function setQuery(params, replace = true) {
  const q = new URLSearchParams(location.search);
  for (const [k, v] of Object.entries(params)) {
    if (v === '' || v == null) q.delete(k);
    else q.set(k, v);
  }
  const s = q.toString();
  history[replace ? 'replaceState' : 'pushState']({}, '', location.pathname + (s ? '?' + s : ''));
  route.set(read());
}

export function startRouter() {
  addEventListener('popstate', () => route.set(read()));
  document.addEventListener('click', (e) => {
    const a = e.target.closest?.('a[href]');
    if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    if (a.target || a.hasAttribute('download')) return;
    const href = a.getAttribute('href');
    if (!href.startsWith('/') || href.startsWith('//') || href.startsWith('/api/')) return;
    e.preventDefault();
    navigate(href);
  });
}
