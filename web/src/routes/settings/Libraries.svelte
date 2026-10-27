<script>
  import { onMount } from 'svelte';
  import { slide } from 'svelte/transition';
  import { api } from '../../lib/api.js';
  import { toast, setLibraries, user } from '../../lib/stores.js';
  import { motion } from '../../lib/motion.js';
  import Icon from '../../components/Icon.svelte';

  let libs = $state([]);
  let users = $state([]);
  let nl = $state({ name: '', path: '' });
  let pathEdited = $state(false);
  // a new library's folder: by default a folder named after it next to the
  // year folders of the first library (that one leaves it alone)
  const slug = (n) => n.trim().toLowerCase().replace(/ä/g, 'ae').replace(/ö/g, 'oe').replace(/ü/g, 'ue').replace(/ß/g, 'ss')
    .replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  let base = $derived(libs.find((l) => l.id === 1)?.path || '');
  $effect(() => {
    if (!pathEdited && base) nl.path = nl.name.trim() ? `${base.replace(/\/+$/, '')}/${slug(nl.name) || 'library'}` : '';
  });
  let adding = $state({}); // library id -> user id to add
  let renaming = $state(0);
  let newName = $state('');
  let busy = $state(false);

  async function load() {
    [libs, users] = await Promise.all([api.get('/api/libraries/all'), api.get('/api/users')]);
  }
  // the switcher shows the account's own libraries: refresh it after changes
  async function refreshMine() {
    try { setLibraries(await api.get('/api/libraries')); } catch {}
  }
  onMount(load);

  async function create(e) {
    e.preventDefault();
    busy = true;
    try {
      await api.post('/api/libraries', nl);
      toast(`Library “${nl.name}” created`);
      nl = { name: '', path: '' };
      pathEdited = false;
      await load();
      refreshMine();
    } catch (err) { toast(err.message, 'error', 6000); }
    busy = false;
  }
  async function setRole(l, uid, role) {
    try {
      await api.put(`/api/libraries/${l.id}/members`, { user_id: uid, role });
      await load();
      if (uid === $user.id) refreshMine();
    } catch (err) { toast(err.message, 'error'); }
  }
  async function addMember(l) {
    const uid = Number(adding[l.id]);
    if (!uid) return;
    await setRole(l, uid, 'reader');
    adding[l.id] = '';
  }
  async function rename(e, l) {
    e.preventDefault();
    try { await api.patch(`/api/libraries/${l.id}`, { name: newName }); renaming = 0; await load(); refreshMine(); }
    catch (err) { toast(err.message, 'error'); }
  }
  async function remove(l) {
    if (!confirm(`Remove the library “${l.name}”? Its folder on the server stays as it is.`)) return;
    try { await api.del(`/api/libraries/${l.id}`); await load(); refreshMine(); }
    catch (err) { toast(err.message, 'error', 6000); }
  }
  const outside = (l) => users.filter((u) => !l.members.some((m) => m.user_id === u.id));
</script>

<div class="card sec">
  <h2>Libraries</h2>
  <p class="muted">Each library is its own archive: its own folder, documents, tags, senders and sorting rules. Share one with the accounts that should see it. Editors add and change documents; readers search, read and download.</p>
</div>

{#each libs as l (l.id)}
  <div class="card sec lib">
    <div class="head">
      {#if renaming === l.id}
        <form class="inline grow" onsubmit={(e) => rename(e, l)}>
          <input type="text" bind:value={newName} maxlength="60" aria-label="New name" />
          <button class="btn small primary">Save</button>
          <button type="button" class="btn small ghost" onclick={() => (renaming = 0)}>Cancel</button>
        </form>
      {:else}
        <h3 class="grow">{l.name}</h3>
        <button class="btn small ghost" onclick={() => { renaming = l.id; newName = l.name; }}>Rename</button>
        {#if l.id !== 1}<button class="btn small ghost danger iconbtn" onclick={() => remove(l)} aria-label="Remove {l.name}" disabled={l.docs > 0} title={l.docs ? 'Only empty libraries can be removed' : 'Remove'}><Icon name="trash" /></button>{/if}
      {/if}
    </div>
    <p class="small"><span class="mono">{l.path}</span><span class="muted"> · {l.docs} document{l.docs === 1 ? '' : 's'}{l.id === 1 ? ' · folder set with SB_ARCHIVE' : ''}</span></p>
    <ul class="rows">
      {#each l.members as m (m.user_id)}
        <li transition:slide={motion({ duration: 160 })}>
          <Icon name="user" />
          <span class="grow">{m.name || m.username} <span class="muted small">{m.username}</span></span>
          <select aria-label="Role of {m.username} in {l.name}" value={m.role} onchange={(e) => setRole(l, m.user_id, e.target.value)}>
            <option value="editor">Can edit</option>
            <option value="reader">Can read</option>
          </select>
          <button class="btn small ghost iconbtn" onclick={() => setRole(l, m.user_id, '')} aria-label="Remove {m.username} from {l.name}"><Icon name="close" /></button>
        </li>
      {/each}
      {#if !l.members.length}<li class="muted small">Nobody has access yet.</li>{/if}
    </ul>
    {#if outside(l).length}
      <div class="inline">
        <select bind:value={adding[l.id]} aria-label="Account to add to {l.name}">
          <option value="">Share with …</option>
          {#each outside(l) as u (u.id)}<option value={u.id}>{u.name || u.username}</option>{/each}
        </select>
        <button class="btn small" onclick={() => addMember(l)} disabled={!adding[l.id]}>Add as reader</button>
      </div>
    {/if}
  </div>
{/each}

<form class="card sec" onsubmit={create}>
  <h2>New library</h2>
  <div class="two">
    <label class="field">Name<input type="text" bind:value={nl.name} maxlength="60" placeholder="e.g. Private, Company, Mum's papers" required /></label>
    <label class="field">Folder on the server
      <input type="text" bind:value={nl.path} oninput={() => (pathEdited = true)} class="mono" placeholder={base ? `${base}/private` : '/srv/shoebox/private'} required />
    </label>
  </div>
  <p class="hint">Shoebox creates the folder and files the library's documents in it by year and month, like the first library. The suggestion puts it inside the first library's folder, so it lives on the same NAS share and nothing has to be set up on the server. To use another share instead, mount it into the container first (Proxmox: <span class="mono">pct set &lt;ID&gt; -mp1 /mnt/pve/nas/other,mp=/srv/shoebox/other</span>) and enter that path.</p>
  <div class="actions"><button class="btn primary" disabled={busy}><Icon name="plus" />Create library</button></div>
</form>

<style>
  .lib .head { display: flex; align-items: center; gap: 6px; }
  .lib h3 { margin: 0; font-size: 1.05rem; }
  .lib p { margin: 0; overflow-wrap: anywhere; }
  .rows { list-style: none; margin: 0; padding: 0; }
  .rows li { display: flex; align-items: center; gap: 10px; padding: 8px 0; border-top: 1px solid var(--line); }
  select, .inline input { min-height: 36px; border-radius: 8px; border: 1px solid var(--line-strong); background: var(--field); color: var(--ink); padding: 0 8px; font: inherit; }
  .rows select, .inline select { width: auto; flex: none; }
  .inline { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  .inline input { flex: 1; min-width: 0; }
  .two { display: grid; gap: 12px; }
  @media (min-width: 640px) { .two { grid-template-columns: 1fr 2fr; } }
</style>
