<script>
  import { onMount } from 'svelte';
  import { api } from './lib/api.js';
  import { user, counts, toasts, can, setLibraries, library, canEdit } from './lib/stores.js';
  import LibrarySwitch from './components/LibrarySwitch.svelte';
  import DropZone from './components/DropZone.svelte';
  import { route } from './lib/router.js';
  import { loadTags } from './lib/tags.js';
  import Icon from './components/Icon.svelte';
  import Login from './routes/Login.svelte';
  import Home from './routes/Home.svelte';
  import Scan from './routes/Scan.svelte';
  import Review from './routes/Review.svelte';
  import Archive from './routes/Archive.svelte';
  import DocView from './routes/DocView.svelte';
  import Settings from './routes/Settings.svelte';
  import Stats from './routes/Stats.svelte';

  let needsSetup = $state(false);
  let updateTo = $state(''); // a newer release, shown to admins
  let version = $state('');
  let theme = $state(readTheme());

  function readTheme() {
    try { return localStorage.getItem('sb-theme') || 'system'; } catch { return 'system'; }
  }
  function cycleTheme() {
    const dark = matchMedia('(prefers-color-scheme: dark)').matches;
    const flip = dark ? 'light' : 'dark', same = dark ? 'dark' : 'light';
    theme = theme === 'system' ? flip : theme === flip ? same : 'system';
    try { theme === 'system' ? localStorage.removeItem('sb-theme') : localStorage.setItem('sb-theme', theme); } catch {}
    if (theme === 'system') delete document.documentElement.dataset.theme;
    else document.documentElement.dataset.theme = theme;
  }

  async function loadMe() {
    try {
      const me = await api.get('/api/me');
      needsSetup = me.needs_setup;
      version = me.version;
      setLibraries(me.libraries);
      user.set(me.user);
      if (me.user) loadTags(true);
      if (can(me.user, 'admin')) api.get('/api/update').then((u) => (updateTo = u.newer ? u.latest.tag : '')).catch(() => {});
    } catch {
      user.set(null);
    }
  }

  // inbox badge and "working" indicator
  let timer;
  async function poll() {
    clearTimeout(timer);
    if ($user) {
      try {
        const o = await api.get('/api/overview');
        counts.set({ inbox: o.inbox, busy: o.busy, filed: o.filed, missing: o.missing });
      } catch {}
    }
    timer = setTimeout(poll, $counts.busy ? 2500 : 15000);
  }

  // another library: fresh counts and tag colors (the pages reload below)
  let lastLib = $library;
  $effect(() => {
    const id = $library;
    if (id !== lastLib && $user) {
      lastLib = id;
      loadTags(true);
      poll();
    }
  });

  onMount(() => {
    loadMe().then(poll);
    const onVis = () => document.visibilityState === 'visible' && poll();
    document.addEventListener('visibilitychange', onVis);
    return () => { clearTimeout(timer); document.removeEventListener('visibilitychange', onVis); };
  });

  let path = $derived($route.path);
  let parts = $derived(path.split('/').filter(Boolean));
  let section = $derived(parts[0] || '');
  const allNav = [
    { href: '/', label: 'Home', icon: 'home', key: '', role: 'viewer' },
    { href: '/review', label: 'Review', icon: 'inbox', key: 'review', role: 'member' },
    { href: '/archive', label: 'Archive', icon: 'folder', key: 'archive', role: 'viewer' },
    { href: '/stats', label: 'Statistics', icon: 'chart', key: 'stats', role: 'viewer', wide: true },
    { href: '/settings', label: 'Settings', icon: 'settings', key: 'settings', role: 'viewer' },
  ];
  let nav = $derived(allNav.filter((n) => (n.key === 'review' ? $canEdit : can($user, n.role))));
  // the phone's tab bar keeps four tabs; statistics is reached from Settings there
  let tabNav = $derived(nav.filter((n) => !n.wide));
  let fullscreen = $derived(section === 'scan');
</script>

{#if $user === undefined}
  <div class="boot" aria-busy="true"><Icon name="box" size={40} /></div>
{:else if $user === null}
  <Login {needsSetup} onDone={() => loadMe().then(poll)} />
{:else}
  <div class="shell" class:fullscreen>
    {#if !fullscreen}
      <aside class="side">
        <a class="brand" href="/" aria-label="Shoebox home">
          <Icon name="box" size={26} /><span>shoebox</span>
        </a>
        <LibrarySwitch />
        {#if $canEdit}
          <div class="side-actions">
            <a class="btn primary" href="/scan"><Icon name="scan" />Scan</a>
          </div>
        {/if}
        <nav aria-label="Main">
          {#each nav as n (n.key)}
            <a href={n.href} class="navlink" aria-current={section === n.key ? 'page' : undefined}>
              <Icon name={n.icon} /><span>{n.label}</span>
              {#if n.key === 'review' && $counts.inbox}<span class="badge">{$counts.inbox}</span>{/if}
            </a>
          {/each}
        </nav>
        <div class="side-foot">
          {#if $counts.busy}<p class="working small"><span class="dot"></span>Reading {$counts.busy} document{$counts.busy === 1 ? '' : 's'} …</p>{/if}
          <button class="btn ghost small" onclick={cycleTheme} aria-label="Theme: {theme}" title="Theme: {theme}">
            <Icon name={theme === 'dark' ? 'moon' : theme === 'light' ? 'sun' : 'auto'} />{theme === 'system' ? 'Auto' : theme === 'dark' ? 'Dark' : 'Light'}
          </button>
          {#if updateTo}<a class="update small" href="/settings/updates"><Icon name="download" size={16} />Update to {updateTo}</a>{/if}
          <span class="muted small">{$user.name} · {version}</span>
        </div>
      </aside>
    {/if}

    <main class="main">
      {#key $library}
      {#if section === ''}
        <Home />
      {:else if section === 'scan'}
        <Scan />
      {:else if section === 'review'}
        <Review id={parts[1] ? Number(parts[1]) : 0} />
      {:else if section === 'archive'}
        <Archive />
      {:else if section === 'doc' && parts[1]}
        <DocView id={Number(parts[1])} />
      {:else if section === 'stats'}
        <Stats />
      {:else if section === 'settings'}
        <Settings tab={parts[1] || 'general'} {version} {cycleTheme} {theme} />
      {:else}
        <div class="page"><h1>Not found</h1><p><a href="/">Back to the start</a></p></div>
      {/if}
      {/key}
    </main>

    {#if !fullscreen}<DropZone />{/if}
    {#if !fullscreen}
      <nav class="tabbar" aria-label="Main">
        {#each tabNav as n (n.key)}
          <a href={n.href} aria-current={section === n.key ? 'page' : undefined}>
            <span class="tabicon"><Icon name={n.icon} size={22} />{#if n.key === 'review' && $counts.inbox}<span class="badge">{$counts.inbox}</span>{/if}</span>
            {n.label}
          </a>
        {/each}
      </nav>
    {/if}
  </div>
{/if}

<div class="toasts" role="status" aria-live="polite">
  {#each $toasts as t (t.id)}
    <div class="toast" class:bad={t.kind === 'error'}>{t.text}</div>
  {/each}
</div>

<style>
  .update { display: inline-flex; align-items: center; gap: 6px; color: var(--accent); font-weight: 600; text-decoration: none; }
  .update:hover { text-decoration: underline; }
  .boot { min-height: 100dvh; display: grid; place-items: center; color: var(--accent); }
  .shell { min-height: 100dvh; }
  .side { display: none; }
  .tabbar {
    position: fixed; left: 0; right: 0; bottom: 0; z-index: 20; display: flex;
    background: color-mix(in srgb, var(--card) 94%, transparent); backdrop-filter: blur(10px);
    border-top: 1px solid var(--line); padding: 4px 4px calc(4px + var(--safe-b));
  }
  .tabbar a {
    flex: 1; display: flex; flex-direction: column; align-items: center; gap: 2px; padding: 6px 0;
    color: var(--muted); text-decoration: none; font-size: 0.75rem; font-weight: 500; min-height: 52px;
  }
  .tabbar a[aria-current='page'] { color: var(--accent); font-weight: 650; }
  .tabicon { position: relative; display: flex; }
  .tabicon .badge { position: absolute; top: -6px; right: -14px; }

  @media (min-width: 900px) {
    .tabbar { display: none; }
    .shell:not(.fullscreen) { display: grid; grid-template-columns: 240px minmax(0, 1fr); }
    .side {
      display: flex; flex-direction: column; gap: 18px; position: sticky; top: 0; height: 100dvh;
      padding: 22px 16px; border-right: 1px solid var(--line); background: var(--card);
    }
    .brand { display: flex; align-items: center; gap: 10px; color: var(--accent); text-decoration: none; padding: 0 8px; }
    .brand span { color: var(--ink); font-size: 1.25rem; font-weight: 700; letter-spacing: -0.02em; }
    .side-actions .btn { width: 100%; }
    nav { display: flex; flex-direction: column; gap: 2px; }
    .navlink {
      display: flex; align-items: center; gap: 12px; min-height: 42px; padding: 0 12px; border-radius: 10px;
      color: var(--ink-2); text-decoration: none; font-weight: 500;
    }
    .navlink:hover { background: var(--bg); }
    .navlink[aria-current='page'] { background: var(--chip); color: var(--chip-ink); font-weight: 650; }
    .navlink .badge { margin-left: auto; }
    .side-foot { margin-top: auto; display: flex; flex-direction: column; gap: 8px; align-items: flex-start; }
  }
  .working { display: flex; align-items: center; gap: 8px; color: var(--muted); }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--accent); animation: pulse 1.2s ease-in-out infinite; }
  @keyframes pulse { 50% { opacity: 0.3; } }

  .toasts { position: fixed; left: 50%; bottom: calc(var(--nav-h) + var(--safe-b) + 16px); transform: translateX(-50%); z-index: 50;
    display: flex; flex-direction: column; gap: 8px; align-items: center; pointer-events: none; width: min(92vw, 420px); }
  .toast { background: var(--ink); color: var(--bg); padding: 10px 16px; border-radius: 12px; font-weight: 500; box-shadow: var(--shadow);
    animation: up 0.25s ease-out; text-align: center; }
  .toast.bad { background: var(--danger); color: #fff; }
  @keyframes up { from { opacity: 0; transform: translateY(8px); } }
  @media (min-width: 900px) { .toasts { bottom: 24px; } }
  @media (prefers-reduced-motion: reduce) { .toast, .dot { animation: none; } }
</style>
