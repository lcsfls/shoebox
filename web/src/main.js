import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { startRouter } from './lib/router.js';

startRouter();
mount(App, { target: document.getElementById('app') });

if ('serviceWorker' in navigator && (location.protocol === 'https:' || location.hostname === 'localhost')) {
  navigator.serviceWorker.register('/sw.js').catch(() => {});
}
