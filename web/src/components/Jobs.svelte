<script>
  // What is being read right now, and what went wrong.
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { counts } from '../lib/stores.js';
  import Icon from './Icon.svelte';

  let { onChange } = $props();
  let jobs = $state([]);
  let timer;
  let lastDone = 0;

  async function load() {
    clearTimeout(timer);
    try {
      jobs = await api.get('/api/jobs');
      const done = jobs.filter((j) => j.state === 'done').length;
      if (done !== lastDone) { lastDone = done; onChange?.(); }
    } catch {}
    const busy = jobs.some((j) => j.state === 'queued' || j.state === 'working');
    timer = setTimeout(load, busy ? 1500 : 8000);
  }
  onMount(() => { load(); return () => clearTimeout(timer); });
  $effect(() => { if ($counts.busy) load(); });

  async function dismiss(id) {
    await api.del(`/api/jobs/${id}`).catch(() => {});
    jobs = jobs.filter((j) => j.id !== id);
  }
  let visible = $derived(jobs.filter((j) => j.state !== 'done').slice(0, 6));
</script>

{#if visible.length}
  <ul class="jobs">
    {#each visible as j (j.id)}
      <li class="job card" class:failed={j.state === 'failed'}>
        {#if j.state === 'failed'}
          <Icon name="warn" />
          <span class="grow"><strong>{j.name}</strong><span class="small">{j.error}</span></span>
          {#if j.doc_id}<a class="btn small" href="/doc/{j.doc_id}">Open</a>{/if}
          <button class="btn ghost small iconbtn" onclick={() => dismiss(j.id)} aria-label="Dismiss"><Icon name="close" /></button>
        {:else}
          <span class="spinner" aria-hidden="true"></span>
          <span class="grow"><strong>{j.name}</strong><span class="small muted">{j.state === 'queued' ? 'Waiting …' : j.step || 'Working …'}</span></span>
        {/if}
      </li>
    {/each}
  </ul>
{/if}

<style>
  .jobs { list-style: none; margin: 0 0 16px; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .job { display: flex; align-items: center; gap: 12px; padding: 10px 12px; }
  .job .grow { display: flex; flex-direction: column; }
  .job strong { font-weight: 600; font-size: 0.92rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .failed { background: var(--warn); color: var(--warn-ink); border-color: transparent; }
  .spinner { width: 18px; height: 18px; border-radius: 50%; border: 2px solid var(--line-strong); border-top-color: var(--accent); animation: spin 0.8s linear infinite; flex: none; }
  @keyframes spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) { .spinner { animation-duration: 3s; } }
</style>
