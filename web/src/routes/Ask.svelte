<script>
  // Asking the archive in plain words; the answer names its documents.
  import { tick } from 'svelte';
  import { api } from '../lib/api.js';
  import { route } from '../lib/router.js';
  import { formatDate } from '../lib/format.js';
  import Icon from '../components/Icon.svelte';

  let q = $state($route.query.get('q') || '');
  let turns = $state([]); // { q, answer, sources, error, busy }
  let input = $state();
  let list = $state();

  async function send(e) {
    e?.preventDefault();
    const question = q.trim();
    if (!question || turns.some((t) => t.busy)) return;
    q = '';
    turns = [...turns, { q: question, busy: true, answer: '', sources: [] }];
    const t = turns[turns.length - 1];
    await tick();
    list?.lastElementChild?.scrollIntoView({ behavior: 'smooth', block: 'end' });
    try {
      const r = await api.post('/api/ask', { question });
      Object.assign(t, { answer: r.answer, sources: r.sources, busy: false });
    } catch (err) {
      Object.assign(t, { error: err.message, busy: false });
    }
    await tick();
    input?.focus();
  }
  // a question from the start page's search
  $effect(() => { if (q && !turns.length) send(); });

  const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  // the answer as text with links for [1], [2] … and **bold**
  function render(t) {
    const byN = new Map(t.sources.map((s) => [s.n, s]));
    return esc(t.answer)
      .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
      .replace(/\[(\d+)\]/g, (m, n) => (byN.has(Number(n)) ? `<a class="cite" href="/doc/${byN.get(Number(n)).id}">${n}</a>` : m))
      .replace(/\n/g, '<br>');
  }
  const examples = ['Wann läuft der Handyvertrag aus?', 'Was haben wir 2025 für Strom bezahlt?', 'Wo ist die Garantie vom Kühlschrank?'];
</script>

<div class="page ask">
  <div class="page-head"><h1>Ask</h1></div>
  {#if !turns.length}
    <div class="card intro">
      <Icon name="sparkle" size={26} />
      <p>Ask about your documents in your own words. The answer comes only from the documents you can see and names them, so you can check. When they don't say, it tells you so.</p>
      <div class="examples">
        {#each examples as ex}<button class="chip" onclick={() => { q = ex; send(); }}>{ex}</button>{/each}
      </div>
    </div>
  {/if}
  <div class="turns" bind:this={list}>
    {#each turns as t}
      <div class="turn">
        <p class="question">{t.q}</p>
        <div class="answer card" aria-live="polite" aria-busy={t.busy}>
          {#if t.busy}<p class="muted">Looking through the documents …</p>
          {:else if t.error}<p class="error">{t.error}</p>
          {:else}
            <p>{@html render(t)}</p>
            {#if t.sources.length}
              <ol class="sources">
                {#each t.sources as s (s.id)}
                  <li value={s.n}><a href="/doc/{s.id}">{s.title || 'Untitled'}</a>
                    <span class="muted small">{[s.sender, s.date && formatDate(s.date), s.library].filter(Boolean).join(' · ')}</span></li>
                {/each}
              </ol>
            {/if}
          {/if}
        </div>
      </div>
    {/each}
  </div>
  <form class="asker" onsubmit={send}>
    <label class="sr-only" for="ask-q">Your question</label>
    <input id="ask-q" bind:this={input} type="text" bind:value={q} placeholder="Ask about your documents …" autocomplete="off" enterkeyhint="send" />
    <button class="btn primary" disabled={!q.trim() || turns.some((t) => t.busy)}>Ask</button>
  </form>
</div>

<style>
  .ask { max-width: 820px; display: flex; flex-direction: column; gap: 14px; }
  .intro { padding: 18px; display: flex; flex-direction: column; gap: 10px; color: var(--ink-2); }
  .intro :global(svg) { color: var(--accent); }
  .intro p { margin: 0; }
  .examples { display: flex; flex-wrap: wrap; gap: 6px; }
  .examples .chip { cursor: pointer; border: 1px dashed var(--line-strong); background: transparent; color: var(--ink-2); }
  .turns { display: flex; flex-direction: column; gap: 18px; }
  .turn { display: flex; flex-direction: column; gap: 8px; }
  .question { align-self: flex-end; margin: 0; max-width: 85%; padding: 10px 14px; border-radius: 16px 16px 4px 16px; background: var(--chip); color: var(--chip-ink); font-weight: 550; }
  .answer { padding: 14px 16px; line-height: 1.5; }
  .answer p { margin: 0; }
  .answer :global(.cite) { display: inline-grid; place-items: center; min-width: 1.4em; height: 1.4em; padding: 0 4px; margin: 0 1px; border-radius: 999px; background: var(--chip); color: var(--chip-ink); font-size: 0.75em; font-weight: 700; text-decoration: none; vertical-align: 0.15em; }
  .sources { margin: 12px 0 0; padding: 10px 0 0 1.4em; border-top: 1px solid var(--line); display: flex; flex-direction: column; gap: 4px; font-size: 0.9rem; }
  .sources a { font-weight: 600; }
  .sources span { margin-left: 6px; }
  .asker { position: sticky; bottom: calc(var(--nav-h, 0px) + 12px); display: flex; gap: 8px; padding: 8px; background: var(--card); border: 1px solid var(--line); border-radius: 14px; box-shadow: var(--shadow); }
  .asker input { flex: 1; min-width: 0; border: 0; background: transparent; min-height: 44px; padding: 0 8px; font-size: 1rem; }
  .asker input:focus-visible { outline: none; }
  .asker:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
</style>
