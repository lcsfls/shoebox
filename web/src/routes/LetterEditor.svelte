<script>
  import { onMount, untrack } from 'svelte';
  import { fade, slide } from 'svelte/transition';
  import { api } from '../lib/api.js';
  import { toast, canEdit, library } from '../lib/stores.js';
  import { navigate } from '../lib/router.js';
  import Icon from '../components/Icon.svelte';

  let { id } = $props();
  let l = $state(null);
  let pages = $state([]);
  let recipients = $state([]);
  let error = $state('');
  let saving = $state(false);
  let finishing = $state(false);
  let confirmFinish = $state(false);
  let pickRecipient = $state(false);

  const fields = ['form', 'lang', 'recipient', 'your_ref', 'our_ref', 'date', 'subject', 'salutation', 'body', 'closing', 'signature', 'enclosures'];
  const defaults = {
    de: { salutation: 'Sehr geehrte Damen und Herren,', closing: 'Mit freundlichen Grüßen' },
    en: { salutation: 'Dear Sir or Madam,', closing: 'Yours faithfully' },
  };
  const pick = (x) => Object.fromEntries(fields.map((k) => [k, x[k]]));
  let saved = $state('');

  onMount(async () => {
    try {
      l = await api.get(`/api/letters/${id}`);
      saved = JSON.stringify(pick(l));
      preview();
      api.get('/api/letters/recipients').then((r) => (recipients = r)).catch(() => {});
    } catch (e) { error = e.message; }
  });

  let locked = $derived(!l || l.status !== 'draft' || !$canEdit);
  let snapshot = $derived(l ? JSON.stringify(pick(l)) : '');

  // save and refresh the preview a moment after the last keystroke
  let timer;
  $effect(() => {
    const s = snapshot;
    if (!s || untrack(() => locked) || s === saved) return;
    clearTimeout(timer);
    timer = setTimeout(() => save().then(preview), 700);
    return () => clearTimeout(timer);
  });

  async function save() {
    if (locked) return;
    const s = JSON.stringify(pick(l));
    if (s === saved) return;
    saving = true;
    try { await api.put(`/api/letters/${id}`, pick(l)); saved = s; } catch (e) { toast(e.message, 'error'); }
    saving = false;
  }
  let previewing = 0;
  async function preview() {
    const n = ++previewing;
    try {
      const r = await api.post(`/api/letters/${id}/preview`, pick(l));
      if (n === previewing) pages = r.pages;
    } catch (e) { if (n === previewing) toast(e.message, 'error'); }
  }

  function setLang(lang) {
    const old = defaults[l.lang];
    if (old) {
      if (l.salutation === old.salutation) l.salutation = defaults[lang].salutation;
      if (l.closing === old.closing) l.closing = defaults[lang].closing;
    }
    l.lang = lang;
  }

  const pdfURL = () => `/api/letters/${id}/pdf?library=${$library}`;
  async function print() {
    const w = window.open('', '_blank');
    await save();
    if (w) w.location.href = pdfURL();
    else location.href = pdfURL();
  }
  async function finish() {
    finishing = true;
    try {
      await save();
      const r = await api.post(`/api/letters/${id}/finish`);
      toast('Finished and filed in the archive');
      l = await api.get(`/api/letters/${id}`);
      confirmFinish = false;
      return r;
    } catch (e) { toast(e.message, 'error', 6000); } finally { finishing = false; }
  }
  async function copy() {
    const r = await api.post('/api/letters', { from: l.id });
    navigate(`/letters/${r.id}`);
  }
  async function remove() {
    clearTimeout(timer);
    try { await api.del(`/api/letters/${id}`); toast('Draft deleted'); navigate('/letters'); } catch (e) { toast(e.message, 'error'); }
  }
</script>

<div class="page editor">
  <div class="page-head">
    <a class="btn ghost iconbtn" href="/letters" aria-label="All letters"><Icon name="back" /></a>
    <h1>{l?.subject || 'New letter'}</h1>
    {#if l}
      <span class="muted small state" aria-live="polite">{l.status !== 'draft' ? 'Finished' : saving ? 'Saving …' : snapshot === saved ? 'Saved' : ''}</span>
      <button class="btn" onclick={print}><Icon name="print" />Print</button>
      {#if l.status === 'draft' && $canEdit}
        <button class="btn primary" onclick={() => (confirmFinish = !confirmFinish)} aria-expanded={confirmFinish}><Icon name="check" />Finish</button>
      {:else if l.doc_id}
        <a class="btn" href="/doc/{l.doc_id}"><Icon name="folder" />In the archive</a>
        {#if $canEdit}<button class="btn" onclick={copy}><Icon name="copy" />Copy as new letter</button>{/if}
      {/if}
    {/if}
  </div>

  {#if confirmFinish && l?.status === 'draft'}
    <div class="card confirm" transition:slide={{ duration: 180 }}>
      <p class="grow">Finishing files the letter in the archive (tagged “{l.lang === 'en' ? 'Sent' : 'Postausgang'}”) and locks it. Later changes go into a copy.</p>
      <button class="btn primary" onclick={finish} disabled={finishing}>{finishing ? 'Filing …' : 'Finish and file'}</button>
      <button class="btn ghost" onclick={() => (confirmFinish = false)}>Not yet</button>
    </div>
  {/if}

  {#if error}
    <p class="card sec">{error}</p>
  {:else if l}
    <div class="cols">
      <form class="card form" onsubmit={(e) => e.preventDefault()}>
        <fieldset disabled={locked}>
          <div class="row">
            <div class="seg" role="radiogroup" aria-label="Layout">
              {#each [['B', 'Form B'], ['A', 'Form A']] as [v, t] (v)}
                <label class:on={l.form === v}><input type="radio" name="form" value={v} bind:group={l.form} />{t}</label>
              {/each}
            </div>
            <div class="seg" role="radiogroup" aria-label="Language">
              {#each [['de', 'Deutsch'], ['en', 'English']] as [v, t] (v)}
                <label class:on={l.lang === v}><input type="radio" name="lang" value={v} checked={l.lang === v} onchange={() => setLang(v)} />{t}</label>
              {/each}
            </div>
          </div>

          <div class="field-wrap">
            <label class="field">Recipient
              <textarea rows="4" bind:value={l.recipient} placeholder={'Company or name\nStreet 1\n12345 Town'}></textarea>
            </label>
            {#if recipients.length && !locked}
              <button type="button" class="btn small ghost earlier" onclick={() => (pickRecipient = !pickRecipient)} aria-expanded={pickRecipient}>Earlier recipients</button>
              {#if pickRecipient}
                <ul class="card picks" transition:fade={{ duration: 120 }}>
                  {#each recipients as r (r)}
                    <li><button type="button" onclick={() => { l.recipient = r; pickRecipient = false; }}>{r.split('\n').join(', ')}</button></li>
                  {/each}
                </ul>
              {/if}
            {/if}
          </div>

          <div class="three">
            <label class="field">Your reference<input type="text" bind:value={l.your_ref} /></label>
            <label class="field">Our reference<input type="text" bind:value={l.our_ref} /></label>
            <label class="field">Date<input type="date" bind:value={l.date} /></label>
          </div>
          <label class="field">Subject<input type="text" bind:value={l.subject} class="strong" /></label>
          <label class="field">Salutation<input type="text" bind:value={l.salutation} /></label>
          <label class="field">Text
            <textarea class="body" bind:value={l.body} placeholder="A blank line starts a new paragraph."></textarea>
          </label>
          <div class="two">
            <label class="field">Closing<input type="text" bind:value={l.closing} /></label>
            <label class="field">Name under the signature<input type="text" bind:value={l.signature} /></label>
          </div>
          <label class="field">Enclosures<textarea rows="2" bind:value={l.enclosures} placeholder="One per line"></textarea></label>
        </fieldset>
        {#if l.status === 'draft' && $canEdit}
          <div class="foot"><a class="small muted" href="/settings/letterhead">Edit the letterhead</a><button type="button" class="btn small ghost danger" onclick={remove}><Icon name="trash" />Delete draft</button></div>
        {/if}
      </form>

      <section class="preview" aria-label="Preview">
        {#each pages as src, i (i)}
          <img {src} alt="Page {i + 1}" class="sheet" />
        {:else}
          <div class="sheet blank" aria-hidden="true"></div>
        {/each}
      </section>
    </div>
  {/if}
</div>

<style>
  .editor { max-width: 1400px; }
  .page-head h1 { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .state { min-width: 60px; text-align: right; }
  .confirm { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; padding: 14px 16px; margin-bottom: 16px; border-color: var(--accent); }
  .confirm p { margin: 0; }
  .cols { display: grid; gap: 18px; }
  .form { padding: 18px; }
  fieldset { border: 0; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 14px; min-width: 0; }
  .row { display: flex; gap: 10px; flex-wrap: wrap; }
  .seg { display: inline-flex; border: 1px solid var(--line); border-radius: 10px; padding: 3px; background: var(--field); }
  .seg label { position: relative; padding: 6px 14px; border-radius: 7px; font-weight: 500; font-size: 0.9rem; cursor: pointer; color: var(--ink-2); }
  .seg label.on { background: var(--chip); color: var(--chip-ink); font-weight: 650; }
  .seg input { position: absolute; opacity: 0; inset: 0; margin: 0; cursor: pointer; }
  .seg label:has(input:focus-visible) { outline: 2px solid var(--accent); outline-offset: 1px; }
  .field-wrap { position: relative; }
  .earlier { position: absolute; top: -8px; right: 0; }
  .picks { position: absolute; z-index: 5; right: 0; top: 26px; list-style: none; margin: 0; padding: 4px; max-height: 260px; overflow: auto; width: min(100%, 420px); box-shadow: var(--shadow); }
  .picks button { all: unset; display: block; width: 100%; box-sizing: border-box; padding: 8px 10px; border-radius: 8px; cursor: pointer; font-size: 0.9rem; }
  .picks button:hover, .picks button:focus-visible { background: var(--chip); color: var(--chip-ink); }
  .three, .two { display: grid; gap: 12px; }
  .strong { font-weight: 600; }
  textarea.body { min-height: 300px; }
  .foot { display: flex; justify-content: space-between; align-items: center; margin-top: 14px; }
  .preview { display: flex; flex-direction: column; gap: 14px; align-items: center; }
  .sheet { width: 100%; max-width: 560px; aspect-ratio: 210 / 297; background: #fff; border-radius: 4px; box-shadow: var(--shadow); border: 1px solid var(--line); }
  @media (min-width: 640px) { .three { grid-template-columns: 1fr 1fr 1fr; } .two { grid-template-columns: 1fr 1fr; } }
  @media (min-width: 1100px) {
    .cols { grid-template-columns: minmax(0, 1fr) minmax(0, 520px); align-items: start; }
    .preview { position: sticky; top: 20px; max-height: calc(100dvh - 40px); overflow-y: auto; padding: 4px 4px 12px; }
  }
</style>
