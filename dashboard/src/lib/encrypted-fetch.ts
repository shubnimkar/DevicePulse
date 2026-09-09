'use client';

const protocol = 'devicepulse-response-v1';
const encoder = new TextEncoder();

/** Decrypt in memory; the actual HTTP response remains binary ciphertext. */
export async function encryptedFetch(input: string, init: RequestInit = {}): Promise<Response> {
  if (!globalThis.crypto?.subtle) {
    throw new Error('Secure dashboard connection required. Use HTTPS or localhost.');
  }
  // A separate, non-exportable private key for every request also prevents
  // concurrent requests, tabs, and users from sharing decryption state.
  const pair = await crypto.subtle.generateKey(
    { name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits'],
  );
  const publicKey = new Uint8Array(await crypto.subtle.exportKey('raw', pair.publicKey));
  const headers = new Headers(init.headers);
  headers.set('X-DevicePulse-Public-Key', btoa(String.fromCharCode(...publicKey)));
  const response = await fetch(input, {
    ...init, headers, credentials: 'include', cache: 'no-store', redirect: 'error',
  });
  if (response.headers.get('X-DevicePulse-Encryption') !== protocol) {
    throw new Error('API did not return an encrypted response. Update the API and dashboard together.');
  }
  const contentType = response.headers.get('X-DevicePulse-Content-Type');
  if (!contentType) throw new Error('Encrypted response is missing its content type.');
  const decodedHeaders = new Headers(response.headers);
  decodedHeaders.set('Content-Type', contentType);
  decodedHeaders.delete('Content-Length');
  decodedHeaders.delete('Content-Encoding');
  decodedHeaders.delete('X-DevicePulse-Encryption');
  decodedHeaders.delete('X-DevicePulse-Content-Type');
  let plaintext: ArrayBuffer | null = null;
  if (init.method?.toUpperCase() !== 'HEAD' && response.status !== 204 && response.status !== 304) {
    const wire = new Uint8Array(await response.arrayBuffer());
    if (wire.byteLength < 125) throw new Error('Encrypted response is truncated.');
    try {
      const serverKey = await crypto.subtle.importKey(
        'raw', wire.slice(0, 65), { name: 'ECDH', namedCurve: 'P-256' }, false, [],
      );
      const shared = await crypto.subtle.deriveBits(
        { name: 'ECDH', public: serverKey }, pair.privateKey, 256,
      );
      const material = await crypto.subtle.importKey('raw', shared, 'HKDF', false, ['deriveKey']);
      const key = await crypto.subtle.deriveKey(
        { name: 'HKDF', hash: 'SHA-256', salt: wire.slice(65, 97), info: encoder.encode(protocol) },
        material, { name: 'AES-GCM', length: 256 }, false, ['decrypt'],
      );
      plaintext = await crypto.subtle.decrypt(
        {
          name: 'AES-GCM', iv: wire.slice(97, 109), tagLength: 128,
          additionalData: encoder.encode(`${protocol}\n${response.status}\n${contentType}`),
        },
        key, wire.slice(109),
      );
    } catch {
      throw new Error('Encrypted response verification failed.');
    }
  }
  return new Response(plaintext, {
    status: response.status, statusText: response.statusText, headers: decodedHeaders,
  });
}
