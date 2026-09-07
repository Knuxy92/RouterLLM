// Pure-JS HMAC-SHA256, ported from the old React frontend's web/src/lib/hmac.ts.
// `crypto.subtle` only exists in secure contexts (HTTPS / localhost), and the
// admin console is routinely opened over plain http://<lan-ip> — so the proof
// computation cannot rely on it.
// SHA-256 core follows FIPS 180-4; HMAC construction follows RFC 2104.
//
// Correctness is pinned by the RFC 4231 test vectors (previously hmac.test.ts):
//   Case 1: key=0x0b*20, data="Hi There"
//     → b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7
//   Case 2: key="Jefe", data="what do ya want for nothing?"
//     → 5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843
//   Case 3: key=0xaa*20, data=0xdd*50
//     → 773ea91e36800e46854db8ebd09181a72959098b3ef8c122d9635514ced565fe
//   Case 4: key=0x01..0x19, data=0xcd*50
//     → 82558a389a443c0ea4cc819899f2083a85f0faa3e578f8077a2e3ff46729665b
//   Case 6 (key > block size, 131 bytes of 0xaa, data="Test Using Larger Than Block-Size Key - Hash Key First")
//     → 60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54
//   Case 7 (key > block size, complex data)
//     → 9b09ffa71b942fcb27635fbcd5b0e944bfdc63644f0713938a7f51535c3a35e2
// Verify with e.g. node: hmacProof("Jefe", "what do ya want for nothing?") must
// equal the Case 2 digest above.

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
  0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
  0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
  0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
  0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
])

function rotr(x, n) {
  return (x >>> n) | (x << (32 - n))
}

function sha256(input) {
  const h = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c,
    0x1f83d9ab, 0x5be0cd19,
  ])

  // Padded length: message + 0x80 + length words, rounded to 64-byte blocks.
  const bitLength = input.length * 8
  const padded = new Uint8Array(((input.length + 9 + 63) & ~63))
  padded.set(input)
  padded[input.length] = 0x80
  const view = new DataView(padded.buffer)
  view.setUint32(padded.length - 4, bitLength >>> 0)
  view.setUint32(padded.length - 8, Math.floor(bitLength / 0x100000000))

  const w = new Uint32Array(64)
  for (let block = 0; block < padded.length; block += 64) {
    for (let i = 0; i < 16; i++) {
      w[i] = view.getUint32(block + i * 4)
    }
    for (let i = 16; i < 64; i++) {
      const s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >>> 3)
      const s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >>> 10)
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0
    }

    let [a, b, c, d, e, f, g, hh] = h
    for (let i = 0; i < 64; i++) {
      const S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)
      const ch = (e & f) ^ (~e & g)
      const t1 = (hh + S1 + ch + K[i] + w[i]) >>> 0
      const S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)
      const maj = (a & b) ^ (a & c) ^ (b & c)
      const t2 = (S0 + maj) >>> 0
      hh = g
      g = f
      f = e
      e = (d + t1) >>> 0
      d = c
      c = b
      b = a
      a = (t1 + t2) >>> 0
    }

    h[0] = (h[0] + a) >>> 0
    h[1] = (h[1] + b) >>> 0
    h[2] = (h[2] + c) >>> 0
    h[3] = (h[3] + d) >>> 0
    h[4] = (h[4] + e) >>> 0
    h[5] = (h[5] + f) >>> 0
    h[6] = (h[6] + g) >>> 0
    h[7] = (h[7] + hh) >>> 0
  }

  const out = new Uint8Array(32)
  const outView = new DataView(out.buffer)
  for (let i = 0; i < 8; i++) {
    outView.setUint32(i * 4, h[i])
  }

  return out
}

function utf8(text) {
  return new TextEncoder().encode(text)
}

function concat(a, b) {
  const out = new Uint8Array(a.length + b.length)
  out.set(a)
  out.set(b, a.length)

  return out
}

export function hmacSha256(key, message) {
  let keyBytes = utf8(key)
  if (keyBytes.length > 64) {
    keyBytes = sha256(keyBytes)
  }

  const blockKey = new Uint8Array(64)
  blockKey.set(keyBytes)

  const inner = new Uint8Array(64)
  const outer = new Uint8Array(64)
  for (let i = 0; i < 64; i++) {
    inner[i] = blockKey[i] ^ 0x36
    outer[i] = blockKey[i] ^ 0x5c
  }

  const innerHash = sha256(concat(inner, utf8(message)))

  return sha256(concat(outer, innerHash))
}

export function toHex(bytes) {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("")
}

/** Proof sent to /admin/api/auth/verify: hex(HMAC-SHA256(secret, nonce)). */
export function hmacProof(secret, nonce) {
  return toHex(hmacSha256(secret, nonce))
}
