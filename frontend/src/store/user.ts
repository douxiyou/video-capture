import { defineStore } from 'pinia'
interface UserState {
  name: string
  token: string
  userId: number | null
}
export const useUserStore = defineStore('user', {
  state: (): UserState => ({
    // 用户信息
    name: '',
    token: '',
    userId: null,
  }),
  actions: {
    setUserName(name: string) {
      this.name = name
    },
    setSession(name: string, userId: number, token: string) {
      this.name = name
      this.userId = userId
      this.token = token
    },
    clearSession() {
      this.userId = null
      this.token = ''
    }
  },
  persist: { pick: ['name'] }
})