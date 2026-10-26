<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast, user, can } from '../../lib/stores.js';
  import { formatDate } from '../../lib/format.js';
  import Icon from '../../components/Icon.svelte';

  let tokens = $state([]);
  let name = $state('');
  let readonly = $state(!can($user, 'member'));
  let created = $state('');
  const base = location.origin;

  async function load() { tokens = await api.get('/api/tokens'); }
  onMount(load);

  async function create(e) {
    e.preventDefault();
    try {
      const r = await api.post('/api/tokens', { name, readonly });
      created = r.token;
      name = '';
      load();
    } catch (err) { toast(err.message, 'error'); }
  }
  async function revoke(t) {
    if (!confirm(`Revoke “${t.name}”? Scripts using it stop working.`)) return;
    await api.del(`/api/tokens/${t.id}`);
    load();
  }
  async function copy() {
    try { await navigator.clipboard.writeText(created); toast('Copied'); } catch { toast('Select the token and copy it by hand.', 'error'); }
  }
  let tok = $derived(created || 'sb_…');
</script>

<div class="card sec">
  <h2>API tokens</h2>
  <p class="muted">Scripts, a scanner on the network or another program can use Shoebox with a token. It acts with your account's rights ({$user.role}); a read-only token can only search and download.</p>
  {#if tokens.length}
    <ul class="rows">
      {#each tokens as t (t.id)}
        <li><Icon name="code" /><span class="grow">{t.name} <span class="mono muted">{t.prefix}…</span>
          <span class="muted small"> · {t.readonly ? 'read-only' : 'read and write'} · created {formatDate(t.created_at.slice(0, 10))}{t.last_used_at ? ` · last used ${formatDate(t.last_used_at.slice(0, 10))}` : ' · not used yet'}</span></span>
          <button class="btn small ghost danger iconbtn" onclick={() => revoke(t)} aria-label="Revoke {t.name}"><Icon name="trash" /></button></li>
      {/each}
    </ul>
  {/if}
  {#if created}
    <div class="fresh" role="status">
      <p><strong>Your new token.</strong> Copy it now: Shoebox keeps only a fingerprint of it and can't show it again.</p>
      <div class="inline"><input class="mono grow" readonly value={created} onfocus={(e) => e.target.select()} aria-label="New API token" /><button class="btn" onclick={copy}>Copy</button></div>
    </div>
  {/if}
  <form class="inline" onsubmit={create}>
    <label class="field grow">Name <span class="hint">what uses it, e.g. “Scanner in the office”</span><input type="text" bind:value={name} maxlength="60" required /></label>
    <label class="check"><input type="checkbox" bind:checked={readonly} disabled={!can($user, 'member')} />Read-only</label>
    <button class="btn primary">Create token</button>
  </form>
</div>

<div class="card sec">
  <h2>Using the API</h2>
  <p class="muted">Send the token in the <span class="mono">Authorization</span> header. Answers are JSON; the app itself uses the same endpoints.</p>
  <h3>Search</h3>
  <pre class="code">curl -H "Authorization: Bearer {tok}" \
  "{base}/api/docs?q=stromrechnung&year=2026"</pre>
  <h3>Upload PDFs or photos</h3>
  <pre class="code">curl -H "Authorization: Bearer {tok}" \
  -F files=@scan.pdf -F files=@seite2.jpg {base}/api/upload</pre>
  <h3>Download a document</h3>
  <pre class="code">curl -H "Authorization: Bearer {tok}" -o brief.pdf {base}/api/docs/42/pdf</pre>
  <h3>Endpoints</h3>
  <table class="eps">
    <tbody>
      <tr><td class="mono">GET /api/docs</td><td><span class="mono">q</span>, <span class="mono">status</span> (inbox, filed), <span class="mono">year</span>, <span class="mono">month</span>, <span class="mono">tag</span>, <span class="mono">sender</span>, <span class="mono">sort</span> (newest date by default, or added, relevance, sender, amount), <span class="mono">limit</span>, <span class="mono">offset</span></td></tr>
      <tr><td class="mono">GET /api/docs/&#123;id&#125;</td><td>one document with its facts and tags</td></tr>
      <tr><td class="mono">GET /api/docs/&#123;id&#125;/pdf</td><td>the PDF</td></tr>
      <tr><td class="mono">PATCH /api/docs/&#123;id&#125;</td><td>change <span class="mono">title</span>, <span class="mono">sender</span>, <span class="mono">date</span>, <span class="mono">amount_cents</span>, <span class="mono">tags</span>, <span class="mono">notes</span></td></tr>
      <tr><td class="mono">POST /api/docs/&#123;id&#125;/file</td><td>file a document from Review into the archive</td></tr>
      <tr><td class="mono">POST /api/upload</td><td>multipart field <span class="mono">files</span>, one document per file</td></tr>
      <tr><td class="mono">GET /api/jobs</td><td>what is being read right now</td></tr>
      <tr><td class="mono">GET /api/tags</td><td>tags with counts; <span class="mono">/api/senders</span> the same for senders</td></tr>
      <tr><td class="mono">GET /api/stats</td><td>the numbers behind the statistics page</td></tr>
    </tbody>
  </table>
</div>

<style>
  .rows { list-style: none; margin: 0; padding: 0; }
  .rows li { display: flex; align-items: center; gap: 10px; padding: 10px 0; border-top: 1px solid var(--line); }
  .inline { display: flex; gap: 10px; align-items: end; flex-wrap: wrap; }
  .inline .check { min-height: 44px; display: inline-flex; align-items: center; gap: 8px; }
  .fresh { background: var(--warn); color: var(--warn-ink); border-radius: 10px; padding: 12px; display: flex; flex-direction: column; gap: 8px; }
  .fresh p { margin: 0; }
  .fresh input { min-height: 40px; border-radius: 8px; border: 1px solid var(--line-strong); padding: 0 10px; background: var(--card); color: var(--ink); }
  h3 { font-size: 0.95rem; margin: 6px 0 -6px; }
  .eps { border-collapse: collapse; width: 100%; font-size: 0.9rem; }
  .eps td { padding: 8px 8px 8px 0; border-top: 1px solid var(--line); vertical-align: top; }
  .eps td:first-child { white-space: nowrap; padding-right: 16px; }
  @media (max-width: 640px) { .eps td { display: block; border: 0; padding: 2px 0; } .eps tr { display: block; border-top: 1px solid var(--line); padding: 8px 0; } }
</style>
