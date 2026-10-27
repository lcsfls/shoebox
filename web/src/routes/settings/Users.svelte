<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast, user } from '../../lib/stores.js';
  import Icon from '../../components/Icon.svelte';

  let users = $state([]);
  let nu = $state({ username: '', name: '', password: '', role: 'member' });
  let resetFor = $state(null);
  let resetPw = $state('');
  const roles = [
    ['admin', 'Admin', 'also settings, libraries and accounts'],
    ['member', 'User', 'works in the libraries shared with them'],
  ];
  let shared = $state('editor'); // access to the first library for a new account

  async function load() { users = await api.get('/api/users'); }
  onMount(load);

  async function add(e) {
    e.preventDefault();
    try {
      await api.post('/api/users', { ...nu, libraries: shared ? [{ id: 1, role: shared }] : [] });
      nu = { username: '', name: '', password: '', role: 'member' };
      toast('Account created');
      load();
    } catch (err) { toast(err.message, 'error'); }
  }
  async function setRole(u, role) {
    try { await api.patch(`/api/users/${u.id}`, { role }); toast(`${u.name || u.username} is now ${role}`); }
    catch (err) { toast(err.message, 'error'); }
    load();
  }
  async function reset(e) {
    e.preventDefault();
    try {
      await api.patch(`/api/users/${resetFor.id}`, { password: resetPw });
      toast(`New password set for ${resetFor.username}. They were signed out.`);
      resetFor = null; resetPw = '';
    } catch (err) { toast(err.message, 'error'); }
  }
  async function remove(u) {
    if (!confirm(`Delete the account “${u.username}”? Its documents stay in the archive.`)) return;
    try { await api.del(`/api/users/${u.id}`); load(); } catch (err) { toast(err.message, 'error'); }
  }
</script>

<div class="card sec">
  <h2>Accounts</h2>
  <p class="muted">Who sees which documents is set per library under <a href="/settings/libraries">Libraries</a>.</p>
  <ul class="rows">
    {#each users as u (u.id)}
      <li>
        <Icon name="user" />
        <span class="grow">{u.name || u.username} <span class="muted small">{u.username} · {u.docs} document{u.docs === 1 ? '' : 's'} added{u.passkeys ? ` · ${u.passkeys} passkey${u.passkeys === 1 ? '' : 's'}` : ''}</span></span>
        <select aria-label="Role of {u.username}" value={u.role === 'viewer' ? 'member' : u.role} onchange={(e) => setRole(u, e.target.value)} disabled={u.id === $user.id}>
          {#each roles as [k, label] (k)}<option value={k}>{label}</option>{/each}
        </select>
        {#if u.id !== $user.id}
          <button class="btn small ghost iconbtn" onclick={() => { resetFor = u; resetPw = ''; }} aria-label="New password for {u.username}" title="New password"><Icon name="key" /></button>
          <button class="btn small ghost danger iconbtn" onclick={() => remove(u)} aria-label="Delete {u.username}"><Icon name="trash" /></button>
        {/if}
      </li>
      {#if resetFor?.id === u.id}
        <li class="sub">
          <form class="inline" onsubmit={reset}>
            <label class="field grow">New password for {u.username}<input type="password" bind:value={resetPw} autocomplete="new-password" minlength="10" required /></label>
            <button class="btn primary">Set password</button>
            <button type="button" class="btn ghost" onclick={() => (resetFor = null)}>Cancel</button>
          </form>
        </li>
      {/if}
    {/each}
  </ul>
  <dl class="legend small muted">
    {#each roles as [k, label, what] (k)}<dt>{label}</dt><dd>{what}</dd>{/each}
  </dl>
</div>

<form class="card sec" onsubmit={add}>
  <h2>Add an account</h2>
  <div class="two">
    <label class="field">Username<input type="text" bind:value={nu.username} autocapitalize="none" required /></label>
    <label class="field">Name<input type="text" bind:value={nu.name} /></label>
  </div>
  <div class="two">
    <label class="field">Password <span class="hint">at least 10 characters; they can add a passkey later</span><input type="password" bind:value={nu.password} autocomplete="new-password" minlength="10" required /></label>
    <label class="field">Account
      <select bind:value={nu.role}>{#each roles as [k, label] (k)}<option value={k}>{label}</option>{/each}</select>
    </label>
  </div>
  <div class="two">
    <label class="field">In the first library
      <select bind:value={shared}><option value="editor">Can edit</option><option value="reader">Can read</option><option value="">No access</option></select>
      <span class="hint">More libraries under Settings → Libraries.</span>
    </label>
  </div>
  <div class="actions"><button class="btn primary">Create account</button></div>
</form>

<style>
  .rows { list-style: none; margin: 0; padding: 0; }
  .rows li { display: flex; align-items: center; gap: 10px; padding: 10px 0; border-top: 1px solid var(--line); flex-wrap: wrap; }
  .rows li.sub { border-top: 0; padding-top: 0; }
  .rows select { min-height: 36px; border-radius: 8px; border: 1px solid var(--line-strong); background: var(--field); color: var(--ink); padding: 0 8px; }
  .inline { display: flex; gap: 10px; align-items: end; flex-wrap: wrap; width: 100%; }
  .two { display: grid; gap: 12px; }
  @media (min-width: 640px) { .two { grid-template-columns: 1fr 1fr; } }
  .legend { display: grid; grid-template-columns: max-content 1fr; gap: 2px 12px; margin: 0; }
  .legend dt { font-weight: 600; }
  .legend dd { margin: 0; }
</style>
