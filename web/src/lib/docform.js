// The editable fields of a document (Review and the document page), and
// the PATCH they turn into.
import { amountInput, parseAmount } from './format.js';

export function formFromDoc(doc) {
  return {
    title: doc.title, date: doc.date, sender: doc.sender, amount: amountInput(doc.amount_cents), tags: [...doc.tags], notes: doc.notes,
    summary: doc.summary || '', keywords: (doc.keywords || []).join(', '), due: doc.due || '', action: doc.action || '',
  };
}

export function patchFromForm(form) {
  const cents = parseAmount(form.amount);
  return {
    title: form.title, date: form.date || '', sender: form.sender, tags: form.tags, notes: form.notes,
    amount_cents: cents ?? undefined, no_amount: cents == null,
    summary: form.summary, keywords: form.keywords.split(',').map((k) => k.trim()).filter(Boolean), due: form.due || '', action: form.action,
  };
}

/** "in 3 days", "today", "2 days overdue" for a deadline. */
export function dueLabel(due) {
  if (!due) return '';
  const today = new Date(new Date().toDateString());
  const d = new Date(due + 'T00:00:00');
  const days = Math.round((d - today) / 86400000);
  if (days < -1) return `${-days} days overdue`;
  if (days === -1) return 'overdue since yesterday';
  if (days === 0) return 'today';
  if (days === 1) return 'tomorrow';
  if (days < 14) return `in ${days} days`;
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: d.getFullYear() === today.getFullYear() ? undefined : 'numeric' });
}
export const overdue = (due) => !!due && due < new Date().toISOString().slice(0, 10);
