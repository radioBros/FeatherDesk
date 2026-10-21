// SPDX-License-Identifier: AGPL-3.0-only

// @vitest-environment node
/// <reference types="node" />
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { encodeQr, QR_MAX_VERSION, type QrMatrix } from './qr'

/**
 * FE-071: the hand-written QR encoder. The golden hashes were produced by this
 * encoder and each symbol was decoded back with zbarimg (ZBar) during
 * development; when zbarimg is installed the decode check runs again here, for
 * every version 1–15 and all eight masks.
 */
const hash = (q: QrMatrix) => createHash('sha256').update(q.modules.map((r) => r.map((c) => (c ? '1' : '0')).join('')).join('\n')).digest('hex')
const OTPAUTH = 'otpauth://totp/SimpleTickets:ada%40example.test?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=SimpleTickets&algorithm=SHA1&digits=6&period=30'

function hasZbar(): boolean {
  try {
    execFileSync('zbarimg', ['--version'], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
}

function decode(q: QrMatrix): string {
  const scale = 4
  const border = 4
  const w = (q.size + 2 * border) * scale
  const rows: string[] = []
  for (let y = 0; y < w; y++) {
    const r: string[] = []
    for (let x = 0; x < w; x++) {
      const mx = Math.floor(x / scale) - border
      const my = Math.floor(y / scale) - border
      r.push(mx >= 0 && my >= 0 && mx < q.size && my < q.size && q.modules[my]![mx] ? '1' : '0')
    }
    rows.push(r.join(' '))
  }
  const file = join(mkdtempSync(join(tmpdir(), 'qr-')), 'q.pbm')
  writeFileSync(file, `P1\n${w} ${w}\n${rows.join('\n')}\n`)
  return execFileSync('zbarimg', ['-q', '--raw', file], { encoding: 'utf8' }).replace(/\n$/, '')
}

describe('QR encoder (FE-071)', () => {
  it('matches the verified golden symbols', () => {
    const cases: [string, number, number, string][] = [
      ['hello', 1, 7, '683921f56b2988dd454e5f08cc85d949682bd846d510fe13aa028a95d1427918'],
      [OTPAUTH, 8, 3, 'ea19735005182cab9babf86224e9d4369055dd98c089f2ce0f6281d0547b32a6'],
      ['ab1'.repeat(100), 13, 2, 'd18107f4dc51e258975d5e15c88c32e24969f7ec795a7c7e25bf57e78dbf6a83'],
    ]
    for (const [text, version, mask, digest] of cases) {
      const q = encodeQr(text)
      expect([q.version, q.mask, hash(q)]).toEqual([version, mask, digest])
    }
  })

  it('draws the finder, timing and dark-module patterns', () => {
    const q = encodeQr(OTPAUTH)
    expect(q.size).toBe(q.version * 4 + 17)
    for (const [x, y] of [[0, 0], [q.size - 7, 0], [0, q.size - 7]]) {
      for (let i = 0; i < 7; i++) {
        expect(q.modules[y!]![x! + i]).toBe(true) // finder outer ring, top edge
        expect(q.modules[y! + 6]![x! + i]).toBe(true)
      }
      expect(q.modules[y! + 1]![x! + 1]).toBe(false)
    }
    for (let i = 8; i < q.size - 8; i++) expect(q.modules[6]![i]).toBe(i % 2 === 0)
    expect(q.modules[q.size - 8]![8]).toBe(true)
  })

  it('rejects text beyond version 15', () => {
    expect(QR_MAX_VERSION).toBe(15)
    expect(() => encodeQr('x'.repeat(500))).toThrow(/too long/)
  })

  it.skipIf(!hasZbar())('decodes back with zbarimg for every version and mask', () => {
    for (const n of [1, 20, 40, 60, 80, 100, 115, 130, 180, 210, 240, 280, 320, 360, 410]) {
      const text = 'ab1'.repeat(200).slice(0, n)
      expect(decode(encodeQr(text))).toBe(text)
    }
    for (let m = 0; m < 8; m++) expect(decode(encodeQr(OTPAUTH, m))).toBe(OTPAUTH)
  })
})
