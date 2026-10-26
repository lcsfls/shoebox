// Applies a chosen light/dark theme before the first paint (no flash).
try { var t = localStorage.getItem('sb-theme'); if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t; } catch (e) {}
