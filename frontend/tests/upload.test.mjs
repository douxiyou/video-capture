import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createServer } from 'vite'
import axios from 'axios'
import { createPinia } from 'pinia'

let server, cacheDir, UploadQueue, uploadImage, uploadFailure, request, useImageStore

before(async () => {
  cacheDir = await mkdtemp(join(tmpdir(), 'video-capture-upload-test-'))
  server = await createServer({ configFile: false, cacheDir, server: { middlewareMode: true, ws: false }, appType: 'custom' })
  ;({ UploadQueue } = await server.ssrLoadModule('/src/utils/uploadQueue.ts'))
  ;({ uploadImage, uploadFailure } = await server.ssrLoadModule('/src/utils/imageUpload.ts'))
  ;({ default: request } = await server.ssrLoadModule('/src/utils/request.ts'))
  ;({ useImageStore } = await server.ssrLoadModule('/src/store/image.ts'))
})

after(async () => {
  await server?.close()
  if (cacheDir) await rm(cacheDir, { recursive: true, force: true })
})

const image = (id, userId = 1, uploadStatus = 'queued') => ({
  id, userId, uploadStatus, src: 'data:image/jpeg;base64,aW1hZ2U=', expression: '微笑', createdAt: 1,
})
const tick = () => new Promise(resolve => setImmediate(resolve))

function harness(images, upload, extra = {}) {
  const queue = new UploadQueue({
    images: () => images,
    update: (id, changes) => Object.assign(images.find(item => item.id === id), changes),
    upload,
    failure: uploadFailure,
    onStorageError: error => { throw error },
    onUnauthorized: () => {},
    ...extra,
  })
  return queue
}

test('captures are queued without waiting and uploads have bounded concurrency', async () => {
  const images = Array.from({ length: 7 }, (_, i) => image(String(i)))
  const pending = []
  const queue = harness(images, (item, token) => new Promise(resolve => pending.push({ item, token, resolve })))
  queue.start(1, 'user-token')
  assert.equal(pending.length, 3)
  assert.deepEqual(images.map(item => item.uploadStatus), ['uploading', 'uploading', 'uploading', 'queued', 'queued', 'queued', 'queued'])
  assert.ok(pending.every(item => item.token === 'user-token'))
  pending[0].resolve(123)
  await tick()
  assert.equal(images[0].uploadStatus, 'uploaded')
  assert.equal(images[0].uploadId, 123)
  assert.equal(pending.length, 4)
  for (let i = 1; i < 7; i++) {
    pending[i].resolve(i + 123)
    await tick()
  }
  assert.ok(images.every(item => item.uploadStatus === 'uploaded'))
})

test('queue pauses on logout and only resumes matching user work', async () => {
  const images = [image('1'), image('2'), image('3', 2)]
  const pending = []
  const queue = harness(images, (item, token) => new Promise(resolve => pending.push({ item, token, resolve })), { concurrency: 1 })
  queue.start(1, 'first')
  queue.pause()
  pending[0].resolve(1)
  await tick()
  assert.equal(pending.length, 1)
  queue.start(2, 'second')
  assert.equal(pending[1].item.id, '3')
  pending[1].resolve(2)
  await tick()
  assert.equal(images[1].uploadStatus, 'queued')
  queue.start(1, 'new-token')
  assert.equal(pending[2].token, 'new-token')
  pending[2].resolve(3)
  await tick()
})

test('interrupted uploads are uncertain and are not resent automatically', () => {
  const images = [image('1', 1, 'uploading'), image('2', 1, 'unknown'), image('3', 1, 'failed')]
  let calls = 0
  const queue = harness(images, async () => { calls++; return 1 })
  queue.start(1, 'token')
  assert.equal(images[0].uploadStatus, 'unknown')
  assert.equal(calls, 0)
})

test('storage failure blocks dispatch before the request', () => {
  const images = [image('1')]
  let calls = 0
  let errors = 0
  const queue = harness(images, async () => { calls++; return 1 }, {
    update: () => { throw new Error('quota') },
    onStorageError: () => { errors++ },
  })
  queue.start(1, 'token')
  assert.equal(calls, 0)
  assert.equal(errors, 1)
})

test('real image store persists queued and uploaded state', async () => {
  const previous = globalThis.localStorage
  const values = new Map()
  globalThis.localStorage = {
    setItem: (key, value) => values.set(key, value),
    getItem: key => values.get(key) ?? null,
  }
  try {
    const store = useImageStore(createPinia())
    store.addImage(image('photo'))
    const queue = new UploadQueue({
      images: () => store.images,
      update: (id, changes) => store.updateImage(id, changes),
      upload: async () => 42,
      failure: uploadFailure,
      onStorageError: error => { throw error },
      onUnauthorized: () => {},
    })
    queue.start(1, 'token')
    await tick()
    assert.equal(JSON.parse(values.get('image')).images[0].uploadStatus, 'uploaded')
    assert.equal(store.images[0].uploadId, 42)
  } finally {
    globalThis.localStorage = previous
  }
})

test('a blocked queue can resume after the store action becomes available', async () => {
  const images = [image('1'), image('2')]
  let available = false
  let errors = 0
  let calls = 0
  const queue = harness(images, async () => ++calls, {
    update: (id, changes) => {
      if (!available) throw new TypeError('store.updateImage is not a function')
      Object.assign(images.find(item => item.id === id), changes)
    },
    onStorageError: () => { errors++ },
  })
  queue.start(1, 'token')
  assert.equal(errors, 1)
  assert.equal(calls, 0)
  available = true
  queue.resume()
  await tick()
  assert.equal(calls, 2)
  assert.ok(images.every(item => item.uploadStatus === 'uploaded'))
})

test('resuming persists completed results before sending more uploads', async () => {
  const images = [image('1'), image('2')]
  let available = false
  const calls = []
  const queue = harness(images, async item => { calls.push(item.id); return 42 }, {
    concurrency: 1,
    update: (id, changes) => {
      if (!available && changes.uploadStatus === 'uploaded') throw new Error('quota')
      Object.assign(images.find(item => item.id === id), changes)
    },
    onStorageError: () => {},
  })
  queue.start(1, 'token')
  await tick()
  assert.deepEqual(calls, ['1'])
  queue.resume()
  assert.deepEqual(calls, ['1'])
  available = true
  queue.resume()
  await tick()
  assert.deepEqual(calls, ['1', '2'])
  assert.equal(images[0].uploadId, 42)
  assert.ok(images.every(item => item.uploadStatus === 'uploaded'))
})

test('expired credentials pause the queue until login and explicit retry', async () => {
  const images = [image('1'), image('2')]
  let calls = 0
  let unauthorized = 0
  const queue = harness(images, async () => {
    calls++
    throw new axios.AxiosError('expired', undefined, undefined, undefined, {
      status: 401, data: {}, headers: {}, config: {},
    })
  }, { concurrency: 1, onUnauthorized: () => { unauthorized++ } })
  queue.start(1, 'expired')
  await tick()
  assert.equal(unauthorized, 1)
  assert.equal(calls, 1)
  assert.equal(images[0].uploadStatus, 'failed')
  assert.equal(images[1].uploadStatus, 'queued')
})

test('failure to persist a completed upload pauses further requests', async () => {
  const images = [image('1'), image('2')]
  let calls = 0
  let errors = 0
  const queue = harness(images, async () => { calls++; return 42 }, {
    concurrency: 1,
    update: (id, changes) => {
      if (changes.uploadStatus === 'uploaded') throw new Error('quota')
      Object.assign(images.find(item => item.id === id), changes)
    },
    onStorageError: () => { errors++ },
  })
  queue.start(1, 'token')
  await tick()
  assert.equal(calls, 1)
  assert.equal(errors, 1)
  assert.equal(images[0].uploadStatus, 'uploading')
  assert.equal(images[1].uploadStatus, 'queued')
})

test('multipart upload uses image bytes, expression and bearer token', async () => {
  const previous = request.defaults.adapter
  request.defaults.adapter = async config => {
    assert.equal(config.url, '/api/images')
    assert.equal(config.headers.get('Authorization'), 'Bearer token')
    assert.equal(config.data.get('expression'), '微笑')
    assert.equal(config.data.get('file').type, 'image/jpeg')
    assert.equal(await config.data.get('file').text(), 'image')
    return { data: { id: 17 }, status: 201, statusText: 'Created', headers: {}, config }
  }
  try {
    assert.equal(await uploadImage(image('photo'), 'token'), 17)
  } finally {
    request.defaults.adapter = previous
  }
})

test('unknown outcomes are not treated as safe failures', async () => {
  const network = new axios.AxiosError('network')
  assert.equal(uploadFailure(network).uploadStatus, 'unknown')
  const response = new axios.AxiosError('error', undefined, undefined, undefined, {
    status: 502, data: { upload_id: 42, status: 'pending' }, headers: {}, config: {},
  })
  assert.equal(uploadFailure(response).uploadId, 42)
  assert.equal(uploadFailure(response).uploadStatus, 'unknown')
  const unauthorized = new axios.AxiosError('error', undefined, undefined, undefined, {
    status: 401, data: {}, headers: {}, config: {},
  })
  assert.equal(uploadFailure(unauthorized).uploadStatus, 'failed')
  const previous = request.defaults.adapter
  request.defaults.adapter = async config => ({ data: {}, status: 201, statusText: 'Created', headers: {}, config })
  try {
    await assert.rejects(uploadImage(image('photo'), 'token'), error => uploadFailure(error).uploadStatus === 'unknown')
  } finally {
    request.defaults.adapter = previous
  }
})
