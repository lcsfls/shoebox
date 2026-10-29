// Uploading files (from the picker or dropped anywhere on the page).
import { upload } from './api.js';
import { toast, counts, showIntake } from './stores.js';
import { writable } from 'svelte/store';

export const uploading = writable(0); // progress 0..1, 0 = idle

const accepted = (f) => f.type === 'application/pdf' || f.type.startsWith('image/') || /\.(pdf|jpe?g|png|tiff?|webp|heic)$/i.test(f.name);

export async function uploadFiles(list) {
  const files = [...list].filter(accepted);
  const skipped = list.length - files.length;
  if (skipped) toast(`${skipped} file${skipped === 1 ? ' is' : 's are'} not a PDF or photo and ${skipped === 1 ? 'was' : 'were'} left out.`, 'error', 5000);
  if (!files.length) return false;
  const fd = new FormData();
  files.forEach((f) => fd.append('files', f, f.name));
  uploading.set(0.01);
  try {
    const r = await upload('/api/upload', fd, (p) => uploading.set(Math.max(p, 0.01)));
    showIntake(r.library);
    toast(files.length === 1 ? 'Uploaded. Reading the text …' : `${files.length} files uploaded. Reading the text …`);
    counts.update((c) => ({ ...c, busy: c.busy + files.length }));
    return true;
  } catch (err) {
    toast(err.message, 'error', 6000);
    return false;
  } finally {
    uploading.set(0);
  }
}
