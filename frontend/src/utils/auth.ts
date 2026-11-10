import axios from 'axios'
import request from './request'

export interface AuthResult {
  user_id: number
  name: string
  user_token: string
  expires_in: number
}

export interface UserInfo {
  user_id: number
  name: string
}

export function tokenExpiry(token: string): number {
  const payload = token.split('.')[1]
  if (!payload) throw new Error('用户凭证格式无效')
  const base64 = payload.replace(/-/g, '+').replace(/_/g, '/')
  const data: unknown = JSON.parse(atob(base64.padEnd(Math.ceil(base64.length / 4) * 4, '=')))
  if (typeof data !== 'object' || data === null || !('exp' in data) ||
    typeof data.exp !== 'number' || !Number.isFinite(data.exp)) {
    throw new Error('用户凭证缺少有效期')
  }
  return data.exp * 1000
}

export async function getCaptcha() {
  const { data } = await request.get<{ captcha_id: string; image: string }>('/api/captcha', { silentError: true })
  return data
}

export async function getGuest(captchaID: string, answer: string) {
  const { data } = await request.post<{ guest_token: string; expires_in: number }>('/api/guest',
    { captcha_id: captchaID, answer }, { silentError: true })
  return data
}

export async function authenticate(mode: 'register' | 'login', guestToken: string, name: string, image: string) {
  const { data } = await request.post<AuthResult>(`/api/${mode}`, { name, image }, {
    headers: { Authorization: `Bearer ${guestToken}` },
    silentError: true,
  })
  return data
}

export async function getUser(token: string) {
  const { data } = await request.get<UserInfo>('/api/user', {
    headers: { Authorization: `Bearer ${token}` },
    silentError: true,
  })
  return data
}

export async function deleteUser(token: string) {
  await request.delete('/api/user', {
    headers: { Authorization: `Bearer ${token}` },
    silentError: true,
  })
}

export function authError(error: unknown, fallback: string): string {
  if (!axios.isAxiosError(error)) return fallback
  if (error.response?.status === 429) return '请求过于频繁，请稍后再试。'
  if (error.response?.status === 404) return '认证接口不可用，请检查后端是否启用实验性人脸注册。'
  if (error.response?.status === 409) return '用户名或人脸已注册，请尝试登录或更换用户名。'
  if (error.response?.status === 401) return '验证失败或凭证已过期，请重新验证。'
  if (error.response?.status === 503) return '认证服务暂不可用，请检查后端配置后重试。'
  if (!error.response) return '无法连接认证服务，请检查网络及后端配置。'
  return fallback
}
