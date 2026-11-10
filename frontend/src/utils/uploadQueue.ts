import type { CapturedImage } from '../store/image'

type Image = CapturedImage | string
type Changes = Partial<CapturedImage>

interface QueueOptions {
  images: () => Image[]
  update: (id: string, changes: Changes) => void
  upload: (image: CapturedImage, token: string) => Promise<number>
  failure: (error: unknown) => Changes
  onStorageError: (error: unknown) => void
  onRecovered?: () => void
  onUnauthorized: () => void
  concurrency?: number
}

export class UploadQueue {
  private session: { userId: number; token: string } | null = null
  private active = new Set<string>()
  private pendingResults = new Map<string, Changes>()
  private initialized = false
  private blocked = false
  private options: QueueOptions

  constructor(options: QueueOptions) {
    this.options = options
  }

  start(userId: number, token: string) {
    this.session = { userId, token }
    this.resume()
  }

  resume() {
    if (!this.session) return
    try {
      for (const [id, changes] of this.pendingResults) {
        this.options.update(id, changes)
        this.pendingResults.delete(id)
      }
    } catch (error) {
      this.blocked = true
      this.options.onStorageError(error)
      return
    }
    if (!this.initialized) {
      try {
        for (const image of this.options.images()) {
          if (typeof image !== 'string' && image.id && image.uploadStatus === 'uploading' && !this.active.has(image.id)) {
            this.options.update(image.id, { uploadStatus: 'unknown', uploadMessage: '页面关闭前上传未完成，请联系管理员核对，勿重复上传' })
          }
        }
        this.initialized = true
      } catch (error) {
        this.blocked = true
        this.options.onStorageError(error)
        return
      }
    }
    this.blocked = false
    this.options.onRecovered?.()
    this.pump()
  }

  pause() {
    this.session = null
  }

  pump() {
    while (this.session && !this.blocked && this.active.size < (this.options.concurrency ?? 3)) {
      const session = this.session
      const image = this.options.images().find((item): item is CapturedImage =>
        typeof item !== 'string' && !!item.id && item.userId === session.userId &&
        item.uploadStatus === 'queued' && !this.active.has(item.id))
      if (!image?.id) return
      const id = image.id
      try {
        this.options.update(id, { uploadStatus: 'uploading', uploadMessage: undefined, uploadId: undefined })
      } catch (error) {
        this.blocked = true
        this.options.onStorageError(error)
        return
      }
      this.active.add(id)
      void this.run(image, id, session)
    }
  }

  private async run(image: CapturedImage, id: string, session: { userId: number; token: string }) {
    let changes: Changes
    try {
      const uploadId = await this.options.upload(image, session.token)
      changes = { uploadStatus: 'uploaded', uploadId }
    } catch (error) {
      changes = this.options.failure(error)
      if (changes.uploadStatus === 'failed' && changes.uploadMessage === '登录已失效，请重新登录' &&
          this.session?.token === session.token) {
        this.pause()
        this.options.onUnauthorized()
      }
    }
    try {
      this.options.update(id, changes)
    } catch (error) {
      this.pendingResults.set(id, changes)
      this.blocked = true
      this.options.onStorageError(error)
    } finally {
      this.active.delete(id)
      this.pump()
    }
  }
}
