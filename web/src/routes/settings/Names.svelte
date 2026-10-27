<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast } from '../../lib/stores.js';
  import Icon from '../../components/Icon.svelte';
  import { palette, colorFor, loadTags } from '../../lib/tags.js';
  import { slide } from 'svelte/transition';
  import { motion } from '../../lib/motion.js';

  let picking = $state(0); // tag whose color row is open
  async function setColor(t, color) {
    try {
      await api.patch(`/api/tags/${t.id}`, { color });
      picking = 0;
      await load();
      loadTags(true);
    } catch (e) { toast(e.message, 'error'); }
  }

  let tags = $state([]);
  let senders = $state([]);
  async function load() {
    [tags, senders] = await Promise.all([api.get('/api/tags'), api.get('/api/senders')]);
  }
  onMount(load);

  async function rename(kind, item) {
    const name = prompt(`New name for “${item.name}”. An existing name merges the two.`, item.name);
    if (!name || name === item.name) return;
    try {
      const r = await api.patch(`/api/${kind}/${item.id}`, { name });
      toast(r.merged ? 'Merged' : 'Renamed');
      load();
    } catch (e) { toast(e.message, 'error'); }
  }
  async function remove(kind, item) {
    if (!confirm(`Remove “${item.name}”? Documents stay; they just lose this ${kind === 'tags' ? 'tag' : 'sender'}.`)) return;
    await api.del(`/api/${kind}/${item.id}`);
    load();
  }
</script>

{#each [['tags', 'Tags', tags], ['senders', 'Senders', senders]] as [kind, title, items] (kind)}
  <div class="card sec">
    <h2>{title}</h2>
    <p class="hint">Renaming changes the name on every document, and in the PDFs the next time each one is saved.{kind === 'tags' ? ' Tap a tag to give it a color.' : ''}</p>
    {#if items.length}
      <ul class="names">
        {#each items as it (it.id)}
          <li>
            {#if kind === 'tags'}
              <button class="chip swatch" data-c={colorFor(it.name, { [it.name.toLowerCase()]: it })} onclick={() => (picking = picking === it.id ? 0 : it.id)}
                aria-expanded={picking === it.id} aria-label="Color of {it.name}" title="Change the color">{it.name}</button>
              <span class="grow"></span>
            {:else}
              <span class="grow">{it.name}</span>
            {/if}
            <span class="muted small">{it.count}</span>
            <button class="btn small" onclick={() => rename(kind, it)}>Rename</button>
            <button class="btn small ghost danger iconbtn" onclick={() => remove(kind, it)} aria-label="Remove {it.name}"><Icon name="trash" /></button>
          </li>
          {#if kind === 'tags' && picking === it.id}
            <li class="colors" transition:slide={motion({ duration: 160 })}>
              <div role="radiogroup" aria-label="Color of {it.name}">
                <button class="chip auto" role="radio" aria-checked={!it.color} onclick={() => setColor(it, '')}>Automatic</button>
                {#each palette as c (c)}
                  <button class="chip dot" data-c={c} role="radio" aria-checked={it.color === c} aria-label={c} title={c} onclick={() => setColor(it, c)}></button>
                {/each}
              </div>
            </li>
          {/if}
        {/each}
      </ul>
    {:else}<p class="muted">None yet.</p>{/if}
  </div>
{/each}

<style>
  .names { list-style: none; margin: 0; padding: 0; }
  .names li { display: flex; align-items: center; gap: 8px; padding: 8px 0; border-top: 1px solid var(--line); }
  .names .grow { overflow-wrap: anywhere; }
  .swatch { cursor: pointer; border: 0; max-width: 70%; overflow-wrap: anywhere; }
  .names li.colors { border-top: 0; padding-top: 0; }
  .colors div { display: flex; flex-wrap: wrap; gap: 6px; }
  .colors .chip { cursor: pointer; border: 0; }
  .colors .dot { width: 30px; justify-content: center; padding: 0; }
  .colors .dot::before { width: 12px; height: 12px; opacity: 0.9; }
  .colors [aria-checked='true'] { outline: 2px solid var(--accent); outline-offset: 2px; }
  .colors .auto { background: var(--field); border: 1px dashed var(--line-strong); color: var(--ink-2); }
</style>
