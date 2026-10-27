<script>
  // The fields of a document: used on the review screen and the document page.
  // On a desktop, the pen next to a field opens the page to mark its value.
  import TagInput from './TagInput.svelte';
  import Marker from './Marker.svelte';
  import Icon from './Icon.svelte';
  import { amountInput } from '../lib/format.js';
  let { form = $bindable(), senders = [], tags = [], idPrefix = 'd', docId = 0, pages = 0, updated = '', learned = [] } = $props();

  let marking = $state('');
  const names = { title: 'title', date: 'date', amount: 'amount', sender: 'sender', tags: 'tag' };

  function picked(value) {
    if (marking === 'date') form.date = value;
    else if (marking === 'amount') form.amount = amountInput(value);
    else if (marking === 'tags') {
      if (!form.tags.some((t) => t.toLowerCase() === String(value).toLowerCase())) form.tags = [...form.tags, value];
    } else form[marking] = value;
    marking = '';
  }
</script>

{#snippet pen(field)}
  {#if docId}
    <button type="button" class="pen" onclick={() => (marking = field)} aria-label="Mark the {names[field]} on the page" title="Mark it on the page">
      <Icon name="pen" size={16} />
    </button>
  {/if}
{/snippet}

<div class="stack">
  <div class="row">
    <label class="field">Title<input type="text" bind:value={form.title} maxlength="200" /></label>
    {@render pen('title')}
  </div>
  <div class="two">
    <div class="row">
      <label class="field">Document date<input type="date" bind:value={form.date} /></label>
      {@render pen('date')}
    </div>
    <div class="row">
      <label class="field">Amount<input type="text" inputmode="decimal" bind:value={form.amount} placeholder="e.g. 84,20" /></label>
      {@render pen('amount')}
    </div>
  </div>
  <div class="row">
    <label class="field">Sender
      <input type="text" bind:value={form.sender} list="{idPrefix}-senders" autocomplete="off" />
      <datalist id="{idPrefix}-senders">{#each senders as s (s)}<option value={s}></option>{/each}</datalist>
    </label>
    {@render pen('sender')}
  </div>
  <div class="row">
    <div class="field-wrap">
      <label class="lbl" for="{idPrefix}-tags">Tags</label>
      <TagInput bind:tags={form.tags} suggestions={tags} id="{idPrefix}-tags" {learned} />
      {#if learned.length}<span class="hint">Dashed tags: similar documents have them.</span>{/if}
    </div>
    {@render pen('tags')}
  </div>
  <label class="field">Notes <span class="hint">only here, not in the PDF</span><textarea bind:value={form.notes} rows="2"></textarea></label>
</div>

{#if marking}
  <Marker {docId} pages={pages || 1} {updated} kind={marking} onpick={picked} onclose={() => (marking = '')} />
{/if}

<style>
  .two { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 12px; }
  .row { display: flex; align-items: flex-end; gap: 6px; min-width: 0; }
  .row > :first-child { flex: 1; min-width: 0; }
  .field-wrap { display: flex; flex-direction: column; gap: 6px; }
  .lbl { font-size: 0.9rem; font-weight: 600; }
  .pen { display: none; flex: none; width: 36px; height: 44px; border: 1px solid transparent; border-radius: 10px; background: transparent; color: var(--muted); cursor: pointer; place-items: center; }
  .pen:hover { color: var(--accent); background: var(--chip); }
  .pen:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
  .row:has(.field-wrap) .pen { margin-bottom: 0; align-self: flex-start; margin-top: 26px; }
  @media (min-width: 900px) and (pointer: fine) { .pen { display: grid; } }
</style>
