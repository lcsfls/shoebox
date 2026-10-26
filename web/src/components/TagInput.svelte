<script>
  // Tags as chips; type and press Enter or comma. Suggestions from existing tags.
  let { tags = $bindable([]), suggestions = [], id = 'tags' } = $props();
  let text = $state('');

  function add(t) {
    t = t.trim().replace(/,$/, '').trim();
    if (t && !tags.some((x) => x.toLowerCase() === t.toLowerCase())) tags = [...tags, t];
    text = '';
  }
  function remove(t) { tags = tags.filter((x) => x !== t); }
  function key(e) {
    if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add(text); }
    else if (e.key === 'Backspace' && !text && tags.length) tags = tags.slice(0, -1);
  }
  let open = $derived(text.trim() ? suggestions.filter((s) => s.toLowerCase().includes(text.trim().toLowerCase()) && !tags.includes(s)).slice(0, 6) : []);
</script>

<div class="tagbox">
  {#each tags as t (t)}
    <button type="button" class="chip" onclick={() => remove(t)} aria-label="Remove tag {t}">{t}<span aria-hidden="true">×</span></button>
  {/each}
  <input {id} type="text" bind:value={text} onkeydown={key} onblur={() => text && add(text)} placeholder={tags.length ? '' : 'Add tags …'} autocomplete="off" />
</div>
{#if open.length}
  <div class="sugg">
    {#each open as s (s)}
      <button type="button" class="chip" onmousedown={(e) => { e.preventDefault(); add(s); }}>+ {s}</button>
    {/each}
  </div>
{/if}

<style>
  .tagbox { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; min-height: 44px; padding: 6px 8px; border: 1px solid var(--line); border-radius: 10px; background: var(--field); }
  .tagbox:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
  .tagbox input { flex: 1; min-width: 90px; border: 0; background: transparent; min-height: 30px; padding: 0 4px; outline: none; }
  .chip span { opacity: 0.7; margin-left: 2px; }
  .chip { cursor: pointer; }
  .sugg { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 6px; }
  .sugg .chip { background: transparent; border: 1px dashed var(--line-strong); color: var(--ink-2); }
</style>
