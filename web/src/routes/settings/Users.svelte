<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast, user } from '../../lib/stores.js';
  import Icon from '../../components/Icon.svelte';

  let users = $state([]);
  let nu = $state({ username: '', name: '', password: '' });
  let pw = $state({ current: '', next: '' });
  async function load() { users = await api.get('/api/users'); }
  onMount(load);

  async function add(e) {
    e.preventDefault();
    try {
      await api.post('/api/users', nu);
      nu = { username: '', name: '', password: '' };
      toast('Account created');
      load();
    } catch (err) { toast(err.message, 'error'); }
  }
  async function remove(u) {
    if (!confirm(`Delete the account “${u.username}”? Documents are shared and stay.`)) return;
    try { await api.del(`/api/users/${u.id}`); load(); } catch (err) { toast(err.message, 'error'); }
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
  <h2>Accounts</h2>
  <p class="muted">Everyone with an account sees the whole archive (made for a household or a small office).</p>
  <ul class="names">
    {#each users as u (u.id)}
      <li><Icon name="user" /><span class="grow">{u.name || u.username} <span class="muted small">{u.username}</span></span>
        {#if u.id !== $user?.id}<button class="btn small ghost danger iconbtn" onclick={() => remove(u)} aria-label="Delete {u.username}"><Icon name="trash" /></button>{/if}</li>
    {/each}
  </ul>
  <form class="stack" onsubmit={add}>
    <h3>Add an account</h3>
    <div class="two">
      <label class="field">Username<input type="text" bind:value={nu.username} autocapitalize="none" required /></label>
      <label class="field">Name<input type="text" bind:value={nu.name} /></label>
    </div>
    <label class="field">Password <span class="hint">at least 10 characters</span><input type="password" bind:value={nu.password} autocomplete="new-password" minlength="10" required /></label>
    <div class="actions"><button class="btn">Create account</button></div>
  </form>
</div>

<form class="card sec" onsubmit={change}>
  <h2>Your password</h2>
  <label class="field">Current password<input type="password" bind:value={pw.current} autocomplete="current-password" required /></label>
  <label class="field">New password<input type="password" bind:value={pw.next} autocomplete="new-password" minlength="10" required /></label>
  <div class="actions"><button class="btn primary">Change password</button></div>
</form>

<style>
  .names { list-style: none; margin: 0; padding: 0; }
  .names li { display: flex; align-items: center; gap: 10px; padding: 10px 0; border-top: 1px solid var(--line); }
  .two { display: grid; gap: 12px; }
  @media (min-width: 640px) { .two { grid-template-columns: 1fr 1fr; } }
  h3 { font-size: 0.95rem; margin-top: 8px; }
</style>
