<script>
  // Statistics for a server of one's own: read from /api/metrics, or sent by Shoebox.
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast } from '../../lib/stores.js';
  import { formatDate } from '../../lib/format.js';
  import { loadSettings, saveSettings } from './useSettings.js';
  import Icon from '../../components/Icon.svelte';

  let s = $state(null);
  let headerSet = $state(false);
  let clearHeader = $state(false);
  let busy = $state(false);
  let status = $state({});
  let example = $state('prometheus');
  const base = location.origin;
  const host = location.host;

  onMount(async () => {
    const r = await loadSettings();
    s = r.settings;
    headerSet = r.metrics_header_set;
    status = await api.get('/api/metrics/push').catch(() => ({}));
  });

  async function save(e) {
    e?.preventDefault();
    busy = true;
    try {
      await saveSettings(s, { clear_metrics_header: clearHeader });
      if (s.metrics.header || clearHeader) headerSet = !clearHeader;
      s.metrics.header = '';
      clearHeader = false;
      toast(s.metrics.url ? `Saved. Shoebox sends the numbers every ${s.metrics.minutes} min.` : 'Saved. Sending is off.');
    } catch (err) { toast(err.message, 'error'); } finally { busy = false; }
  }
  async function sendNow() {
    busy = true;
    try {
      await api.post('/api/metrics/push', s.metrics);
      toast('Sent. The server took it.');
    } catch (err) { toast(err.message, 'error', 8000); } finally {
      busy = false;
      status = await api.get('/api/metrics/push').catch(() => ({}));
    }
  }

  const formats = [
    ['json', 'JSON', 'webhooks, n8n, Node-RED, Home Assistant, your own server'],
    ['influx', 'InfluxDB line protocol', 'InfluxDB 1 and 2, VictoriaMetrics, QuestDB, Telegraf'],
    ['prometheus', 'Prometheus text', 'a Pushgateway, VictoriaMetrics'],
  ];
  const urlHints = {
    json: 'https://n8n.example.org/webhook/shoebox',
    influx: 'http://influxdb:8086/api/v2/write?org=home&bucket=shoebox&precision=ns',
    prometheus: 'http://pushgateway:9091/metrics/job/shoebox',
  };
  const headerHints = { json: 'X-Api-Key: …', influx: 'Authorization: Token …', prometheus: 'Authorization: Basic …' };

  let examples = $derived({
    prometheus: `# prometheus.yml\nscrape_configs:\n  - job_name: shoebox\n    scheme: ${location.protocol.replace(':', '')}\n    metrics_path: /api/metrics\n    authorization:\n      credentials: sb_…   # a read-only token of an admin\n    static_configs:\n      - targets: ['${host}']`,
    homeassistant: `# configuration.yaml\nrest:\n  - resource: ${base}/api/metrics?format=json\n    headers:\n      Authorization: Bearer sb_…\n    scan_interval: 300\n    sensor:\n      - name: Shoebox documents\n        value_template: "{{ value_json.documents }}"\n      - name: Shoebox to review\n        value_template: "{{ value_json.inbox }}"\n      - name: Shoebox free space\n        value_template: "{{ (value_json.archive_free_bytes / 1e9) | round(1) }}"\n        unit_of_measurement: GB`,
    telegraf: `# telegraf.conf\n[[inputs.prometheus]]\n  urls = ["${base}/api/metrics"]\n  bearer_token_string = "sb_…"`,
    curl: `curl -H "Authorization: Bearer sb_…" ${base}/api/metrics\ncurl -H "Authorization: Bearer sb_…" "${base}/api/metrics?format=json"\ncurl -H "Authorization: Bearer sb_…" "${base}/api/metrics?format=influx"\n\n# tools that can't send a header (Uptime Kuma's JSON query, simple monitors):\n${base}/api/metrics?format=json&token=sb_…   # read-only tokens only`,
  });
</script>

<div class="card sec">
  <h2>Read by your server</h2>
  <p class="muted">Shoebox gives its numbers at <span class="mono">/api/metrics</span>: documents per library and state, pages, size, how they came in, text recognition time, the work queue, accounts, free space, the last folder look-through and backup, and whether an update is out. Shared libraries one by one; the private Homes only as their sum.</p>
  <table class="eps">
    <tbody>
      <tr><td class="mono">/api/metrics</td><td>Prometheus / OpenMetrics text: Prometheus, Grafana Alloy, VictoriaMetrics, Netdata, Zabbix, Datadog, Telegraf</td></tr>
      <tr><td class="mono">?format=json</td><td>JSON with the totals at the top: Home Assistant, Uptime Kuma, n8n, scripts</td></tr>
      <tr><td class="mono">?format=influx</td><td>InfluxDB line protocol</td></tr>
    </tbody>
  </table>
  <p class="small">The key is an admin's API token; a <a href="/settings/api">read-only one</a> is enough and the safest. <a href="/api/metrics?format=json" target="_blank" rel="noopener">See the numbers now <Icon name="expand" size={14} /></a></p>
  <div class="seg" role="tablist" aria-label="Examples">
    {#each [['prometheus', 'Prometheus'], ['homeassistant', 'Home Assistant'], ['telegraf', 'Telegraf'], ['curl', 'curl & others']] as [k, l]}
      <button role="tab" aria-selected={example === k} class:on={example === k} onclick={() => (example = k)}>{l}</button>
    {/each}
  </div>
  <pre class="code">{examples[example]}</pre>
</div>

{#if s}
  <form class="card sec" onsubmit={save}>
    <h2>Send to a server</h2>
    <p class="muted">Or Shoebox sends the numbers by itself, as a POST, for servers that don't come and fetch them.</p>
    <label class="field">Address <span class="hint">empty = off</span>
      <input type="url" bind:value={s.metrics.url} placeholder={urlHints[s.metrics.format]} class="mono" />
    </label>
    <div class="two">
      <label class="field">Format
        <select bind:value={s.metrics.format}>
          {#each formats as [k, l, hint] (k)}<option value={k}>{l} – {hint}</option>{/each}
        </select>
      </label>
      <label class="field">Every … minutes<input type="number" min="1" max="1440" bind:value={s.metrics.minutes} /></label>
    </div>
    <label class="field">Extra header <span class="hint">{headerSet ? 'one is saved; type a new one to replace it' : 'for a password or token, e.g. ' + headerHints[s.metrics.format]}</span>
      <input type="password" bind:value={s.metrics.header} placeholder={headerSet ? '••••••••' : headerHints[s.metrics.format]} autocomplete="off" class="mono" />
    </label>
    {#if headerSet}<label class="check"><input type="checkbox" bind:checked={clearHeader} /> <span>Remove the saved header</span></label>{/if}
    {#if status.last || status.error}
      <p class="small" class:error={status.error}>{status.error ? `Last try failed: ${status.error}` : `Last sent ${formatDate(status.last.slice(0, 10))}, ${status.last.slice(11, 16)} UTC.`}</p>
    {/if}
    <div class="actions">
      <button type="button" class="btn" onclick={sendNow} disabled={busy || !s.metrics.url}>Send now</button>
      <button class="btn primary" disabled={busy}>Save</button>
    </div>
  </form>
{/if}

<style>
  .eps { border-collapse: collapse; width: 100%; font-size: 0.9rem; }
  .eps td { padding: 8px 8px 8px 0; border-top: 1px solid var(--line); vertical-align: top; }
  .eps td:first-child { white-space: nowrap; padding-right: 16px; }
  .code { background: var(--bg); border: 1px solid var(--line); border-radius: 10px; padding: 12px; font-family: var(--mono); font-size: 0.8rem; overflow: auto; margin: 0; white-space: pre; }
  .seg { display: flex; flex-wrap: wrap; gap: 4px; background: var(--field); border-radius: 10px; padding: 3px; width: fit-content; max-width: 100%; }
  .seg button { border: 0; background: transparent; color: var(--ink-2); font: inherit; font-size: 0.85rem; min-height: 34px; padding: 0 12px; border-radius: 8px; cursor: pointer; }
  .seg button.on { background: var(--card); color: var(--ink); font-weight: 600; box-shadow: 0 1px 2px rgb(0 0 0 / 0.08); }
  .two { display: grid; gap: 12px; }
  @media (min-width: 640px) { .two { grid-template-columns: 2fr 1fr; } }
  .error { color: var(--danger, #B3261E); }
  @media (max-width: 640px) { .eps td { display: block; border: 0; padding: 2px 0; } .eps tr { display: block; border-top: 1px solid var(--line); padding: 8px 0; } }
</style>
