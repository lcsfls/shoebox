<script>
  // Column chart for one series: thin bars from the baseline, a value on
  // hover or keyboard focus, and the largest value named on the axis.
  let { items = [], label = '', height = 140, unit = 'documents', every = 1 } = $props();
  let max = $derived(Math.max(1, ...items.map((i) => i.value)));
  let active = $state(-1);
  const fmt = (n) => n.toLocaleString();
</script>

<figure class="bars" aria-label={label}>
  <div class="plot" style="height:{height}px" role="list" onmouseleave={() => (active = -1)}>
    <span class="top muted small" aria-hidden="true">{fmt(max)}</span>
    {#each items as it, i (it.label + i)}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div class="col" role="listitem" tabindex="0" aria-label="{it.title || it.label}: {fmt(it.value)} {unit}"
        onmouseenter={() => (active = i)} onfocus={() => (active = i)} onblur={() => (active = -1)}>
        <span class="bar" class:on={active === i} style="height:{(it.value / max) * 100}%"></span>
        {#if active === i}
          <span class="tip" role="tooltip"><strong>{fmt(it.value)}</strong> {unit}<br /><span class="muted">{it.title || it.label}</span></span>
        {/if}
      </div>
    {/each}
  </div>
  <div class="axis small muted" aria-hidden="true">
    {#each items as it, i (it.label + i)}<span>{i % every === 0 ? it.label : ''}</span>{/each}
  </div>
</figure>

<style>
  .bars { margin: 0; }
  .plot { position: relative; display: flex; align-items: flex-end; gap: 2px; border-bottom: 1px solid var(--line-strong);
    background-image: linear-gradient(var(--line) 1px, transparent 1px); background-size: 100% 50%; background-position: 0 0; }
  .top { position: absolute; top: -2px; left: 0; transform: translateY(-100%); }
  .col { position: relative; flex: 1; height: 100%; display: flex; align-items: flex-end; justify-content: center; outline: none; cursor: default; }
  .col:focus-visible .bar { outline: 2px solid var(--accent); outline-offset: 2px; }
  .bar { width: min(100%, 22px); min-height: 1px; background: var(--accent); opacity: 0.85; border-radius: 4px 4px 0 0; transition: opacity 0.15s; }
  .bar.on { opacity: 1; }
  .tip { position: absolute; bottom: calc(100% + 6px); left: 50%; transform: translateX(-50%); z-index: 2; white-space: nowrap;
    background: var(--card); color: var(--ink); border: 1px solid var(--line); box-shadow: var(--shadow); border-radius: 8px; padding: 6px 9px; font-size: 0.82rem; pointer-events: none; }
  .axis { display: flex; gap: 2px; margin-top: 4px; }
  .axis span { flex: 1; text-align: center; overflow: visible; white-space: nowrap; font-size: 0.72rem; }
</style>
