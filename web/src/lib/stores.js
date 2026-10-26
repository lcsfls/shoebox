import { writable } from 'svelte/store';

export const user = writable(undefined); // undefined = not known yet, null = signed out
export const toasts = writable([]);
export const counts = writable({ inbox: 0, busy: 0 });

let n = 0;
export function toast(text, kind = 'info', ms = 3500) {
  const id = ++n;
  toasts.update((t) => [...t, { id, text, kind }]);
  setTimeout(() => toasts.update((t) => t.filter((x) => x.id !== id)), ms);
}
