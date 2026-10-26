<script>
  // A ranked list with a bar behind each value: top senders, tags, sources.
  let { items = [], unit = 'documents', link = null } = $props();
  let max = $derived(Math.max(1, ...items.map((i) => i.value)));
</script>

{#if items.length}
  <ol class="rank">
    {#each items as it (it.label)}
      <li>
        <span class="name">{#if link}<a href={link(it)}>{it.label}</a>{:else}{it.label}{/if}</span>
        <span class="track" aria-hidden="true"><span class="fill" style="width:{(it.value / max) * 100}%"></span></span>
        <span class="num" title="{it.value.toLocaleString()} {unit}">{it.value.toLocaleString()}</span>
      </li>
    {/each}
  </ol>
{:else}
  <p class="muted small">Nothing yet.</p>
{/if}

<style>
  .rank { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  li { display: grid; grid-template-columns: minmax(0, 11rem) 1fr auto; gap: 10px; align-items: center; font-size: 0.92rem; }
  .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .name a { color: inherit; text-decoration: none; }
  .name a:hover { text-decoration: underline; }
  .track { height: 8px; border-radius: 4px; background: var(--thumb); overflow: hidden; }
  .fill { display: block; height: 100%; background: var(--accent); border-radius: 4px; min-width: 2px; }
  .num { font-variant-numeric: tabular-nums; color: var(--ink-2); min-width: 2.5ch; text-align: right; }
</style>
