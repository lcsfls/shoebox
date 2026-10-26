<script>
  // The fields of a document: used on the review screen and the document page.
  import TagInput from './TagInput.svelte';
  let { form = $bindable(), senders = [], tags = [], idPrefix = 'd' } = $props();
</script>

<div class="stack">
  <label class="field">Title<input type="text" bind:value={form.title} maxlength="200" /></label>
  <div class="two">
    <label class="field">Document date<input type="date" bind:value={form.date} /></label>
    <label class="field">Amount<input type="text" inputmode="decimal" bind:value={form.amount} placeholder="e.g. 84,20" /></label>
  </div>
  <label class="field">Sender
    <input type="text" bind:value={form.sender} list="{idPrefix}-senders" autocomplete="off" />
    <datalist id="{idPrefix}-senders">{#each senders as s (s)}<option value={s}></option>{/each}</datalist>
  </label>
  <div class="field-wrap">
    <label class="lbl" for="{idPrefix}-tags">Tags</label>
    <TagInput bind:tags={form.tags} suggestions={tags} id="{idPrefix}-tags" />
  </div>
  <label class="field">Notes <span class="hint">only here, not in the PDF</span><textarea bind:value={form.notes} rows="2"></textarea></label>
</div>

<style>
  .two { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 12px; }
  .field-wrap { display: flex; flex-direction: column; gap: 6px; }
  .lbl { font-size: 0.9rem; font-weight: 600; }
</style>
