<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast, user, can } from '../../lib/stores.js';
  import { previewPath } from '../../lib/format.js';
  import { loadSettings, saveSettings } from './useSettings.js';
  import Icon from '../../components/Icon.svelte';

  let { version = '', cycleTheme, theme } = $props();
  let s = $state(null);
  let busy = $state(false);
  const sample = { title: 'Stromrechnung Oktober', sender: 'Stadtwerke', date: '2026-10-24', tags: ['Strom'], id: 42 };

  onMount(async () => { if (can($user, 'admin')) s = (await loadSettings()).settings; });

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
      <span>File automatically when Shoebox is sure<span class="hint">A sorting rule or a known sender matched, and a date and a title were found. Everything else waits in Review.</span></span></label>
    <label class="check"><input type="checkbox" bind:checked={s.learn} />
      <span>Suggest tags and senders from similar documents<span class="hint">A new letter gets the tags that most of the closest filed documents share. They show with a dashed outline in Review.</span></span></label>
    <div class="two">
      <label class="field">Received stamp<input type="text" bind:value={s.stamp_in} maxlength="24" placeholder="Eingang" /></label>
      <label class="field">Paid stamp<input type="text" bind:value={s.stamp_paid} maxlength="24" placeholder="Bezahlt" /></label>
    </div>
    <label class="field">Documents read at the same time
      <select bind:value={s.workers}><option value={1}>1 (small servers)</option><option value={2}>2</option><option value={3}>3</option><option value={4}>4</option></select>
      <span class="hint">Takes effect after a restart.</span>
    </label>
    <div class="actions"><button class="btn primary" disabled={busy}>Save</button></div>
  </form>
{/if}

<a class="card sec statslink" href="/stats"><Icon name="chart" /><span class="grow"><strong>Statistics</strong><span class="muted small"> · pages scanned, documents, senders and more</span></span></a>

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
  <p>Signed in as <strong>{$user?.name}</strong> ({$user?.username}, {$user?.role}). Shoebox {version}.</p>
  <div class="actions">
    <a class="btn" href="/settings/account"><Icon name="key" />Passkeys and password</a>
    <button class="btn" onclick={logout}><Icon name="logout" />Sign out</button>
  </div>
</div>

<style>
  .two { display: grid; gap: 12px; }
  @media (min-width: 640px) { .two { grid-template-columns: 1fr 1fr; } }
  .statslink { flex-direction: row !important; align-items: center; text-decoration: none; color: var(--ink); }
  @media (min-width: 900px) { .statslink { display: none !important; } }
</style>
