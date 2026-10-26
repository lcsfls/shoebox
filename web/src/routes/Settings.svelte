<script>
  import Icon from '../components/Icon.svelte';
  import General from './settings/General.svelte';
  import Storage from './settings/Storage.svelte';
  import OCR from './settings/OCR.svelte';
  import Rules from './settings/Rules.svelte';
  import Import from './settings/Import.svelte';
  import Names from './settings/Names.svelte';
  import Users from './settings/Users.svelte';
  import Account from './settings/Account.svelte';
  import Api from './settings/Api.svelte';
  import { user, can } from '../lib/stores.js';

  let { tab = 'general', version = '', cycleTheme, theme } = $props();
  // [key, label, who may open it]
  const all = [
    ['general', 'General', 'viewer'], ['account', 'Account & passkeys', 'viewer'], ['storage', 'Storage', 'admin'],
    ['ocr', 'Text recognition & AI', 'admin'], ['rules', 'Sorting rules', 'admin'], ['import', 'Import from Paperless', 'admin'],
    ['names', 'Tags & senders', 'member'], ['users', 'Users', 'admin'], ['api', 'API', 'viewer'],
  ];
  let tabs = $derived(all.filter(([, , role]) => can($user, role)));
  let allowed = $derived(tabs.some(([k]) => k === tab));
</script>

<div class="page settings">
  <div class="page-head"><h1>Settings</h1><a class="btn small mobile-only" href="/stats"><Icon name="chart" />Statistics</a></div>
  <div class="layout">
    <nav class="sub" aria-label="Settings">
      {#each tabs as [k, label] (k)}
        <a href="/settings/{k}" aria-current={tab === k ? 'page' : undefined}>{label}</a>
      {/each}
    </nav>
    <div class="content">
      {#if !allowed}<div class="card sec"><h2>Not for your account</h2><p class="muted">Ask an admin to change this.</p></div>
      {:else if tab === 'general'}<General {version} {cycleTheme} {theme} />
      {:else if tab === 'account'}<Account />
      {:else if tab === 'api'}<Api />
      {:else if tab === 'storage'}<Storage />
      {:else if tab === 'ocr'}<OCR />
      {:else if tab === 'rules'}<Rules />
      {:else if tab === 'import'}<Import />
      {:else if tab === 'names'}<Names />
      {:else if tab === 'users'}<Users />
      {:else}<p>Unknown section.</p>{/if}
    </div>
  </div>
</div>

<style>
  .layout { display: grid; gap: 16px; }
  .sub { display: flex; gap: 6px; overflow-x: auto; padding-bottom: 4px; margin: 0 -16px; padding-left: 16px; padding-right: 16px; scrollbar-width: none; }
  .sub a { flex: none; min-height: 38px; display: inline-flex; align-items: center; padding: 0 14px; border-radius: 999px; border: 1px solid var(--line);
    background: var(--card); color: var(--ink-2); text-decoration: none; font-weight: 500; font-size: 0.92rem; }
  .sub a[aria-current='page'] { background: var(--accent); border-color: var(--accent); color: var(--on-accent); }
  .content { min-width: 0; max-width: 820px; }
  @media (min-width: 900px) {
    .layout { grid-template-columns: 230px minmax(0, 1fr); align-items: start; }
    .sub { flex-direction: column; margin: 0; padding: 0; position: sticky; top: 20px; }
    .sub a { border: 0; background: transparent; border-radius: 10px; }
    .sub a:hover { background: var(--card); }
    .sub a[aria-current='page'] { background: var(--chip); color: var(--chip-ink); font-weight: 650; }
  }
  :global(.settings .sec) { padding: 18px; display: flex; flex-direction: column; gap: 14px; margin-bottom: 16px; }
  :global(.settings .sec h2) { margin-bottom: -4px; }
  :global(.settings .actions) { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
  :global(.settings pre.code) { margin: 0; padding: 12px 14px; background: #2A2620; color: #F0E9DC; border-radius: 10px; font-family: var(--mono); font-size: 0.8rem; line-height: 1.55; overflow-x: auto; }
</style>
