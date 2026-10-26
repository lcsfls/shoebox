<script>
  import Icon from '../components/Icon.svelte';
  import General from './settings/General.svelte';
  import Storage from './settings/Storage.svelte';
  import OCR from './settings/OCR.svelte';
  import Rules from './settings/Rules.svelte';
  import Import from './settings/Import.svelte';
  import Names from './settings/Names.svelte';
  import Users from './settings/Users.svelte';

  let { tab = 'general', version = '', cycleTheme, theme } = $props();
  const tabs = [
    ['general', 'General'], ['storage', 'Storage'], ['ocr', 'Text recognition & AI'], ['rules', 'Sorting rules'],
    ['import', 'Import from Paperless'], ['names', 'Tags & senders'], ['users', 'Users'],
  ];
</script>

<div class="page settings">
  <div class="page-head"><h1>Settings</h1></div>
  <div class="layout">
    <nav class="sub" aria-label="Settings">
      {#each tabs as [k, label] (k)}
        <a href="/settings/{k}" aria-current={tab === k ? 'page' : undefined}>{label}</a>
      {/each}
    </nav>
    <div class="content">
      {#if tab === 'general'}<General {version} {cycleTheme} {theme} />
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
