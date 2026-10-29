import { writable, derived, get } from 'svelte/store';

export const user = writable(undefined); // undefined = not known yet, null = signed out
export const toasts = writable([]);
export const counts = writable({ inbox: 0, busy: 0 });

let n = 0;
export function toast(text, kind = 'info', ms = 3500) {
  const id = ++n;
  toasts.update((t) => [...t, { id, text, kind }]);
  setTimeout(() => toasts.update((t) => t.filter((x) => x.id !== id)), ms);
}

// Accounts: admins also manage settings, libraries and accounts.
const levels = { viewer: 1, member: 2, admin: 3 };
export const can = (u, role) => (levels[u?.role] || 0) >= levels[role];

// Libraries: the ones this account belongs to, and the one it looks at.
// The API gets the current one in the X-Library header (lib/api.js).
export const libraries = writable([]);
function storedLibrary() {
  try { return Number(localStorage.getItem('sb-library')) || 0; } catch { return 0; }
}
export const library = writable(storedLibrary());
library.subscribe((id) => { try { if (id) localStorage.setItem('sb-library', String(id)); } catch {} });

/** After /api/me: keep the chosen library if the account still has it. */
export function setLibraries(list) {
  libraries.set(list || []);
  const cur = get(library);
  if (!list?.some((l) => l.id === cur)) library.set(list?.[0]?.id || 0);
}
export const currentLibrary = derived([libraries, library], ([ls, id]) => ls.find((l) => l.id === id) || null);
/** Where new scans and uploads go: the account's private Home (the server picks it as well). */
export const intakeLibrary = derived([libraries, currentLibrary], ([ls, cur]) => ls.find((l) => l.home) || cur);
/** May the account scan and upload? */
export const canAdd = derived(intakeLibrary, (l) => l?.role === 'editor');
/** After a scan or upload: look at the library it went into, so it shows in Review. */
export function showIntake(id) {
  if (id && id !== get(library) && get(libraries).some((l) => l.id === id)) library.set(id);
}
/** May the account add and change documents in the current library? */
export const canEdit = derived(currentLibrary, (l) => l?.role === 'editor');
/** … in the library of a given document. */
export function canEditLibrary(id) {
  return get(libraries).find((l) => l.id === id)?.role === 'editor';
}
