<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast } from '../../lib/stores.js';
  import { ago } from '../../lib/format.js';
  import { loadSettings, saveSettings } from './useSettings.js';
  import Icon from '../../components/Icon.svelte';

  let info = $state(null);
  let s = $state(null);
  let checking = $state(false);
  let starting = $state(false);
  let timer;

  const running = (st) => st && ['requested', 'downloading', 'installing', 'restarting'].includes(st.state) && Date.now() - Date.parse(st.time) < 15 * 60e3;

  async function load(check = false) {
    try {
      info = await api.get('/api/update' + (check ? '?check=1' : ''));
    } catch {
      // Shoebox restarts during an update: keep asking
    }
    clearTimeout(timer);
    if (running(info?.status)) timer = setTimeout(load, 2000);
  }
  onMount(async () => {
    load();
    s = (await loadSettings()).settings;
  });
  onDestroy(() => clearTimeout(timer));

  async function check() {
    checking = true;
    await load(true);
    checking = false;
  }
  async function start() {
    if (!confirm(`Update Shoebox to ${info.latest.tag}? It makes a backup first and is unavailable for a few seconds while it restarts.`)) return;
    starting = true;
    try {
      await api.post('/api/update', { version: info.latest.tag });
      toast('Backup made. Updating …');
      load();
    } catch (e) { toast(e.message, 'error', 6000); }
    starting = false;
  }
  async function save(e) {
    e.preventDefault();
    try { await saveSettings(s); toast('Saved'); } catch (err) { toast(err.message, 'error'); }
  }

  // after a restart the page still runs the old app: reload once it's done
  $effect(() => {
    if (info?.status?.state === 'done' && info.status.to === info.current && sessionStorage.getItem('sb-updated') !== info.current) {
      try { sessionStorage.setItem('sb-updated', info.current); } catch {}
      toast(`Updated to ${info.current}`);
      setTimeout(() => location.reload(), 1200);
    }
  });

  const stateText = { requested: 'Waiting for the updater …', downloading: 'Downloading …', installing: 'Installing …', restarting: 'Restarting …',
    done: 'Updated', failed: 'Update failed', rolled_back: 'Went back to the previous version' };
</script>

{#if info}
  <div class="card sec">
    <h2>Updates</h2>
    <div class="versions">
      <div><span class="muted small">Running</span><strong class="mono">{info.current}</strong></div>
      <div><span class="muted small">Newest release</span><strong class="mono">{info.latest?.tag || '—'}</strong></div>
    </div>

    {#if info.status && info.status.state !== 'done'}
      <p class="state" class:bad={info.status.state === 'failed' || info.status.state === 'rolled_back'} role="status">
        {#if running(info.status)}<span class="spin" aria-hidden="true"></span>{:else}<Icon name="warn" />{/if}
        <span><strong>{stateText[info.status.state] || info.status.state}</strong>{info.status.to ? ` (${info.status.to})` : ''}<br />
          <span class="small">{info.status.message} · {ago(info.status.time)}</span></span>
      </p>
    {/if}

    {#if info.error}
      <p class="muted">Couldn't ask GitHub for releases: {info.error}</p>
    {:else if !info.release}
      <p class="muted">This is a development build ({info.current}); it doesn't update itself.</p>
    {:else if info.newer}
      <p><strong>{info.latest.tag}</strong> is available{info.latest.published ? `, published ${ago(info.latest.published)}` : ''}.</p>
    {:else}
      <p class="ok"><Icon name="check" />Shoebox is up to date.</p>
    {/if}

    <div class="actions">
      {#if info.newer && info.installed}
        <button class="btn primary" onclick={start} disabled={starting || running(info.status)}><Icon name="download" />Update to {info.latest.tag}</button>
      {/if}
      <button class="btn" onclick={check} disabled={checking}><Icon name="refresh" />{checking ? 'Asking GitHub …' : 'Check now'}</button>
      {#if info.latest?.url}<a class="btn ghost" href={info.latest.url} target="_blank" rel="noopener">Release page</a>{/if}
    </div>

    {#if info.newer && info.latest.notes}
      <details open>
        <summary>What's new in {info.latest.tag}</summary>
        <div class="notes">{info.latest.notes}</div>
      </details>
    {/if}

    {#if !info.installed}
      <div class="how">
        <p class="small"><strong>This installation updates another way.</strong></p>
        <p class="small">Installed with the script: run it again once, then updates work from here.</p>
        <pre class="code">curl -fsSL https://raw.githubusercontent.com/{info.repo}/main/deploy/install.sh | sudo bash</pre>
        <p class="small">Docker: <span class="mono">git pull && docker compose up -d --build</span></p>
      </div>
    {/if}
  </div>

  {#if s && info.installed}
    <form class="card sec" onsubmit={save}>
      <h2>Automatic updates</h2>
      <label class="check"><input type="checkbox" bind:checked={s.update_auto} />
        <span>Install new releases by themselves<span class="hint">Once a night, with a backup first. If the new version doesn't start, Shoebox goes back to the one before.</span></span></label>
      <label class="field">At
        <select bind:value={s.update_hour} disabled={!s.update_auto}>
          {#each [0, 1, 2, 3, 4, 5, 6, 22, 23] as h (h)}<option value={h}>{String(h).padStart(2, '0')}:00</option>{/each}
        </select>
      </label>
      <p class="hint">Releases come from GitHub ({info.repo}). They aren't signed; each download is checked against the release's checksums.</p>
      <div class="actions"><button class="btn primary">Save</button></div>
    </form>
  {/if}
{:else}
  <div class="card sec"><p class="muted">Asking for the newest release …</p></div>
{/if}

<style>
  .versions { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
  .versions div { display: flex; flex-direction: column; gap: 2px; padding: 12px; background: var(--field); border-radius: 10px; }
  .versions strong { font-size: 1.15rem; }
  .state { display: flex; gap: 10px; align-items: flex-start; background: var(--chip); color: var(--chip-ink); padding: 10px 12px; border-radius: 10px; margin: 0; }
  .state.bad { background: var(--warn); color: var(--warn-ink); }
  .ok { display: flex; align-items: center; gap: 6px; color: var(--ok); margin: 0; }
  .notes { white-space: pre-wrap; font-size: 0.9rem; background: var(--field); border-radius: 10px; padding: 12px; margin-top: 8px; max-height: 360px; overflow: auto; }
  details summary { cursor: pointer; font-weight: 600; }
  .how { border-top: 1px solid var(--line); padding-top: 12px; display: flex; flex-direction: column; gap: 6px; }
  .how p { margin: 0; }
  .spin { width: 16px; height: 16px; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: spin 0.8s linear infinite; flex: none; margin-top: 3px; }
  @keyframes spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) { .spin { animation: none; } }
</style>
