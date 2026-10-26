<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast, user } from '../../lib/stores.js';
  import { formatDate } from '../../lib/format.js';
  import { addPasskey, deviceName, passkeysSupported } from '../../lib/passkey.js';
  import Icon from '../../components/Icon.svelte';

  let pw = $state({ current: '', next: '' });
  let keys = $state([]);
  let keyName = $state(deviceName());
  let busy = $state(false);
  const supported = passkeysSupported();

  async function load() { keys = await api.get('/api/passkeys'); }
  onMount(load);

  async function add(e) {
    e.preventDefault();
    busy = true;
    try {
      await addPasskey(keyName);
      toast('Passkey added. Next time, sign in with it.');
      load();
    } catch (err) { toast(err.message, 'error'); }
    busy = false;
  }
  async function remove(k) {
    if (!confirm(`Remove the passkey “${k.name}”? Delete it on the device too.`)) return;
    await api.del(`/api/passkeys/${k.id}`);
    load();
  }
  async function change(e) {
    e.preventDefault();
    try {
      await api.post('/api/account/password', { current: pw.current, new: pw.next });
      pw = { current: '', next: '' };
      toast('Password changed. Other devices were signed out.');
    } catch (err) { toast(err.message, 'error'); }
  }
</script>

<div class="card sec">
  <h2>Passkeys</h2>
  <p class="muted">Sign in with the fingerprint, face or PIN of your phone or computer instead of the password. The passkey stays on your device; Shoebox only keeps its public half.</p>
  {#if keys.length}
    <ul class="rows">
      {#each keys as k (k.id)}
        <li><Icon name="key" /><span class="grow">{k.name}<span class="muted small"> · added {formatDate(k.created_at.slice(0, 10))}{k.last_used_at ? ` · last used ${formatDate(k.last_used_at.slice(0, 10))}` : ''}</span></span>
          <button class="btn small ghost danger iconbtn" onclick={() => remove(k)} aria-label="Remove {k.name}"><Icon name="trash" /></button></li>
      {/each}
    </ul>
  {/if}
  {#if supported}
    <form class="inline" onsubmit={add}>
      <label class="field grow">Name of this device<input type="text" bind:value={keyName} maxlength="60" /></label>
      <button class="btn primary" disabled={busy}><Icon name="plus" />Add a passkey</button>
    </form>
  {:else}
    <p class="note">Passkeys work when Shoebox is opened over HTTPS (or on <span class="mono">localhost</span>). Put it behind your reverse proxy with a certificate, then add one here.</p>
  {/if}
</div>

<form class="card sec" onsubmit={change}>
  <h2>Password</h2>
  <p class="muted">Signed in as <strong>{$user.name}</strong> ({$user.username}, {$user.role}).</p>
  <label class="field">Current password<input type="password" bind:value={pw.current} autocomplete="current-password" required /></label>
  <label class="field">New password <span class="hint">at least 10 characters</span><input type="password" bind:value={pw.next} autocomplete="new-password" minlength="10" required /></label>
  <div class="actions"><button class="btn">Change password</button></div>
</form>

<style>
  .rows { list-style: none; margin: 0; padding: 0; }
  .rows li { display: flex; align-items: center; gap: 10px; padding: 10px 0; border-top: 1px solid var(--line); }
  .inline { display: flex; gap: 10px; align-items: end; flex-wrap: wrap; }
  .note { background: var(--warn); color: var(--warn-ink); padding: 10px 12px; border-radius: 10px; margin: 0; }
</style>
