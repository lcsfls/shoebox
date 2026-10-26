<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast, user } from '../../lib/stores.js';
  import { previewPath } from '../../lib/format.js';
  import { loadSettings, saveSettings } from './useSettings.js';
  import Icon from '../../components/Icon.svelte';

  let { version = '', cycleTheme, theme } = $props();
  let s = $state(null);
  let busy = $state(false);
  const sample = { title: 'Stromrechnung Oktober', sender: 'Stadtwerke', date: '2026-10-24', tags: ['Strom'], id: 42 };

  onMount(async () => { s = (await loadSettings()).settings; });

  async function save(e) {
    e.preventDefault();
    busy = true;
    try { await saveSettings(s); toast('Saved'); } catch (err) { toast(err.message, 'error'); } finally { busy = false; }
  }
  async function logout() {
    await api.post('/api/logout').catch(() => {});
    user.set(null);
  }
</script>

{#if s}
  <form class="card sec" onsubmit={save}>
    <h2>Filing</h2>
    <label class="field">File name pattern
      <input type="text" bind:value={s.pattern} class="mono" />
      <span class="hint">Placeholders: {'{year} {month} {day} {date} {sender} {title} {tags} {id}'}. Slashes make folders.</span>
    </label>
    <p class="small">Example: <span class="mono">{previewPath(s.pattern, sample)}</span></p>
    <label class="check"><input type="checkbox" bind:checked={s.auto_file} />
      <span>File automatically when Shoebox is sure<span class="hint">A sorting rule or a known sender matched and a date was found. Everything else waits in Review.</span></span></label>
    <label class="field">Documents read at the same time
      <select bind:value={s.workers}><option value={1}>1 (small servers)</option><option value={2}>2</option><option value={3}>3</option><option value={4}>4</option></select>
      <span class="hint">Takes effect after a restart.</span>
    </label>
    <div class="actions"><button class="btn primary" disabled={busy}>Save</button></div>
  </form>
{/if}

<div class="card sec">
  <h2>Appearance</h2>
  <div class="actions">
    <button class="btn" onclick={cycleTheme}><Icon name={theme === 'dark' ? 'moon' : theme === 'light' ? 'sun' : 'auto'} />
      {theme === 'system' ? 'Follows the system' : theme === 'dark' ? 'Dark' : 'Light'}</button>
    <span class="hint">Tap to switch. Stored in this browser only.</span>
  </div>
</div>

<div class="card sec">
  <h2>Account</h2>
  <p>Signed in as <strong>{$user?.name}</strong> ({$user?.username}). Shoebox {version}.</p>
  <div class="actions">
    <a class="btn" href="/settings/users">Change password</a>
    <button class="btn" onclick={logout}><Icon name="logout" />Sign out</button>
  </div>
</div>
