<script>
  // The pages of a document as images (phones can't show PDFs inline).
  let { doc } = $props();
  let pages = $derived(Array.from({ length: Math.max(doc.pages || 1, 1) }, (_, i) => i + 1));
</script>

<div class="pages">
  {#each pages as n (n)}
    <a class="pg card" href="/api/docs/{doc.id}/pdf" target="_blank" rel="noopener" aria-label="Page {n}, open the PDF">
      <img src="/api/docs/{doc.id}/page/{n}?v={encodeURIComponent(doc.updated_at)}" alt="Page {n}" loading={n > 2 ? 'lazy' : 'eager'} decoding="async" />
    </a>
  {/each}
</div>

<style>
  .pages { display: flex; flex-direction: column; gap: 12px; }
  .pg { display: block; overflow: hidden; background: var(--thumb); min-height: 200px; }
  .pg img { width: 100%; height: auto; background: #fff; }
</style>
