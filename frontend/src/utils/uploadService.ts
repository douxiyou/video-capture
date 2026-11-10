import { toast } from 'vue-sonner'
import { ref } from 'vue'
import { useImageStore } from '../store/image'
import { uploadFailure, uploadImage } from './imageUpload'
import { UploadQueue } from './uploadQueue'

let queue: UploadQueue | undefined
let unauthorized: (() => void) | undefined
export const uploadQueueBlocked = ref(false)

export function useUploadQueue(onUnauthorized: () => void): UploadQueue {
  unauthorized = onUnauthorized
  if (!queue) {
    const store = useImageStore()
    queue = new UploadQueue({
      images: () => store.images,
      update: (id, changes) => store.updateImage(id, changes),
      upload: uploadImage,
      failure: uploadFailure,
      onStorageError: error => {
        uploadQueueBlocked.value = true
        console.error('保存上传状态失败', error)
        toast.error('无法更新上传状态，后台上传已暂停。请检查浏览器存储后重试')
      },
      onRecovered: () => { uploadQueueBlocked.value = false },
      onUnauthorized: () => unauthorized?.(),
    })
  }
  return queue
}
