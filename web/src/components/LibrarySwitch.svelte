<script>
  // Which library the app shows. A menu of its own (the browser's select
  // list can't follow the app's colours), with a search field; admins can
  // make a new library right here by typing its name.
  import { tick } from 'svelte';
  import { libraries, library, currentLibrary, user, can, setLibraries, toast } from '../lib/stores.js';
  import { api } from '../lib/api.js';
  import { fly } from 'svelte/transition';
  import { motion } from '../lib/motion.js';
  import Icon from './Icon.svelte';
  let { compact = false } = $props();

  let open = $state(false);
  let active = $state(0); // highlighted entry while the menu is open
  let q = $state('');
  let busy = $state(false);
  let root = $state();
  let button = $state();
  let input = $state();
  const id = `libmenu-${Math.random().toString(36).slice(2, 8)}`;

  let admin = $derived(can($user, 'admin'));
  let found = $derived($libraries.filter((l) => l.name.toLowerCase().includes(q.trim().toLowerCase())));
  let exact = $derived($libraries.some((l) => l.name.toLowerCase() === q.trim().toLowerCase()));
  // the entries: the libraries found, then (admins) "Create …"
  let entries = $derived([...found.map((l) => ({ lib: l })), ...(admin ? [{ create: q.trim() && !exact ? q.trim() : '' }] : [])]);

  async function toggle() {
    open = !open;
    if (!open) return;
    q = '';
    active = Math.max(0, $libraries.findIndex((l) => l.id === $library));
    await tick();
    input?.focus();
  }
  function close() {
    open = false;
    button?.focus();
  }
  function pick(l) {
    library.set(l.id);
    close();
  }
  async function choose(e) {
    if (!e) return;
    if (e.lib) return pick(e.lib);
    if (!e.create) { input?.focus(); return; } // "New library …": type its name first
    busy = true;
    try {
      const r = await api.post('/api/libraries', { name: e.create });
      setLibraries(await api.get('/api/libraries'));
      library.set(r.id);
      toast(`Library “${e.create}” created. Share it under Settings → Libraries.`);
      close();
    } catch (err) {
      toast(err.message, 'error', 6000);
    } finally {
      busy = false;
    }
  }
  function key(e) {
    if (!open) {
      if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(e.key)) { e.preventDefault(); toggle(); }
      return;
    }
    const n = entries.length;
    if (e.key === 'Escape') { e.preventDefault(); close(); }
    else if (e.key === 'ArrowDown' && n) { e.preventDefault(); active = (active + 1) % n; }
    else if (e.key === 'ArrowUp' && n) { e.preventDefault(); active = (active - 1 + n) % n; }
    else if (e.key === 'Enter') { e.preventDefault(); choose(entries[active]); }
    else if (e.key === 'Tab') open = false;
  }
  // typing: the first match is the one Enter takes, or "Create" when none
  $effect(() => { q; active = found.length ? 0 : Math.max(0, entries.length - 1); });
  function outside(e) {
    if (open && root && !root.contains(e.target)) open = false;
  }
</script>

<svelte:window onpointerdown={outside} />

{#if $libraries.length > 1 || admin}
  <div class="libswitch" class:compact bind:this={root}>
    <button bind:this={button} type="button" class="current" aria-haspopup="listbox" aria-expanded={open} aria-controls={id} aria-label="Library: {$currentLibrary?.name}"
      onclick={toggle} onkeydown={key}>
      <Icon name="folder" size={16} />
      <span class="name">{$currentLibrary?.name}</span>
      {#if $currentLibrary?.role === 'reader'}<span class="ro">read only</span>{/if}
      <svg class="chev" class:up={open} viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M6 9l6 6 6-6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
    </button>
    {#if open}
      <div class="menu" transition:fly={motion({ y: -4, duration: 140 })}>
        <input bind:this={input} type="search" class="find" bind:value={q} onkeydown={key} placeholder={admin ? 'Find or create …' : 'Find a library …'}
          aria-label={admin ? 'Find a library, or type the name of a new one' : 'Find a library'} aria-controls={id} autocomplete="off"
          aria-activedescendant={entries[active] ? `${id}-${active}` : undefined} />
        <ul role="listbox" {id} aria-label="Libraries">
          {#each entries as e, i (e.lib ? e.lib.id : 'create')}
            {#if e.lib}
              <li id="{id}-{i}" role="option" aria-selected={e.lib.id === $library} class:active={i === active}
                onpointerenter={() => (active = i)} onclick={() => pick(e.lib)} onkeydown={() => {}}>
                <span class="check" aria-hidden="true">{#if e.lib.id === $library}<Icon name="check" size={15} />{/if}</span>
                <span class="name">{e.lib.name}</span>
                <span class="meta">{e.lib.home ? 'private · ' : ''}{e.lib.role === 'reader' ? 'read only · ' : ''}{e.lib.docs ?? ''}</span>
              </li>
            {:else}
              <li id="{id}-{i}" role="option" aria-selected="false" class="create" class:active={i === active} aria-disabled={busy}
                onpointerenter={() => (active = i)} onclick={() => choose(e)} onkeydown={() => {}}>
                <span class="check" aria-hidden="true"><Icon name="plus" size={15} /></span>
                <span class="name">{busy ? 'Creating …' : e.create ? `Create “${e.create}”` : 'New library …'}</span>
              </li>
            {/if}
          {/each}
          {#if !entries.length}<li class="none muted">No library “{q}”</li>{/if}
        </ul>
      </div>
    {/if}
  </div>
{/if}

<style>
  .libswitch { position: relative; }
  .current { width: 100%; display: flex; align-items: center; gap: 8px; min-height: 40px; padding: 0 10px; border: 1px solid var(--line); border-radius: 10px;
    background: var(--card); color: var(--ink); font: inherit; font-weight: 600; cursor: pointer; text-align: left; }
  .current:hover { border-color: var(--line-strong); }
  .current:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
  .current :global(svg:first-child) { color: var(--muted); flex: none; }
  .name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ro { font-size: 0.72rem; font-weight: 500; color: var(--muted); flex: none; }
  .chev { flex: none; color: var(--muted); transition: transform 0.15s; }
  .chev.up { transform: rotate(180deg); }
  .compact .current { min-height: 36px; }
  .menu { position: absolute; z-index: 40; top: calc(100% + 4px); left: 0; right: 0; min-width: 220px; padding: 4px;
    background: var(--card); border: 1px solid var(--line-strong); border-radius: 12px; box-shadow: 0 10px 30px rgb(0 0 0 / 0.18); }
  .menu ul { margin: 0; padding: 0; list-style: none; max-height: 55vh; overflow: auto; }
  .find { width: 100%; min-height: 36px; margin-bottom: 4px; padding: 0 10px; border: 1px solid var(--line); border-radius: 8px; background: var(--field); color: var(--ink); font: inherit; font-size: 0.9rem; }
  .find:focus { outline: 2px solid var(--accent); outline-offset: 0; }
  .menu li.create { color: var(--accent); border-top: 1px solid var(--line); margin-top: 2px; }
  .menu li.none { cursor: default; padding: 0 10px; font-size: 0.85rem; }
  .menu li { display: flex; align-items: center; gap: 8px; min-height: 38px; padding: 0 10px 0 6px; border-radius: 8px; cursor: pointer; color: var(--ink); }
  .menu li.active { background: var(--chip); color: var(--chip-ink); }
  .menu li[aria-selected='true'] { font-weight: 650; }
  .check { width: 16px; display: grid; place-items: center; color: var(--accent); flex: none; }
  .meta { font-size: 0.75rem; color: var(--muted); flex: none; }
  @media (prefers-reduced-motion: reduce) { .chev { transition: none; } }
</style>
