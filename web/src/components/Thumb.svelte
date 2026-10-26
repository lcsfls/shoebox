<script>
  // First page of a document, or a paper placeholder while it loads.
  let { id, alt = '', updated = '' } = $props();
  let failed = $state(false);
  let loaded = $state(false);
</script>

<span class="thumb">
  {#if !failed}
    <img src="/api/docs/{id}/thumb?v={encodeURIComponent(updated)}" {alt} loading="lazy" decoding="async"
      class:loaded onload={() => (loaded = true)} onerror={() => (failed = true)} />
  {/if}
  {#if !loaded}
    <span class="paper" aria-hidden="true"><i></i><i></i><i></i><i></i></span>
  {/if}
</span>

<style>
  .thumb { position: relative; display: block; aspect-ratio: 210 / 297; background: var(--thumb); border-radius: 8px; overflow: hidden; }
  img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; object-position: top; opacity: 0; transition: opacity 0.2s; }
  img.loaded { opacity: 1; }
  .paper { position: absolute; inset: 14% 16%; display: flex; flex-direction: column; gap: 9%; }
  .paper i { display: block; height: 5%; border-radius: 3px; background: var(--line); }
  .paper i:first-child { width: 55%; background: var(--line-strong); }
  .paper i:nth-child(3) { width: 80%; }
</style>
