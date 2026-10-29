<script>
  // Tags as chips; type and press Enter or comma. Suggestions from existing
  // tags, the most used first (that's the order they come in): shown while
  // the field is empty too. Tab or Enter takes the highlighted one, ↑/↓
  // move, Esc clears what was typed.
  import { tagInfo, colorFor } from '../lib/tags.js';
  let { tags = $bindable([]), suggestions = [], id = 'tags', learned = [] } = $props();
  let text = $state('');
  let focused = $state(false);
  let hi = $state(-1); // highlighted suggestion
  let input = $state();

  const has = (t) => tags.some((x) => x.toLowerCase() === t.toLowerCase());
  function add(t) {
    t = t.trim().replace(/,$/, '').trim();
    if (t && !has(t)) tags = [...tags, t];
    text = '';
    hi = -1;
  }
  function remove(t) { tags = tags.filter((x) => x !== t); }

  let open = $derived.by(() => {
    const q = text.trim().toLowerCase();
    const free = suggestions.filter((s) => !has(s));
    if (!q) return focused ? free.slice(0, 8) : [];
    const starts = free.filter((s) => s.toLowerCase().startsWith(q));
    const inside = free.filter((s) => !s.toLowerCase().startsWith(q) && s.toLowerCase().includes(q));
    return [...starts, ...inside].slice(0, 8);
  });
  // while typing, the best match is ready for Tab
  $effect(() => { text; hi = text.trim() && open.length ? 0 : -1; });

  function key(e) {
    if (e.key === 'Tab' && !e.shiftKey && text.trim() && open.length) {
      e.preventDefault(); // completes; without a match Tab moves on (and blur adds the text)
      add(open[hi] ?? open[0]);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (hi >= 0 && open[hi]) add(open[hi]);
      else if (text.trim()) add(text);
    } else if (e.key === ',') {
      e.preventDefault();
      add(text);
    } else if (e.key === 'ArrowDown' && open.length) {
      e.preventDefault();
      hi = (hi + 1) % open.length;
    } else if (e.key === 'ArrowUp' && open.length) {
      e.preventDefault();
      hi = hi <= 0 ? open.length - 1 : hi - 1;
    } else if (e.key === 'Escape' && (text || hi >= 0)) {
      e.preventDefault();
      e.stopPropagation();
      text = '';
      hi = -1;
    } else if (e.key === 'Backspace' && !text && tags.length) tags = tags.slice(0, -1);
  }
</script>

<div class="tagbox">
  {#each tags as t (t)}
    <button type="button" class="chip" class:learned={learned.includes(t)} data-c={colorFor(t, $tagInfo)} onclick={() => remove(t)}
      aria-label="Remove tag {t}" title={learned.includes(t) ? 'Suggested because similar documents have it' : undefined}>{t}<span aria-hidden="true">×</span></button>
  {/each}
  <input bind:this={input} {id} type="text" bind:value={text} onkeydown={key} onfocus={() => (focused = true)}
    onblur={() => { focused = false; if (text) add(text); }} placeholder={tags.length ? '' : 'Add tags …'} autocomplete="off"
    role="combobox" aria-expanded={open.length > 0} aria-controls="{id}-sugg" aria-activedescendant={hi >= 0 ? `${id}-s${hi}` : undefined} />
</div>
{#if open.length}
  <div class="sugg" id="{id}-sugg" role="listbox" aria-label={text.trim() ? 'Matching tags' : 'Often used tags'}>
    {#if !text.trim()}<span class="muted small often">Often used:</span>{/if}
    {#each open as s, i (s)}
      <button type="button" id="{id}-s{i}" class="chip" class:hi={i === hi} role="option" aria-selected={i === hi} tabindex="-1"
        onmousedown={(e) => { e.preventDefault(); add(s); input?.focus(); }}>+ {s}</button>
    {/each}
    {#if hi >= 0}<span class="muted small keys">Tab or Enter adds “{open[hi]}”</span>{/if}
  </div>
{/if}

<style>
  .tagbox { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; min-height: 44px; padding: 6px 8px; border: 1px solid var(--line); border-radius: 10px; background: var(--field); }
  .tagbox:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
  .tagbox input { flex: 1; min-width: 90px; border: 0; background: transparent; min-height: 30px; padding: 0 4px; outline: none; }
  .chip span { opacity: 0.7; margin-left: 2px; }
  .chip { cursor: pointer; }
  .chip.learned { outline: 1px dashed currentColor; outline-offset: -1px; }
  .sugg { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 6px; align-items: center; }
  .sugg .chip { background: transparent; border: 1px dashed var(--line-strong); color: var(--ink-2); }
  .sugg .chip.hi { border-style: solid; border-color: var(--accent); color: var(--ink); background: var(--chip); }
  .often, .keys { white-space: nowrap; }
  @media (pointer: coarse) { .keys { display: none; } }
</style>
