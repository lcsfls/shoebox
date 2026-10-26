import { api } from '../../lib/api.js';

/** Loads the settings object; save() sends it back. */
export async function loadSettings() {
  const r = await api.get('/api/settings');
  return r;
}
export function saveSettings(settings, extra = {}) {
  return api.put('/api/settings', { ...settings, ...extra });
}
