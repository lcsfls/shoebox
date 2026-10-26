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
    <label class="check"><input type="checkbox" bind:checked={s.ai.fields} />
      <span>Also ask for title, date, sender and amount<span class="hint">Sorting rules still win. Needs a model that can follow instructions; pure OCR models can't.</span></span></label>
    <label class="field">Prompt for reading a page<textarea bind:value={s.ai.prompt} rows="3" placeholder={defaultPrompt}></textarea><span class="hint">Empty uses the default. Some OCR models expect their own prompt; see the model's page.</span></label>
    <div class="two">
      <label class="field">Pages per document<input type="number" min="1" max="50" bind:value={s.ai.max_pages} /></label>
      <label class="field">Time limit per page (s)<input type="number" min="10" max="1800" bind:value={s.ai.timeout_sec} /></label>
    </div>
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
{/if}

<style>
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
