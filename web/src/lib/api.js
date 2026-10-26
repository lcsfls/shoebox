// Talks to the Shoebox server. Every change carries the X-Shoebox header
// (the server's check against cross-site requests).
import { user } from './stores.js';

export class ApiError extends Error {
  constructor(message, status) { super(message); this.status = status; }
}

async function request(method, path, body, isForm) {
  const opts = { method, headers: { 'X-Shoebox': '1' }, credentials: 'same-origin' };
  if (body !== undefined) {
    if (isForm) opts.body = body;
    else { opts.body = JSON.stringify(body); opts.headers['Content-Type'] = 'application/json'; }
  }
  let res;
  try {
    res = await fetch(path, opts);
  } catch {
    throw new ApiError('Shoebox is not reachable. Check the connection.', 0);
  }
  const data = await res.json().catch(() => ({}));
  if (res.status === 401) user.set(null);
  if (!res.ok) throw new ApiError(data.error || `Something went wrong (${res.status}).`, res.status);
  return data;
}

export const api = {
  get: (p) => request('GET', p),
  post: (p, b) => request('POST', p, b ?? {}),
  put: (p, b) => request('PUT', p, b),
  patch: (p, b) => request('PATCH', p, b),
  del: (p) => request('DELETE', p),
  form: (p, fd) => request('POST', p, fd, true),
};

/** Upload with progress (fetch can't report upload progress). */
export function upload(path, formData, onProgress) {
  return new Promise((resolve, reject) => {
    const x = new XMLHttpRequest();
    x.open('POST', path);
    x.setRequestHeader('X-Shoebox', '1');
    x.upload.onprogress = (e) => e.lengthComputable && onProgress?.(e.loaded / e.total);
    x.onload = () => {
      let data = {};
      try { data = JSON.parse(x.responseText); } catch {}
      if (x.status >= 200 && x.status < 300) resolve(data);
      else reject(new ApiError(data.error || `Upload failed (${x.status}).`, x.status));
    };
    x.onerror = () => reject(new ApiError('Upload failed. Check the connection.', 0));
    x.send(formData);
  });
}
