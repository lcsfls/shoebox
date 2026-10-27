// The stamp labels from the settings, German and English.
export function stampConfig(s) {
  return {
    lang: s?.stamp_lang === 'en' ? 'en' : 'de',
    de: { in: s?.stamp_in || 'Eingang', paid: s?.stamp_paid || 'Bezahlt' },
    en: { in: s?.stamp_in_en || 'Received', paid: s?.stamp_paid_en || 'Paid' },
  };
}

const monthsEn = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/** A YYYY-MM-DD date as the stamp prints it: 27.10.2026 or 27 Oct 2026. */
export function stampDate(iso, lang) {
  const [y, m, d] = (iso || '').split('-').map(Number);
  if (!y || !m || !d) return '';
  return lang === 'en' ? `${d} ${monthsEn[m - 1]} ${y}` : `${String(d).padStart(2, '0')}.${String(m).padStart(2, '0')}.${y}`;
}
