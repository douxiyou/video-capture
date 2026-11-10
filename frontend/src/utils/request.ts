import axios from 'axios'
import { toast } from 'vue-sonner'

declare module 'axios' {
  export interface AxiosRequestConfig {
    silentError?: boolean
  }
}

const request = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '',
  timeout: 15000,
})

request.interceptors.request.use(
  config => config,
  error => Promise.reject(error),
)

request.interceptors.response.use(
  response => response,
  (error: unknown) => {
    if (axios.isCancel(error)) return Promise.reject(error)
    if (axios.isAxiosError(error) && error.config?.silentError) return Promise.reject(error)

    if (axios.isAxiosError(error)) {
      if (error.code === 'ECONNABORTED' || error.code === 'ETIMEDOUT') {
        toast.error('请求超时，请稍后重试')
      } else if (error.response) {
        toast.error(`请求失败（${error.response.status}）`)
      } else if (error.request) {
        toast.error('网络连接失败，请检查网络')
      } else {
        toast.error('请求配置错误，请稍后重试')
      }
    } else {
      toast.error('请求发生未知错误，请稍后重试')
    }

    return Promise.reject(error)
  },
)

export default request