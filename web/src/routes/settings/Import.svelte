<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast } from '../../lib/stores.js';
  import Icon from '../../components/Icon.svelte';

  let url = $state('');
  let token = $state('');
  let check = $state(null);
  let checking = $state(false);
  let original = $state(false);
  let status = $state(null);
  let timer;

  async function poll() {
    clearTimeout(timer);
    try { status = await api.get('/api/import/status'); } catch {}
    if (status?.running) timer = setTimeout(poll, 1500);
  }
  onMount(() => { poll(); return () => clearTimeout(timer); });

  async function runCheck(e) {
    e.preventDefault();
    checking = true;
    check = null;
    try { check = await api.post('/api/import/paperless/check', { url, token }); }
    catch (err) { check = { error: err.message }; }
    finally { checking = false; }
  }
  async function start(limit) {
    try {
      await api.post('/api/import/paperless/start', { url, token, limit, original });
      toast(limit ? `Importing the newest ${limit} documents …` : 'Importing everything …');
      poll();
    } catch (err) { toast(err.message, 'error'); }
  }
  async function stop() {
    await api.post('/api/import/paperless/stop');
    poll();
  }
  let pct = $derived(status?.total ? Math.round(((status.done + status.skipped + status.failed) / status.total) * 100) : 0);
</script>

<div class="card sec">
  <h2>Import from Paperless-ngx</h2>
  <p class="muted">Shoebox copies documents with their tags, correspondents (as senders), document types (as tags “type:…”), dates, notes and custom fields, and turns matching rules into sorting rules. Paperless is only read, never changed. Documents already imported are skipped, so you can start with a test and run it again.</p>
  <form class="stack" onsubmit={runCheck}>
    <label class="field">Paperless address<input type="url" bind:value={url} placeholder="http://paperless.local:8000" required /></label>
    <label class="field">API token <span class="hint">In Paperless: your profile (top right) → API Auth Token</span>
      <input type="password" bind:value={token} autocomplete="off" required /></label>
    <div class="actions"><button class="btn" disabled={checking}>{checking ? 'Connecting …' : 'Connect and count'}</button></div>
  </form>

  {#if check}
    {#if check.error}<p class="error">{check.error}</p>
    {:else}
      <div class="counts">
        <div><strong>{check.documents}</strong><span>documents</span></div>
        <div><strong>{check.tags}</strong><span>tags</span></div>
        <div><strong>{check.correspondents}</strong><span>correspondents</span></div>
        <div><strong>{check.document_types}</strong><span>document types</span></div>
        <div><strong>{check.rules}</strong><span>matching rules</span></div>
        <div><strong>{check.custom_fields >= 0 ? check.custom_fields : '–'}</strong><span>custom fields</span></div>
      </div>
      {#if check.auto_rules}<p class="notice">{check.auto_rules} item{check.auto_rules === 1 ? '' : 's'} use Paperless' learned “Auto” matching. That can't be copied; add a sorting rule where it matters.</p>{/if}
      <label class="check"><input type="checkbox" bind:checked={original} />
        <span>Take the original files instead of Paperless' archived PDFs<span class="hint">Normally the archived PDF is better: it already has the text layer.</span></span></label>
      <div class="actions">
        <button class="btn" onclick={() => start(50)} disabled={status?.running}>Test with the newest 50</button>
        <button class="btn primary" onclick={() => start(0)} disabled={status?.running}>Import everything</button>
      </div>
    {/if}
  {/if}
</div>

{#if status && (status.running || status.finished)}
  <div class="card sec">
    <h2>{status.running ? 'Importing …' : 'Last import'}</h2>
    <div class="progress" role="progressbar" aria-valuenow={pct} aria-valuemin="0" aria-valuemax="100"><span style="width:{pct}%"></span></div>
    <p>{status.done} imported · {status.skipped} skipped (already there) · {status.failed} failed{status.total ? ` · ${status.total} in total` : ''}</p>
    {#if status.rules}<p class="small muted">{status.rules} sorting rule{status.rules === 1 ? '' : 's'} created.</p>{/if}
    {#if status.running && status.current}<p class="small muted">Now: {status.current}</p>{/if}
    {#if status.running}<div class="actions"><button class="btn" onclick={stop}>Stop</button></div>{/if}
    {#if status.errors?.length}
      <details><summary>{status.errors.length} problem{status.errors.length === 1 ? '' : 's'}</summary>
        <ul class="errs">{#each status.errors as e}<li class="small">{e}</li>{/each}</ul></details>
    {/if}
  </div>
{/if}

<style>
  .counts { display: grid; grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); gap: 10px; }
  .counts div { background: var(--bg); border-radius: 10px; padding: 10px 12px; display: flex; flex-direction: column; }
  .counts strong { font-size: 1.4rem; }
  .counts span { color: var(--muted); font-size: 0.85rem; }
  .progress { height: 10px; border-radius: 999px; background: var(--bg); overflow: hidden; }
  .progress span { display: block; height: 100%; background: var(--accent); transition: width 0.4s; }
  .errs { margin: 8px 0 0; padding-left: 18px; }
</style>
