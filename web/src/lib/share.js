// Sharing documents through the phone's or computer's share sheet (mail
// with the PDF attached, messengers, AirDrop …): the Web Share API with
// files. Browsers only share during the tap itself (Safari strictly), so
// the PDFs are fetched ahead (prepare) and kept; without file sharing
// (Firefox on the desktop) the PDF is downloaded to attach by hand.

const files = new Map(); // document id → File

export const canShareFiles = (() => {
  try {
    return typeof navigator !== 'undefined' && !!navigator.canShare && navigator.canShare({ files: [new File(['x'], 'x.pdf', { type: 'application/pdf' })] });
  } catch {
    return false;
  }
})();

export function fileName(doc) {
  const base = doc.status === 'inbox' || !doc.path ? `${doc.title || 'document'}.pdf` : doc.path.split('/').pop();
  return base.replace(/[\\/:*?"<>|]+/g, '-');
}

/** Fetches the PDFs so a later tap can share them at once. */
export async function prepare(docs) {
  await Promise.all(docs.filter((d) => !files.has(d.id)).map(async (d) => {
    const r = await fetch(`/api/docs/${d.id}/pdf`, { headers: { 'X-Shoebox': '1' } });
    if (!r.ok) throw new Error(`The PDF of “${d.title || d.id}” could not be loaded.`);
    files.set(d.id, new File([await r.blob()], fileName(d), { type: 'application/pdf' }));
  }));
}

export function forget(id) { files.delete(id); }

/**
 * Shares the documents. 'shared', 'cancelled', 'again' (the files are
 * ready now, but the tap has passed: tap once more) or 'downloaded'.
 */
export async function share(docs) {
  if (!canShareFiles) {
    for (const d of docs) {
      const a = document.createElement('a');
      a.href = `/api/docs/${d.id}/pdf?dl=1`;
      a.download = fileName(d);
      document.body.appendChild(a);
      a.click();
      a.remove();
    }
    return 'downloaded';
  }
  const ready = docs.every((d) => files.has(d.id));
  if (!ready) {
    await prepare(docs);
    if (navigator.userActivation && !navigator.userActivation.isActive) return 'again';
  }
  const data = { files: docs.map((d) => files.get(d.id)), title: docs.length === 1 ? docs[0].title || fileName(docs[0]) : `${docs.length} documents` };
  try {
    await navigator.share(data);
    return 'shared';
  } catch (e) {
    if (e.name === 'AbortError') return 'cancelled';
    if (e.name === 'NotAllowedError') return 'again'; // the tap had passed while loading
    throw e;
  }
}
