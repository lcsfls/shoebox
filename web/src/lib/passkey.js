// Passkeys in the browser: turn the server's JSON options into the binary
// form WebAuthn wants, and the browser's answer back into JSON.
import { api } from './api.js';

const b64 = {
  decode(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    const bin = atob(s + '==='.slice((s.length + 3) % 4));
    return Uint8Array.from(bin, (c) => c.charCodeAt(0)).buffer;
  },
  encode(buf) {
    let s = '';
    for (const b of new Uint8Array(buf)) s += String.fromCharCode(b);
    return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  },
};

export const passkeysSupported = () =>
  typeof window !== 'undefined' && !!window.PublicKeyCredential && window.isSecureContext;

function creationOptions(o) {
  if (PublicKeyCredential.parseCreationOptionsFromJSON) return PublicKeyCredential.parseCreationOptionsFromJSON(o);
  return {
    ...o,
    challenge: b64.decode(o.challenge),
    user: { ...o.user, id: b64.decode(o.user.id) },
    excludeCredentials: (o.excludeCredentials || []).map((c) => ({ ...c, id: b64.decode(c.id) })),
  };
}

function requestOptions(o) {
  if (PublicKeyCredential.parseRequestOptionsFromJSON) return PublicKeyCredential.parseRequestOptionsFromJSON(o);
  return {
    ...o,
    challenge: b64.decode(o.challenge),
    allowCredentials: (o.allowCredentials || []).map((c) => ({ ...c, id: b64.decode(c.id) })),
  };
}

function toJSON(cred) {
  try { if (cred.toJSON) return cred.toJSON(); } catch {}
  const r = cred.response;
  const out = { id: cred.id, rawId: b64.encode(cred.rawId), type: cred.type, clientExtensionResults: cred.getClientExtensionResults?.() || {}, authenticatorAttachment: cred.authenticatorAttachment, response: { clientDataJSON: b64.encode(r.clientDataJSON) } };
  if (r.attestationObject) {
    out.response.attestationObject = b64.encode(r.attestationObject);
    out.response.transports = r.getTransports?.() || [];
  } else {
    out.response.authenticatorData = b64.encode(r.authenticatorData);
    out.response.signature = b64.encode(r.signature);
    if (r.userHandle) out.response.userHandle = b64.encode(r.userHandle);
  }
  return out;
}

function friendly(e) {
  if (e?.name === 'NotAllowedError') return 'Cancelled, or the passkey wasn’t confirmed.';
  if (e?.name === 'InvalidStateError') return 'This device already has a passkey for your account.';
  if (e?.name === 'SecurityError') return 'Passkeys need HTTPS and the address Shoebox was set up with.';
  return e?.message || 'The passkey didn’t work.';
}

/** Adds a passkey to the signed-in account. */
export async function addPasskey(name) {
  const opts = await api.post('/api/passkeys/register/begin');
  let cred;
  try {
    cred = await navigator.credentials.create({ publicKey: creationOptions(opts.publicKey) });
  } catch (e) { throw new Error(friendly(e)); }
  await api.post('/api/passkeys/register/finish?name=' + encodeURIComponent(name || ''), toJSON(cred));
}

/** Signs in with any passkey this device offers. */
export async function signInWithPasskey() {
  const opts = await api.post('/api/login/passkey/begin');
  let cred;
  try {
    cred = await navigator.credentials.get({ publicKey: requestOptions(opts.publicKey) });
  } catch (e) { throw new Error(friendly(e)); }
  await api.post('/api/login/passkey/finish', toJSON(cred));
}

/** A name for a new passkey: the kind of device it is on. */
export function deviceName() {
  const ua = navigator.userAgent;
  if (/iPhone/.test(ua)) return 'iPhone';
  if (/iPad/.test(ua)) return 'iPad';
  if (/Android/.test(ua)) return 'Android phone';
  if (/Mac OS X/.test(ua)) return 'Mac';
  if (/Windows/.test(ua)) return 'Windows PC';
  if (/Linux/.test(ua)) return 'Linux computer';
  return 'Passkey';
}
