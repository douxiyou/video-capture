import axios from 'axios'
import type { CapturedImage } from '../store/image'
import request from './request'

class UnknownUploadResult extends Error {}

export async function uploadImage(image: CapturedImage, token: string): Promise<number> {
  const match = /^data:image\/jpeg;base64,(.+)$/.exec(image.src)
  if (!match) throw new Error('照片格式无效')
  const bytes = Uint8Array.from(atob(match[1]!), char => char.charCodeAt(0))
  const form = new FormData()
  form.append('file', new Blob([bytes], { type: 'image/jpeg' }), `${image.id}.jpg`)
  form.append('expression', image.expression)
  const { data } = await request.post<{ id: number }>('/api/images', form, {
    headers: { Authorization: `Bearer ${token}` },
    silentError: true,
  })
  if (!Number.isSafeInteger(data?.id) || data.id <= 0) throw new UnknownUploadResult('上传响应无效')
  return data.id
}

export function uploadFailure(error: unknown): Pick<CapturedImage, 'uploadStatus' | 'uploadMessage' | 'uploadId'> {
  if (error instanceof UnknownUploadResult) {
    return { uploadStatus: 'unknown', uploadMessage: '上传响应无效，请联系管理员核对，勿重复上传' }
  }
  if (axios.isAxiosError(error)) {
    const data: unknown = error.response?.data
    const uploadId = typeof data === 'object' && data !== null && 'upload_id' in data &&
      typeof data.upload_id === 'number' ? data.upload_id : undefined
    if (uploadId === undefined && [400, 401, 404, 429].includes(error.response?.status ?? 0)) {
      const messages: Record<number, string> = {
        400: '照片不符合上传要求',
        401: '登录已失效，请重新登录',
        404: '上传接口不可用，请检查后端 OSS 配置',
        429: '请求过于频繁，请稍后重试',
      }
      return { uploadStatus: 'failed', uploadMessage: messages[error.response!.status], uploadId }
    }
    return { uploadStatus: 'unknown', uploadMessage: '上传结果不确定，请联系管理员核对，勿重复上传', uploadId }
  }
  return { uploadStatus: 'failed', uploadMessage: error instanceof Error ? error.message : '照片处理失败' }
}
