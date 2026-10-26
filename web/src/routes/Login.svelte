<script>
  import { api } from '../lib/api.js';
  import Icon from '../components/Icon.svelte';

  let { needsSetup = false, onDone } = $props();
  let username = $state('');
  let name = $state('');
  let password = $state('');
  let password2 = $state('');
  let code = $state('');
  let error = $state('');
  let busy = $state(false);

  async function submit(e) {
    e.preventDefault();
    error = '';
    if (needsSetup && password !== password2) {
      error = 'The passwords don’t match.';
      return;
    }
    busy = true;
    try {
      if (needsSetup) await api.post('/api/setup', { code, username, name, password });
      else await api.post('/api/login', { username, password });
      onDone?.();
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }
</script>

<div class="wrap">
  <form class="card box stack" onsubmit={submit}>
    <div class="logo"><Icon name="box" size={34} /><span>shoebox</span></div>
    {#if needsSetup}
      <div>
        <h1>Welcome</h1>
        <p class="muted">Create the first account. The setup code is printed in the server log (for an LXC container: <span class="mono">journalctl -u shoebox</span>).</p>
      </div>
      <label class="field">Setup code<input type="text" bind:value={code} autocomplete="one-time-code" autocapitalize="characters" required /></label>
      <label class="field">Username<input type="text" bind:value={username} autocomplete="username" autocapitalize="none" required /></label>
      <label class="field">Your name <span class="hint">shown in the app, optional</span><input type="text" bind:value={name} autocomplete="name" /></label>
      <label class="field">Password <span class="hint">at least 10 characters</span><input type="password" bind:value={password} autocomplete="new-password" minlength="10" required /></label>
      <label class="field">Password again<input type="password" bind:value={password2} autocomplete="new-password" required /></label>
    {:else}
      <h1>Sign in</h1>
      <label class="field">Username<input type="text" bind:value={username} autocomplete="username" autocapitalize="none" required /></label>
      <label class="field">Password<input type="password" bind:value={password} autocomplete="current-password" required /></label>
    {/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button class="btn primary" disabled={busy}>{needsSetup ? 'Create account' : 'Sign in'}</button>
  </form>
</div>

<style>
  .wrap { min-height: 100dvh; display: grid; place-items: center; padding: 20px 16px; }
  .box { width: min(100%, 420px); padding: 28px 24px; box-shadow: var(--shadow); }
  .logo { display: flex; align-items: center; gap: 10px; color: var(--accent); }
  .logo span { color: var(--ink); font-size: 1.4rem; font-weight: 700; letter-spacing: -0.02em; }
  h1 { margin-bottom: 4px; }
</style>
