<script>
  import { onMount } from 'svelte';
  import { fade } from 'svelte/transition';
  import { api } from '../lib/api.js';
  import { formatDate, formatAmount, formatSize } from '../lib/format.js';
  import { motion } from '../lib/motion.js';
  import Bars from '../components/Bars.svelte';
  import Ranking from '../components/Ranking.svelte';

  let s = $state(null);
  let error = $state('');
  onMount(async () => {
    try { s = await api.get('/api/stats'); } catch (e) { error = e.message; }
  });

  const sourceNames = { scan: 'Scanned with the phone', upload: 'Uploaded', folder: 'Copied into the archive folder', paperless: 'Imported from Paperless' };
  const ocrNames = { pdf: 'Had text already', tesseract: 'Tesseract', 'tesseract+ai': 'Tesseract and AI model', 'pdf+ai': 'AI model' };
  const lang = navigator.language || 'en';
  const n = (x) => (x ?? 0).toLocaleString();

  function months(added) {
    // the last 24 months, empty ones included
    const map = Object.fromEntries(added.map((a) => [a.key, a.docs]));
    const out = [];
    const d = new Date();
    d.setDate(1);
    d.setMonth(d.getMonth() - 23);
    for (let i = 0; i < 24; i++) {
      const key = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
      out.push({ label: d.toLocaleDateString(lang, { month: 'narrow' }), title: d.toLocaleDateString(lang, { month: 'long', year: 'numeric' }), value: map[key] || 0 });
      d.setMonth(d.getMonth() + 1);
    }
    return out;
  }
  const weekdayNames = Array.from({ length: 7 }, (_, i) => new Date(2024, 0, 1 + i).toLocaleDateString(lang, { weekday: 'short' }));

  let scanned = $derived(s ? s.sources.find((x) => x.key === 'scan') : null);
  let ocrSeconds = $derived(s ? s.totals.ocr_ms / 1000 : 0);
  let perPage = $derived(s && s.totals.ocr_docs ? ocrSeconds / Math.max(1, s.totals.pages) : 0);
</script>

<div class="page stats">
  <div class="page-head"><h1>Statistics</h1></div>
  {#if error}<p class="error">{error}</p>{/if}
  {#if s}
    <div in:fade={motion({ duration: 200 })}>
      <section class="tiles" aria-label="Totals">
        <div class="tile"><span class="big">{n(s.totals.docs)}</span><span class="what">documents</span><span class="muted small">{n(s.totals.filed)} filed · {n(s.totals.inbox)} in Review</span></div>
        <div class="tile"><span class="big">{n(s.totals.pages)}</span><span class="what">pages</span><span class="muted small">{n(scanned?.pages || 0)} of them scanned with the phone</span></div>
        <div class="tile"><span class="big">{formatSize(s.totals.size) || '0 KB'}</span><span class="what">of PDFs</span><span class="muted small">database {formatSize(s.db_size)}, previews {formatSize(s.cache_size) || '0 KB'}</span></div>
        <div class="tile"><span class="big">{n(Math.round(s.totals.text_chars / 1800))}</span><span class="what">pages of text in the index</span><span class="muted small">{n(s.totals.text_chars)} characters you can search</span></div>
        <div class="tile"><span class="big">{ocrSeconds < 120 ? `${Math.round(ocrSeconds)} s` : `${Math.round(ocrSeconds / 60)} min`}</span><span class="what">spent reading</span><span class="muted small">{perPage ? `about ${perPage.toFixed(1)} s per page` : 'text recognition time'}</span></div>
        <div class="tile"><span class="big">{s.totals.filed ? Math.round((s.totals.by_rule / s.totals.docs) * 100) : 0} %</span><span class="what">sorted by a rule</span><span class="muted small">{n(s.counts.rules)} rules · {n(s.counts.tags)} tags · {n(s.counts.senders)} senders</span></div>
        {#if s.amount_this_year.docs}
          <div class="tile"><span class="big">{formatAmount(s.amount_this_year.cents)}</span><span class="what">in documents from {new Date().getFullYear()}</span><span class="muted small">the amounts of {n(s.amount_this_year.docs)} bills and letters</span></div>
        {/if}
        {#if s.oldest}
          <div class="tile"><span class="big">{s.oldest.slice(0, 4)}</span><span class="what">oldest document</span><span class="muted small">dated {formatDate(s.oldest)}</span></div>
        {/if}
      </section>

      <div class="grid">
        <section class="card box wide">
          <h2>Added per month</h2>
          <p class="muted small">The last two years, by the day a document came into Shoebox.</p>
          <Bars items={months(s.added)} label="Documents added per month" every={3} />
        </section>

        <section class="card box wide">
          <h2>By the year on the letter</h2>
          <Bars items={s.years.map((y) => ({ label: y.key, value: y.docs }))} label="Documents by year of their date" every={Math.max(1, Math.ceil(s.years.length / 12))} />
        </section>

        <section class="card box">
          <h2>How they came in</h2>
          <Ranking items={s.sources.map((x) => ({ label: sourceNames[x.key] || x.key || 'Other', value: x.docs }))} />
        </section>

        <section class="card box">
          <h2>How the text was read</h2>
          <Ranking items={s.ocr.map((x) => ({ label: ocrNames[x.key] || x.key || 'Unknown', value: x.docs }))} />
        </section>

        <section class="card box">
          <h2>Top senders</h2>
          <Ranking items={s.senders.map((x) => ({ label: x.key, value: x.docs }))} link={(it) => `/archive?sender=${encodeURIComponent(it.label)}`} />
        </section>

        <section class="card box">
          <h2>Top tags</h2>
          <Ranking items={s.tags.map((x) => ({ label: x.key, value: x.docs }))} link={(it) => `/archive?tag=${encodeURIComponent(it.label)}`} />
        </section>

        <section class="card box">
          <h2>Time of day</h2>
          <p class="muted small">When scans and uploads happen.</p>
          <Bars items={s.hours.map((v, h) => ({ label: String(h), title: `${h}:00 – ${h}:59`, value: v }))} label="Scans and uploads by hour" every={6} height={110} />
        </section>

        <section class="card box">
          <h2>Day of the week</h2>
          <Bars items={s.weekdays.map((v, i) => ({ label: weekdayNames[i], value: v }))} label="Scans and uploads by weekday" height={110} />
        </section>

        {#if s.people.length > 1}
          <section class="card box">
            <h2>Who added them</h2>
            <Ranking items={s.people.map((x) => ({ label: x.key, value: x.docs }))} />
          </section>
        {/if}
      </div>
    </div>
  {:else if !error}
    <p class="muted">Counting …</p>
  {/if}
</div>

<style>
  .tiles { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 210px), 1fr)); gap: 12px; margin-bottom: 16px; }
  .tile { background: var(--card); border: 1px solid var(--line); border-radius: var(--radius); padding: 14px 16px; display: flex; flex-direction: column; gap: 2px; }
  .big { font-size: 1.7rem; font-weight: 700; letter-spacing: -0.02em; font-variant-numeric: tabular-nums; line-height: 1.15; }
  .what { font-weight: 550; }
  .grid { display: grid; gap: 16px; }
  .box { padding: 16px 18px 18px; display: flex; flex-direction: column; gap: 12px; min-width: 0; }
  .box h2 { font-size: 1.02rem; margin: 0; }
  .box p { margin: -8px 0 4px; }
  @media (min-width: 900px) { .grid { grid-template-columns: 1fr 1fr; } .wide { grid-column: 1 / -1; } }
</style>
