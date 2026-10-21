// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Minimal QR Code encoder (ISO/IEC 18004) for the MFA enrolment page (D-19 §B):
 * byte mode, error correction level M, versions 1–15 (up to 412 bytes, far
 * more than an `otpauth://` URI needs), all eight masks scored with the
 * standard penalty rules. Zero dependencies; lazy-loaded with QrCode.vue only.
 */

// ECC level M per version: [ecc codewords per block, [blocks, data codewords per block]…]
const M_BLOCKS: [number, [number, number][]][] = [
  [10, [[1, 16]]],
  [16, [[1, 28]]],
  [26, [[1, 44]]],
  [18, [[2, 32]]],
  [24, [[2, 43]]],
  [16, [[4, 27]]],
  [18, [[4, 31]]],
  [22, [[2, 38], [2, 39]]],
  [22, [[3, 36], [2, 37]]],
  [26, [[4, 43], [1, 44]]],
  [30, [[1, 50], [4, 51]]],
  [22, [[6, 36], [2, 37]]],
  [22, [[8, 37], [1, 38]]],
  [24, [[4, 40], [5, 41]]],
  [24, [[5, 41], [5, 42]]],
]
const ALIGN: number[][] = [
  [], [6, 18], [6, 22], [6, 26], [6, 30], [6, 34], [6, 22, 38], [6, 24, 42], [6, 26, 46], [6, 28, 50],
  [6, 30, 54], [6, 32, 58], [6, 34, 62], [6, 26, 46, 66], [6, 26, 48, 70],
]
export const QR_MAX_VERSION = M_BLOCKS.length
const FORMAT_M = 0 // ECC level M format bits

function gfMul(x: number, y: number): number {
  let z = 0
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d)
    z ^= ((y >>> i) & 1) * x
  }
  return z & 0xff
}

function rsDivisor(degree: number): number[] {
  const d = new Array<number>(degree).fill(0)
  d[degree - 1] = 1
  let root = 1
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < degree; j++) {
      d[j] = gfMul(d[j]!, root)
      if (j + 1 < degree) d[j]! ^= d[j + 1]!
    }
    root = gfMul(root, 0x02)
  }
  return d
}

function rsRemainder(data: number[], divisor: number[]): number[] {
  const r = new Array<number>(divisor.length).fill(0)
  for (const b of data) {
    const factor = b ^ r.shift()!
    r.push(0)
    for (let i = 0; i < r.length; i++) r[i]! ^= gfMul(divisor[i]!, factor)
  }
  return r
}

const dataCapacity = (v: number) => M_BLOCKS[v - 1]![1].reduce((s, [n, k]) => s + n * k, 0)

/** The encoded symbol: `modules[y][x]` is true for a dark module. */
export interface QrMatrix {
  version: number
  size: number
  mask: number
  modules: boolean[][]
}

/** Encodes text (UTF-8, byte mode, level M). Throws when it does not fit version 15. */
export function encodeQr(text: string, forceMask?: number): QrMatrix {
  const bytes = Array.from(new TextEncoder().encode(text))
  let version = 0
  for (let v = 1; v <= QR_MAX_VERSION; v++) {
    const countBits = v < 10 ? 8 : 16
    if (4 + countBits + bytes.length * 8 <= dataCapacity(v) * 8) {
      version = v
      break
    }
  }
  if (!version) throw new Error('qr: text too long')
  const capBytes = dataCapacity(version)
  const bits: number[] = []
  const put = (val: number, len: number) => {
    for (let i = len - 1; i >= 0; i--) bits.push((val >>> i) & 1)
  }
  put(0b0100, 4)
  put(bytes.length, version < 10 ? 8 : 16)
  for (const b of bytes) put(b, 8)
  put(0, Math.min(4, capBytes * 8 - bits.length))
  put(0, (8 - (bits.length % 8)) % 8)
  const data: number[] = []
  for (let i = 0; i < bits.length; i += 8) data.push(bits.slice(i, i + 8).reduce((a, b) => (a << 1) | b, 0))
  for (let pad = 0xec; data.length < capBytes; pad ^= 0xec ^ 0x11) data.push(pad)

  // Split into blocks, add Reed–Solomon ECC, interleave.
  const [ecLen, groups] = M_BLOCKS[version - 1]!
  const divisor = rsDivisor(ecLen)
  const blocks: { d: number[]; e: number[] }[] = []
  let off = 0
  for (const [n, k] of groups) {
    for (let i = 0; i < n; i++) {
      const d = data.slice(off, off + k)
      off += k
      blocks.push({ d, e: rsRemainder(d, divisor) })
    }
  }
  const words: number[] = []
  const maxD = Math.max(...blocks.map((b) => b.d.length))
  for (let i = 0; i < maxD; i++) for (const b of blocks) if (i < b.d.length) words.push(b.d[i]!)
  for (let i = 0; i < ecLen; i++) for (const b of blocks) words.push(b.e[i]!)

  const size = version * 4 + 17
  const modules = Array.from({ length: size }, () => new Array<boolean>(size).fill(false))
  const fn = Array.from({ length: size }, () => new Array<boolean>(size).fill(false))
  const setFn = (x: number, y: number, dark: boolean) => {
    modules[y]![x] = dark
    fn[y]![x] = true
  }

  // Function patterns: timing, finders (+ separators), alignment, reserved format/version areas.
  for (let i = 0; i < size; i++) {
    setFn(6, i, i % 2 === 0)
    setFn(i, 6, i % 2 === 0)
  }
  for (const [cx, cy] of [[3, 3], [size - 4, 3], [3, size - 4]] as const) {
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const x = cx + dx
        const y = cy + dy
        if (x < 0 || y < 0 || x >= size || y >= size) continue
        const dist = Math.max(Math.abs(dx), Math.abs(dy))
        setFn(x, y, dist !== 2 && dist !== 4)
      }
    }
  }
  const al = ALIGN[version - 1]!
  for (let i = 0; i < al.length; i++) {
    for (let j = 0; j < al.length; j++) {
      if ((i === 0 && j === 0) || (i === 0 && j === al.length - 1) || (i === al.length - 1 && j === 0)) continue
      for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) setFn(al[i]! + dx, al[j]! + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1)
    }
  }
  const drawFormat = (mask: number) => {
    const d = (FORMAT_M << 3) | mask
    let rem = d
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537)
    const f = ((d << 10) | rem) ^ 0x5412
    const bit = (i: number) => ((f >>> i) & 1) === 1
    for (let i = 0; i <= 5; i++) setFn(8, i, bit(i))
    setFn(8, 7, bit(6))
    setFn(8, 8, bit(7))
    setFn(7, 8, bit(8))
    for (let i = 9; i < 15; i++) setFn(14 - i, 8, bit(i))
    for (let i = 0; i < 8; i++) setFn(size - 1 - i, 8, bit(i))
    for (let i = 8; i < 15; i++) setFn(8, size - 15 + i, bit(i))
    setFn(8, size - 8, true)
  }
  drawFormat(0)
  if (version >= 7) {
    let rem = version
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25)
    const v = (version << 12) | rem
    for (let i = 0; i < 18; i++) {
      const dark = ((v >>> i) & 1) === 1
      const a = size - 11 + (i % 3)
      const b = Math.floor(i / 3)
      setFn(a, b, dark)
      setFn(b, a, dark)
    }
  }

  // Data in the two-column zigzag, skipping function modules.
  let bi = 0
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5
    for (let vert = 0; vert < size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j
        const up = ((right + 1) & 2) === 0
        const y = up ? size - 1 - vert : vert
        if (!fn[y]![x] && bi < words.length * 8) {
          modules[y]![x] = ((words[bi >>> 3]! >>> (7 - (bi & 7))) & 1) === 1
          bi++
        }
      }
    }
  }

  const applyMask = (mask: number) => {
    for (let y = 0; y < size; y++) {
      for (let x = 0; x < size; x++) {
        if (fn[y]![x]) continue
        let inv: boolean
        switch (mask) {
          case 0: inv = (x + y) % 2 === 0; break
          case 1: inv = y % 2 === 0; break
          case 2: inv = x % 3 === 0; break
          case 3: inv = (x + y) % 3 === 0; break
          case 4: inv = (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0; break
          case 5: inv = ((x * y) % 2) + ((x * y) % 3) === 0; break
          case 6: inv = (((x * y) % 2) + ((x * y) % 3)) % 2 === 0; break
          default: inv = (((x + y) % 2) + ((x * y) % 3)) % 2 === 0
        }
        if (inv) modules[y]![x] = !modules[y]![x]
      }
    }
  }

  let mask = forceMask ?? -1
  if (mask < 0) {
    let best = Infinity
    for (let m = 0; m < 8; m++) {
      applyMask(m)
      drawFormat(m)
      const p = penalty(modules)
      if (p < best) {
        best = p
        mask = m
      }
      applyMask(m) // XOR again undoes it
    }
  }
  applyMask(mask)
  drawFormat(mask)
  return { version, size, mask, modules }
}

/** Standard penalty score (rules N1–N4) used to pick the mask. */
function penalty(mod: boolean[][]): number {
  const size = mod.length
  let p = 0
  const line = (get: (i: number) => boolean) => {
    let run = 1
    for (let i = 1; i <= size; i++) {
      if (i < size && get(i) === get(i - 1)) run++
      else {
        if (run >= 5) p += 3 + run - 5
        run = 1
      }
    }
    const pat = [true, false, true, true, true, false, true]
    for (let i = 0; i + 7 <= size; i++) {
      if (!pat.every((v, k) => get(i + k) === v)) continue
      const lightBefore = [1, 2, 3, 4].every((k) => i - k < 0 || !get(i - k))
      const lightAfter = [0, 1, 2, 3].every((k) => i + 7 + k >= size || !get(i + 7 + k))
      if (lightBefore || lightAfter) p += 40
    }
  }
  for (let y = 0; y < size; y++) line((x) => mod[y]![x]!)
  for (let x = 0; x < size; x++) line((y) => mod[y]![x]!)
  let dark = 0
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const c = mod[y]![x]!
      if (c) dark++
      if (x + 1 < size && y + 1 < size && c === mod[y]![x + 1] && c === mod[y + 1]![x] && c === mod[y + 1]![x + 1]) p += 3
    }
  }
  p += Math.floor(Math.abs((dark * 100) / (size * size) - 50) / 5) * 10
  return p
}

/** An SVG path (one unit per module, 4-module quiet zone) for a QrMatrix. */
export function qrSvgPath(q: QrMatrix, border = 4): string {
  const parts: string[] = []
  for (let y = 0; y < q.size; y++) {
    for (let x = 0; x < q.size; x++) if (q.modules[y]![x]) parts.push(`M${x + border},${y + border}h1v1h-1z`)
  }
  return parts.join('')
}
