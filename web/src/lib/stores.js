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

// Roles: viewers read, members also add and edit documents, admins also
// change settings, rules and accounts.
const levels = { viewer: 1, member: 2, admin: 3 };
export const can = (u, role) => (levels[u?.role] || 0) >= levels[role];
