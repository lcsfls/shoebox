<script>
  import { onMount } from 'svelte';
  import { api, upload } from '../../lib/api.js';
  import { toast, library } from '../../lib/stores.js';
  import Icon from '../../components/Icon.svelte';

  let lh = $state(null);
  let busy = $state(false);
  let logoV = $state(Date.now());
  onMount(async () => { lh = await api.get('/api/letterhead'); });

  async function save(e) {
    e.preventDefault();
    busy = true;
    try { lh = await api.put('/api/letterhead', lh); toast('Letterhead saved'); } catch (err) { toast(err.message, 'error'); }
    busy = false;
  }
  async function pickLogo(e) {
    const f = e.currentTarget.files?.[0];
    e.currentTarget.value = '';
    if (!f) return;
    const fd = new FormData();
    fd.append('logo', f);
    try { lh = { ...lh, ...(await upload('/api/letterhead/logo', fd)) }; logoV = Date.now(); toast('Logo saved'); } catch (err) { toast(err.message, 'error'); }
  }
  async function removeLogo() {
    try { lh = { ...lh, ...(await api.del('/api/letterhead/logo')) }; } catch (err) { toast(err.message, 'error'); }
  }
</script>

{#if lh}
  <form class="card sec" onsubmit={save}>
    <h2>Letterhead</h2>
    <p class="muted small">The sender of letters written in this library: shown at the top, in the return line above the address window, and in the footer.</p>
    <div class="two">
      <label class="field">Name or company<input type="text" bind:value={lh.name} maxlength="120" /></label>
      <label class="field">Name under the signature<input type="text" bind:value={lh.signer} maxlength="120" /><span class="hint">Used for new letters; leave empty to use the name.</span></label>
    </div>
    <label class="field">Address<textarea rows="3" bind:value={lh.address} placeholder={'Street 1\n12345 Town'}></textarea></label>
    <div class="three">
      <label class="field">Phone<input type="text" bind:value={lh.phone} maxlength="60" /></label>
      <label class="field">Email<input type="text" bind:value={lh.email} maxlength="120" /></label>
      <label class="field">Website<input type="text" bind:value={lh.web} maxlength="120" /></label>
    </div>
    <label class="field">Footer
      <textarea rows="6" bind:value={lh.footer} placeholder={'Bank\nIBAN …\n\nTax number …'}></textarea>
      <span class="hint">Up to three columns: a blank line starts the next one. Bank details, tax number, register entry.</span>
    </label>
    <div class="actions"><button class="btn primary" disabled={busy}>Save</button><a class="btn ghost" href="/letters">Go to letters</a></div>
  </form>

  <div class="card sec">
    <h2>Logo</h2>
    <p class="muted small">PNG or JPEG, printed at the top right. A wide logo works best.</p>
    {#if lh.has_logo}
      <img class="logo" src="/api/letterhead/logo?library={$library}&v={logoV}" alt="The logo" />
    {/if}
    <div class="actions">
      <label class="btn"><Icon name="upload" />{lh.has_logo ? 'Replace logo' : 'Upload logo'}<input type="file" accept="image/png,image/jpeg" onchange={pickLogo} hidden /></label>
      {#if lh.has_logo}<button class="btn ghost danger" onclick={removeLogo}><Icon name="trash" />Remove</button>{/if}
    </div>
  </div>
{/if}

<style>
  .two, .three { display: grid; gap: 12px; }
  @media (min-width: 640px) { .two { grid-template-columns: 1fr 1fr; } .three { grid-template-columns: 1fr 1fr 1fr; } }
  .logo { max-width: 260px; max-height: 110px; object-fit: contain; align-self: flex-start; background: #fff; padding: 8px; border-radius: 8px; border: 1px solid var(--line); }
</style>
