<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast } from '../../lib/stores.js';
  import { formatDate, formatAmount } from '../../lib/format.js';
  import TagInput from '../../components/TagInput.svelte';
  import Icon from '../../components/Icon.svelte';

  let rules = $state([]);
  let tags = $state([]);
  let senders = $state([]);
  let edit = $state(null);
  let sample = $state('');
  let result = $state(null);
  let suggestions = $state([]);

  const kinds = [
    ['any', 'any of the words'], ['all', 'all of the words'], ['exact', 'exactly this text'],
    ['regex', 'regular expression'], ['fuzzy', 'roughly this text (OCR mistakes)'],
  ];

  async function load() {
    [rules, tags, senders] = await Promise.all([
      api.get('/api/rules'), api.get('/api/tags').then((t) => t.map((x) => x.name)), api.get('/api/senders').then((s) => s.map((x) => x.name)),
    ]);
  }
  async function loadSuggestions() { suggestions = await api.get('/api/rules/suggestions').catch(() => []); }
  onMount(() => { load(); loadSuggestions(); });

  async function accept(sg) {
    try {
      await api.post('/api/rules', { name: sg.sender, kind: 'exact', pattern: sg.pattern, nocase: true, sender: sg.sender, tags: sg.tags, title: '', position: 0, enabled: true });
      toast(`Rule for ${sg.sender} added`);
      load(); loadSuggestions();
    } catch (err) { toast(err.message, 'error'); }
  }
  async function dismiss(sg) {
    await api.post('/api/rules/suggestions/dismiss', { key: sg.key });
    suggestions = suggestions.filter((x) => x.key !== sg.key);
  }

  function newRule() {
    edit = { id: 0, name: '', kind: 'any', pattern: '', nocase: true, sender: '', tags: [], title: '', position: 0, enabled: true };
  }

  async function save(e) {
    e.preventDefault();
    try {
      if (edit.id) await api.put(`/api/rules/${edit.id}`, edit);
      else await api.post('/api/rules', edit);
      edit = null;
      toast('Rule saved');
      load();
    } catch (err) { toast(err.message, 'error'); }
  }
  async function remove(r) {
    if (!confirm(`Delete the rule “${r.name}”?`)) return;
    await api.del(`/api/rules/${r.id}`);
    load();
  }
  async function runTest() {
    result = await api.post('/api/rules/test', { text: sample }).catch((e) => ({ error: e.message }));
  }
</script>

{#if suggestions.length}
  <div class="card sec">
    <h2><Icon name="sparkle" />Suggested rules</h2>
    <p class="muted">Lines that appear in the letters of one sender and in no others. As a rule, they file the next letter of that sender without asking.</p>
    <ul class="suggest">
      {#each suggestions as sg (sg.key)}
        <li>
          <div class="grow">
            <p><strong>{sg.sender}</strong> <span class="muted small">· in {sg.covered} of {sg.docs} letters</span></p>
            <p class="small">When the text has <span class="mono quote">{sg.pattern}</span>{#if sg.tags.length}, tag it {sg.tags.join(', ')}{/if}</p>
          </div>
          <button class="btn small primary" onclick={() => accept(sg)}><Icon name="plus" size={16} />Add rule</button>
          <button class="btn small ghost" onclick={() => dismiss(sg)}>Not now</button>
        </li>
      {/each}
    </ul>
  </div>
{/if}

<div class="card sec">
  <h2>Sorting rules</h2>
  <p class="muted">When a document's text matches, Shoebox sets the sender, adds tags or a title. With a matching rule and a date, documents are filed without review. Rules from Paperless are imported with it.</p>
  <div class="actions"><button class="btn primary" onclick={newRule}><Icon name="plus" />New rule</button></div>

  {#if edit}
    <form class="editor stack" onsubmit={save}>
      <label class="field">Look for
        <input type="text" bind:value={edit.pattern} placeholder="e.g. Stadtwerke" required />
      </label>
      <div class="two">
        <label class="field">Match
          <select bind:value={edit.kind}>{#each kinds as [k, l] (k)}<option value={k}>{l}</option>{/each}</select>
        </label>
        <label class="field">Name <span class="hint">optional</span><input type="text" bind:value={edit.name} /></label>
      </div>
      <label class="check"><input type="checkbox" bind:checked={edit.nocase} /><span>Ignore upper and lower case</span></label>
      <label class="field">Then set the sender
        <input type="text" bind:value={edit.sender} list="rule-senders" autocomplete="off" />
        <datalist id="rule-senders">{#each senders as s (s)}<option value={s}></option>{/each}</datalist>
      </label>
      <div class="field-wrap"><label class="lbl" for="rule-tags">and add tags</label><TagInput bind:tags={edit.tags} suggestions={tags} id="rule-tags" /></div>
      <label class="field">and use the title <span class="hint">optional</span><input type="text" bind:value={edit.title} placeholder="e.g. Stromrechnung" /></label>
      <label class="check"><input type="checkbox" bind:checked={edit.enabled} /><span>Active</span></label>
      <div class="actions">
        <button type="button" class="btn" onclick={() => (edit = null)}>Cancel</button>
        <button class="btn primary">Save rule</button>
      </div>
    </form>
  {/if}

  {#if rules.length}
    <ul class="rules">
      {#each rules as r (r.id)}
        <li class:off={!r.enabled}>
          <span class="grow">
            <strong>{r.name}</strong>
            <span class="small muted">{kinds.find((k) => k[0] === r.kind)?.[1]}: <span class="mono">{r.pattern}</span></span>
            <span class="small">→ {[r.sender && `sender ${r.sender}`, r.tags.length && `tags ${r.tags.join(', ')}`, r.title && `title “${r.title}”`].filter(Boolean).join(' · ') || 'nothing'}</span>
          </span>
          <button class="btn small" onclick={() => (edit = { ...r, tags: [...r.tags] })}>Edit</button>
          <button class="btn small ghost danger iconbtn" onclick={() => remove(r)} aria-label="Delete rule {r.name}"><Icon name="trash" /></button>
        </li>
      {/each}
    </ul>
  {:else if !edit}
    <p class="muted">No rules yet.</p>
  {/if}
</div>

<div class="card sec">
  <h2>Try it</h2>
  <label class="field">Paste the text of a letter<textarea bind:value={sample} rows="5" placeholder="Stadtwerke Musterstadt … Rechnungsdatum 24.10.2026 … Gesamtbetrag 84,20 €"></textarea></label>
  <div class="actions"><button class="btn" onclick={runTest} disabled={!sample.trim()}>Check</button></div>
  {#if result}
    {#if result.error}<p class="error">{result.error}</p>
    {:else}
      <dl>
        <dt>Rule</dt><dd>{result.rule || '—'}</dd>
        <dt>Sender</dt><dd>{result.sender || '—'}</dd>
        <dt>Title</dt><dd>{result.title || '—'}</dd>
        <dt>Date</dt><dd>{result.date ? formatDate(result.date) : '—'}</dd>
        <dt>Amount</dt><dd>{result.amount_cents != null ? formatAmount(result.amount_cents) : '—'}</dd>
        <dt>Tags</dt><dd>{result.tags?.join(', ') || '—'}</dd>
      </dl>
    {/if}
  {/if}
</div>

<style>
  .suggest { list-style: none; margin: 0; padding: 0; }
  .suggest li { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 10px 0; border-top: 1px solid var(--line); }
  .suggest p { margin: 0; }
  .quote { background: var(--field); padding: 1px 6px; border-radius: 6px; overflow-wrap: anywhere; }
  h2 :global(svg) { vertical-align: -3px; margin-right: 6px; color: var(--accent); }
  .editor { padding: 14px; border: 1px dashed var(--line-strong); border-radius: 12px; }
  .two { display: grid; gap: 12px; }
  @media (min-width: 640px) { .two { grid-template-columns: 1fr 1fr; } }
  .field-wrap { display: flex; flex-direction: column; gap: 6px; }
  .lbl { font-size: 0.9rem; font-weight: 600; }
  .rules { list-style: none; margin: 0; padding: 0; }
  .rules li { display: flex; align-items: center; gap: 8px; padding: 12px 0; border-top: 1px solid var(--line); }
  .rules li.off { opacity: 0.55; }
  .rules .grow { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  .rules .mono { overflow-wrap: anywhere; }
  dl { display: grid; grid-template-columns: auto 1fr; gap: 6px 16px; margin: 0; }
  dt { color: var(--muted); }
  dd { margin: 0; font-weight: 500; }
</style>
