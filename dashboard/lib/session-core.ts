// Signed session tokens: base64url(payload) + "." + base64url(HMAC-SHA256).
// Web Crypto only, so this runs in proxy.ts as well as on the server.

export const SESSION_COOKIE = "fincore_session";
export const SESSION_TTL_SECONDS = 8 * 60 * 60;

/** sub = operator id; ver must match the operator's session_version. */
export type Session = { sub: string; ver: number; exp: number };

const enc = new TextEncoder();

function b64url(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function fromB64url(s: string): Uint8Array<ArrayBuffer> {
  const bin = atob(s.replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function hmacKey(secret: string) {
  return crypto.subtle.importKey("raw", enc.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign", "verify"]);
}

export async function signSession(session: Session, secret: string): Promise<string> {
  const data = b64url(enc.encode(JSON.stringify(session)));
  const sig = new Uint8Array(await crypto.subtle.sign("HMAC", await hmacKey(secret), enc.encode(data)));
  return `${data}.${b64url(sig)}`;
}

/** Returns the session if the signature is valid and it has not expired. */
export async function verifySession(token: string, secret: string): Promise<Session | null> {
  const [data, sig] = token.split(".");
  if (!data || !sig) return null;
  try {
    const ok = await crypto.subtle.verify("HMAC", await hmacKey(secret), fromB64url(sig), enc.encode(data)); // constant-time
    if (!ok) return null;
    const session = JSON.parse(new TextDecoder().decode(fromB64url(data))) as Session;
    return typeof session.sub === "string" && typeof session.ver === "number" && session.exp > Date.now() / 1000 ? session : null;
  } catch {
    return null;
  }
}
