<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast } from '../../lib/stores.js';
  import { loadSettings, saveSettings } from './useSettings.js';
  import Icon from '../../components/Icon.svelte';

  let s = $state(null);
  let keySet = $state(false);
  let langs = $state([]);
  let defaultPrompt = $state('');
  let key = $state('');
  let busy = $state(false);
  let test = $state(null);
  let testing = $state(false);
  // the archive from before: let the model read the documents without a summary
  let run = $state(null);
  let poller;
  async function runState() {
    clearTimeout(poller);
    try { run = await api.get('/api/ai/enrich'); } catch { run = null; }
    if (run?.running) poller = setTimeout(runState, 3000);
  }
  async function startRun() {
    try { await api.post('/api/ai/enrich'); runState(); } catch (err) { toast(err.message, 'error', 6000); }
  }
  async function stopRun() {
    await api.post('/api/ai/enrich?stop=1').catch(() => {});
    runState();
  }
  onMount(runState);
  // how far the search by meaning has indexed
  let sem = $state(null);
  let semTimer;
  async function semState() {
    clearTimeout(semTimer);
    try { sem = await api.get('/api/semantic'); } catch { sem = null; }
    if (sem?.on && sem.done < sem.total && !sem.error) semTimer = setTimeout(semState, 3000);
  }
  onMount(semState);
  $effect(() => () => clearTimeout(semTimer));
  $effect(() => () => clearTimeout(poller));

  onMount(async () => {
    const r = await loadSettings();
    s = r.settings;
    keySet = r.ai_key_set;
    langs = r.languages || [];
    defaultPrompt = r.default_prompt;
  });

  let chosen = $derived(s ? s.languages.split('+').filter(Boolean) : []);
  function toggleLang(l) {
    const set = new Set(chosen);
    set.has(l) ? set.delete(l) : set.add(l);
    s.languages = [...set].join('+') || 'eng';
  }

  async function save(e) {
    e?.preventDefault();
    busy = true;
    try {
      await saveSettings({ ...s, ai: { ...s.ai, api_key: key } });
      if (key) keySet = true;
      key = '';
      toast('Saved');
      setTimeout(semState, 1500); // a new embedding model starts indexing
    } catch (err) { toast(err.message, 'error'); } finally { busy = false; }
  }
  async function clearKey() {
    await saveSettings(s, { clear_key: true });
    keySet = false;
    toast('Key removed');
  }
  async function runTest() {
    testing = true;
    test = null;
    try {
      test = await api.post('/api/settings/ai-test', { ...s.ai, api_key: key });
    } catch (err) {
      test = { error: err.message };
    } finally {
      testing = false;
    }
  }
  const names = { deu: 'German', eng: 'English', fra: 'French', spa: 'Spanish', ita: 'Italian', nld: 'Dutch', pol: 'Polish', tur: 'Turkish', por: 'Portuguese' };
</script>

{#if s}
  <form class="card sec" onsubmit={save}>
    <h2>Tesseract</h2>
    <p class="muted">Reads every scan on the server and makes the PDF searchable. Pick the languages of your documents.</p>
    <div class="langs">
      {#each langs as l (l)}
        <label class="chip lang" class:on={chosen.includes(l)}><input type="checkbox" checked={chosen.includes(l)} onchange={() => toggleLang(l)} />{names[l] || l}</label>
      {/each}
      {#if !langs.length}<p class="error">Tesseract was not found on the server.</p>{/if}
    </div>
    <p class="hint">More languages: install e.g. <span class="mono">tesseract-ocr-fra</span> on the server.</p>
    <div class="actions"><button class="btn primary" disabled={busy}>Save</button></div>
  </form>

  <form class="card sec" onsubmit={save}>
    <h2><Icon name="sparkle" /> AI model <span class="muted small">optional</span></h2>
    <p class="muted">A local model reads documents better than Tesseract, especially bad photos, handwriting and tables. Any server with an OpenAI-compatible API works: vLLM, Ollama, llama.cpp, LM Studio. Use an OCR model such as Baidu's, or a general vision model. Tesseract still builds the searchable PDF; the model's text is used for search and sorting.</p>
    <label class="check"><input type="checkbox" bind:checked={s.ai.enabled} /><span>Use an AI model</span></label>
    <div class="two">
      <label class="field">Server address<input type="url" bind:value={s.ai.base_url} placeholder="http://gpu-box:8000/v1" /><span class="hint">Ends in /v1 for most servers</span></label>
      <label class="field">Model<input type="text" bind:value={s.ai.model} placeholder="the model name the server uses" /></label>
    </div>
    <label class="field">API key <span class="hint">{keySet ? 'stored, leave empty to keep it' : 'only if your server needs one'}</span>
      <input type="password" bind:value={key} autocomplete="off" placeholder={keySet ? '••••••••' : ''} />
    </label>
    {#if keySet}<div><button type="button" class="btn small ghost" onclick={clearKey}>Remove the stored key</button></div>{/if}
    <label class="check"><input type="checkbox" bind:checked={s.ai.only} />
      <span>Read with the model only, skip Tesseract<span class="hint">Faster on a small server, and the model's reading is used throughout. The PDF then has no hidden text layer: text can't be selected or searched in other PDF viewers (Shoebox's search still finds everything). When the model fails, Tesseract steps in.</span></span></label>
    <label class="check"><input type="checkbox" bind:checked={s.ai.fields} />
      <span>Also let it sort: title, date, sender, amount, tags, keywords, a summary and deadlines<span class="hint">Tags come from the library's own tags, with similar filed letters as examples, so there is nothing to train. Deadlines show on the start page under “To do”. Sorting rules still win. Needs a model that follows instructions (e.g. Qwen3-VL 8B Instruct, Gemma 3 12B); pure OCR models can't.</span></span></label>
    <label class="field">Prompt for reading a page<textarea bind:value={s.ai.prompt} rows="3" placeholder={defaultPrompt}></textarea><span class="hint">Empty uses the default. Some OCR models expect their own prompt; see the model's page.</span></label>
    <div class="two">
      <label class="field">Pages per document<input type="number" min="1" max="50" bind:value={s.ai.max_pages} /></label>
      <label class="field">Time limit per page (s)<input type="number" min="10" max="1800" bind:value={s.ai.timeout_sec} /></label>
    </div>
    <h3 class="sub-h">Search by meaning</h3>
    <p class="muted small">With an embedding model the search also finds what is meant, not only the words: “Fahrzeug” finds the “Kfz-Versicherung”, “Kündigung Handy” the letter from the phone company. Shoebox indexes the documents in the background.</p>
    <div class="two">
      <label class="field">Embedding model <span class="hint">empty = off; e.g. bge-m3</span><input type="text" bind:value={s.ai.embed_model} placeholder="bge-m3" autocomplete="off" /></label>
      <label class="field">Its server <span class="hint">only when not the one above</span><input type="url" bind:value={s.ai.embed_url} placeholder={s.ai.base_url || 'http://gpu-box:11434/v1'} class="mono" /></label>
    </div>
    {#if sem?.on}
      <p class="small" class:error={sem.error}>{sem.error ? `Indexing stopped: ${sem.error}` : sem.done < sem.total ? `Indexing: ${sem.done} of ${sem.total} documents …` : `All ${sem.total} documents are indexed.`}</p>
      {#if sem.done < sem.total && !sem.error}<progress max={sem.total} value={sem.done}></progress>{/if}
    {/if}
    <div class="actions">
      <button type="button" class="btn" onclick={runTest} disabled={testing || !s.ai.base_url}>{testing ? 'Testing …' : 'Test connection'}</button>
      <button class="btn primary" disabled={busy}>Save</button>
    </div>
    {#if test}
      {#if test.error}<p class="error">{test.error}</p>
      {:else}
        <p class:ok={test.model_found}>{test.model_found ? `Connected. “${s.ai.model}” is available.` : `Connected, but “${s.ai.model || '(no model)'}” is not offered.`}</p>
        {#if test.models?.length}<p class="small muted">Models on the server: <span class="mono">{test.models.join(', ')}</span></p>{/if}
      {/if}
    {/if}
  </form>

  {#if s.ai.enabled && s.ai.fields}
    <div class="card sec">
      <h2>Documents from before</h2>
      {#if run}
        {#if run.running}
          <p>The model is reading: {run.done + run.failed} of {run.total}{run.failed ? `, ${run.failed} failed` : ''}.</p>
          <progress max={run.total} value={run.done + run.failed}></progress>
          <div><button class="btn" onclick={stopRun}>Stop</button></div>
        {:else}
          <p class="muted">{run.pending ? `${run.pending} document${run.pending === 1 ? '' : 's'} in this library have no summary yet (from before the model, or from Paperless). The model can read them one after the other in the background: summary, keywords, deadline and fitting tags. What someone set by hand stays.` : 'Every document in this library has a summary.'}</p>
          {#if run.total && !run.running}<p class="small">Last run: {run.done} read{run.failed ? `, ${run.failed} failed (${run.error})` : ''}.</p>{/if}
          {#if run.pending}<div><button class="btn primary" onclick={startRun}>Read {run.pending} document{run.pending === 1 ? '' : 's'}</button></div>{/if}
        {/if}
      {/if}
    </div>
  {/if}
{/if}

<style>
  .sub-h { font-size: 1rem; margin: 10px 0 -4px; }
  .langs { display: flex; flex-wrap: wrap; gap: 8px; }
  .lang { cursor: pointer; min-height: 36px; padding: 0 14px; background: var(--card); border: 1px solid var(--line); color: var(--ink-2); }
  .lang.on { background: var(--chip); border-color: var(--chip); color: var(--chip-ink); }
  .lang input { position: absolute; opacity: 0; width: 1px; height: 1px; }
  .lang:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
  .two { display: grid; gap: 12px; }
  @media (min-width: 640px) { .two { grid-template-columns: 1fr 1fr; } }
  h2 { display: flex; align-items: center; gap: 8px; }
  .ok { color: var(--ok); font-weight: 600; }
</style>
