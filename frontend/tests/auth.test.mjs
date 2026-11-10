import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createServer } from 'vite'

let server
let cacheDir
let alignFace
let tokenExpiry

before(async () => {
  cacheDir = await mkdtemp(join(tmpdir(), 'video-capture-test-'))
  server = await createServer({ configFile: false, cacheDir, server: { middlewareMode: true }, appType: 'custom' })
  ;({ alignFace } = await server.ssrLoadModule('/src/utils/alignFace.ts'))
  ;({ tokenExpiry } = await server.ssrLoadModule('/src/utils/auth.ts'))
})

after(async () => {
  await server?.close()
  if (cacheDir) await rm(cacheDir, { recursive: true, force: true })
})

test('face alignment generates a 112x112 image with the expected transform', () => {
  const landmarks = Array.from({ length: 468 }, () => ({ x: 0.5, y: 0.5 }))
  const set = (index, x, y) => { landmarks[index] = { x: x / 112, y: y / 112 } }
  set(33, 38.2946, 51.6963)
  set(133, 38.2946, 51.6963)
  set(362, 73.5318, 51.5014)
  set(263, 73.5318, 51.5014)
  set(1, 56.0252, 71.7366)
  set(61, 41.5493, 92.3655)
  set(291, 70.7299, 92.2041)

  let transform
  const canvas = {
    width: 0,
    height: 0,
    getContext: () => ({ setTransform: (...values) => { transform = values }, drawImage: () => {} }),
    toDataURL: () => 'data:image/jpeg;base64,aW1hZ2U=',
  }
  const previous = globalThis.document
  globalThis.document = { createElement: () => canvas }
  try {
    const video = { videoWidth: 224, videoHeight: 224 }
    assert.equal(alignFace(video, landmarks), 'aW1hZ2U=')
    assert.equal(canvas.width, 112)
    assert.equal(canvas.height, 112)
    assert.ok(Math.abs(transform[0] - 0.5) < 1e-6)
    assert.ok(Math.abs(transform[3] - 0.5) < 1e-6)
    assert.ok(Math.abs(transform[4]) < 1e-6)
    assert.ok(Math.abs(transform[5]) < 1e-6)
    assert.throws(() => alignFace(video, []), /完整面部/)
  } finally {
    globalThis.document = previous
  }
})

test('user token expiry must be present and finite', () => {
  const payload = Buffer.from(JSON.stringify({ exp: 2000000000 })).toString('base64url')
  assert.equal(tokenExpiry(`header.${payload}.signature`), 2000000000000)
  assert.throws(() => tokenExpiry('invalid'))
  assert.throws(() => tokenExpiry('header.e30.signature'))
})
