import { acceptHMRUpdate, defineStore } from 'pinia'

export interface CapturedImage {
  src: string
  expression: string
  createdAt: number
  id?: string
  userId?: number
  uploadStatus?: 'queued' | 'uploading' | 'uploaded' | 'failed' | 'unknown'
  uploadMessage?: string
  uploadId?: number
}

export const useImageStore = defineStore('image', {
  state: () => ({
    images: [] as (CapturedImage | string)[]
  }),
  actions: {
    saveImages(images: (CapturedImage | string)[]) {
      localStorage.setItem('image', JSON.stringify({ images }))
      this.images = images
    },
    addImage(image: CapturedImage) {
      this.saveImages([...this.images, image])
    },
    removeImage(index: number) {
      this.saveImages(this.images.filter((_, i) => i !== index))
    },
    updateImage(id: string, changes: Partial<CapturedImage>) {
      const index = this.images.findIndex(image => typeof image !== 'string' && image.id === id)
      if (index < 0) return
      const images = [...this.images]
      images[index] = { ...images[index] as CapturedImage, ...changes }
      this.saveImages(images)
    }
  },
  persist: true
})

if (import.meta.hot) {
  import.meta.hot.accept(acceptHMRUpdate(useImageStore, import.meta.hot))
}