<script>
  import { onMount } from 'svelte';
  import { api } from '../../lib/api.js';
  import { toast } from '../../lib/stores.js';
  import Icon from '../../components/Icon.svelte';

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
    <p class="hint">Renaming changes the name on every document, and in the PDFs the next time each one is saved.</p>
    {#if items.length}
      <ul class="names">
        {#each items as it (it.id)}
          <li>
            <span class="grow">{it.name}</span><span class="muted small">{it.count}</span>
            <button class="btn small" onclick={() => rename(kind, it)}>Rename</button>
            <button class="btn small ghost danger iconbtn" onclick={() => remove(kind, it)} aria-label="Remove {it.name}"><Icon name="trash" /></button>
          </li>
        {/each}
      </ul>
    {:else}<p class="muted">None yet.</p>{/if}
  </div>
{/each}

<style>
  .names { list-style: none; margin: 0; padding: 0; }
  .names li { display: flex; align-items: center; gap: 8px; padding: 8px 0; border-top: 1px solid var(--line); }
  .names .grow { overflow-wrap: anywhere; }
</style>
