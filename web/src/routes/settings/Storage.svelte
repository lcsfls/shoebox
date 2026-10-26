<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast, counts } from '../../lib/stores.js';
  import { formatSize, ago } from '../../lib/format.js';
  import { loadSettings, saveSettings } from './useSettings.js';
  import Icon from '../../components/Icon.svelte';

  let info = $state(null);
  let s = $state(null);
  let busy = $state(false);

  async function load() {
    [info, s] = await Promise.all([api.get('/api/storage'), loadSettings().then((r) => r.settings)]);
  }
  onMount(load);

  async function save(e) {
    e.preventDefault();
    busy = true;
    try { await saveSettings(s); toast('Saved'); } catch (err) { toast(err.message, 'error'); } finally { busy = false; }
  }
  async function rescan() {
    await api.post('/api/storage/rescan');
    toast('Looking through the archive folder …');
    setTimeout(load, 4000);
  }
  let network = $derived(info && /^(nfs|cifs|smb)/.test(info.fs || ''));
</script>

{#if info}
  <div class="card sec">
    <h2>Archive folder</h2>
    <p class="mono path">{info.archive}</p>
    <ul class="facts">
      <li class:bad={!info.writable}><Icon name={info.writable ? 'check' : 'warn'} />{info.writable ? 'Shoebox can write here' : `Shoebox can't write here: ${info.write_error}`}</li>
      <li><Icon name="folder" />{info.fs ? `File system: ${info.fs}${network ? ' (network share)' : ''}` : 'File system unknown'}</li>
      {#if info.total}<li><Icon name="pages" />{formatSize(info.free)} free of {formatSize(info.total)}</li>{/if}
      <li><Icon name="refresh" />{info.last_scan ? `Last look: ${ago(info.last_scan)}${info.last_found ? `, ${info.last_found} new` : ''}` : 'Not looked through yet'}</li>
      {#if info.missing}<li class="bad"><Icon name="warn" />{info.missing} document{info.missing === 1 ? '' : 's'} no longer in the folder</li>{/if}
      {#if info.scan_error}<li class="bad"><Icon name="warn" />{info.scan_error}</li>{/if}
    </ul>
    <div class="actions"><button class="btn" onclick={rescan}><Icon name="refresh" />Look through now</button></div>
    <p class="hint">The database stays at <span class="mono">{info.data}</span> on the local disk. Only the PDFs live in the archive folder; never put the database on a network share.</p>
  </div>
{/if}

{#if s}
  <form class="card sec" onsubmit={save}>
    <h2>Files you add yourself</h2>
    <p class="muted">Copy a PDF into the archive folder, for example onto the NAS into <span class="mono">2026/01</span>, and Shoebox adds it to the library: it reads the text, takes the month from the folder and keeps the file where you put it.</p>
    <label class="field">Look for new files every
      <select bind:value={s.watch_minutes}>
        <option value={0}>never (only when you press “Look through now”)</option>
        <option value={1}>minute</option><option value={5}>5 minutes</option><option value={15}>15 minutes</option><option value={60}>hour</option>
      </select>
    </label>
    <label class="check"><input type="checkbox" bind:checked={s.watch_ocr} />
      <span>Add a text layer to scanned PDFs without one<span class="hint">The file is replaced by a searchable version; the untouched original is kept on the server under <span class="mono">originals/</span>.</span></span></label>
    <div class="actions"><button class="btn primary" disabled={busy}>Save</button></div>
  </form>
{/if}

<div class="card sec">
  <h2>Using a NAS (NFS)</h2>
  <p class="muted">Shoebox runs without root rights, so it doesn't mount shares itself. The share is mounted outside and handed in as the archive folder.</p>
  <h3>Proxmox LXC container</h3>
  <p class="small">Mount the share on the Proxmox host (Datacenter → Storage → Add → NFS, or <span class="mono">/etc/fstab</span>), then pass the folder into the container. The install script asks for this; by hand:</p>
  <pre class="code">pct set &lt;ID&gt; -mp0 /mnt/pve/nas/documents,mp=/srv/shoebox/archive</pre>
  <p class="small">In an unprivileged container Shoebox writes as user 101000 on the host: give that user write access on the NAS, or map all users to one account (“squash”) for the share.</p>
  <h3>Docker</h3>
  <pre class="code">volumes:
  archive:
    driver: local
    driver_opts:
      type: nfs
      o: addr=nas.local,nfsvers=4.1,rw
      device: ":/volume1/documents"</pre>
</div>

<style>
  .path { word-break: break-all; font-size: 0.9rem; }
  .facts { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .facts li { display: flex; gap: 10px; align-items: center; }
  .facts li.bad { color: var(--danger); }
  h3 { font-size: 0.95rem; margin: 4px 0 -6px; }
</style>
