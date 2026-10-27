<script>
  // Which library the app shows: only there when the account has more than one.
  // A menu of its own (the browser's select list can't follow the app's colours).
  import { libraries, library, currentLibrary } from '../lib/stores.js';
  import { fly } from 'svelte/transition';
  import { motion } from '../lib/motion.js';
  import Icon from './Icon.svelte';
  let { compact = false } = $props();

  let open = $state(false);
  let active = $state(0); // highlighted option while the menu is open
  let root = $state();
  let button = $state();
  const id = `libmenu-${Math.random().toString(36).slice(2, 8)}`;

  function toggle() {
    open = !open;
    if (open) active = Math.max(0, $libraries.findIndex((l) => l.id === $library));
  }
  function pick(l) {
    library.set(l.id);
    open = false;
    button?.focus();
  }
  function key(e) {
    if (!open) {
      if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(e.key)) { e.preventDefault(); toggle(); }
      return;
    }
    if (e.key === 'Escape') { e.preventDefault(); open = false; }
    else if (e.key === 'ArrowDown') { e.preventDefault(); active = (active + 1) % $libraries.length; }
    else if (e.key === 'ArrowUp') { e.preventDefault(); active = (active - 1 + $libraries.length) % $libraries.length; }
    else if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); pick($libraries[active]); }
    else if (e.key === 'Tab') open = false;
  }
  function outside(e) {
    if (open && root && !root.contains(e.target)) open = false;
  }
</script>

<svelte:window onpointerdown={outside} />

{#if $libraries.length > 1}
  <div class="libswitch" class:compact bind:this={root}>
    <button bind:this={button} type="button" class="current" aria-haspopup="listbox" aria-expanded={open} aria-controls={id} aria-label="Library: {$currentLibrary?.name}"
      onclick={toggle} onkeydown={key}>
      <Icon name="folder" size={16} />
      <span class="name">{$currentLibrary?.name}</span>
      {#if $currentLibrary?.role === 'reader'}<span class="ro">read only</span>{/if}
      <svg class="chev" class:up={open} viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M6 9l6 6 6-6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
    </button>
    {#if open}
      <ul class="menu" role="listbox" {id} aria-label="Libraries" transition:fly={motion({ y: -4, duration: 140 })}>
        {#each $libraries as l, i (l.id)}
          <li role="option" aria-selected={l.id === $library} class:active={i === active}
            onpointerenter={() => (active = i)} onclick={() => pick(l)} onkeydown={() => {}}>
            <span class="check" aria-hidden="true">{#if l.id === $library}<Icon name="check" size={15} />{/if}</span>
            <span class="name">{l.name}</span>
            <span class="meta">{l.role === 'reader' ? 'read only' : ''}{l.docs != null ? `${l.role === 'reader' ? ' · ' : ''}${l.docs}` : ''}</span>
          </li>
        {/each}
      </ul>
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
  .menu { position: absolute; z-index: 40; top: calc(100% + 4px); left: 0; right: 0; min-width: 200px; margin: 0; padding: 4px; list-style: none;
    background: var(--card); border: 1px solid var(--line-strong); border-radius: 12px; box-shadow: 0 10px 30px rgb(0 0 0 / 0.18); max-height: 60vh; overflow: auto; }
  .menu li { display: flex; align-items: center; gap: 8px; min-height: 38px; padding: 0 10px 0 6px; border-radius: 8px; cursor: pointer; color: var(--ink); }
  .menu li.active { background: var(--chip); color: var(--chip-ink); }
  .menu li[aria-selected='true'] { font-weight: 650; }
  .check { width: 16px; display: grid; place-items: center; color: var(--accent); flex: none; }
  .meta { font-size: 0.75rem; color: var(--muted); flex: none; }
  @media (prefers-reduced-motion: reduce) { .chev { transition: none; } }
</style>
