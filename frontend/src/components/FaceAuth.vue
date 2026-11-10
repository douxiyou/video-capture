<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, useTemplateRef } from 'vue'
import { FaceLandmarker, FilesetResolver } from '@mediapipe/tasks-vision'
import { Camera, RefreshCw, ScanFace } from '@lucide/vue'
import { alignFace } from '../utils/alignFace'
import { authenticate, authError, getCaptcha, getGuest, tokenExpiry, type AuthResult } from '../utils/auth'

const props = defineProps<{ initialName: string }>()
const emit = defineEmits<{ authenticated: [result: AuthResult] }>()
const dialog = useTemplateRef<HTMLDialogElement>('dialog')
const video = useTemplateRef<HTMLVideoElement>('video')
const name = ref(props.initialName)
const mode = ref<'register' | 'login'>('register')
const captcha = ref<{ captcha_id: string; image: string } | null>(null)
const answer = ref('')
const error = ref('')
const busy = ref(false)
const loadingCaptcha = ref(false)
const guest = ref('')
const guestExpires = ref(0)
const cameraReady = ref(false)
const modelReady = ref(false)
let stream: MediaStream | null = null
let landmarker: FaceLandmarker | null = null
let disposed = false
let operation = 0

async function loadCaptcha() {
  loadingCaptcha.value = true
  captcha.value = null
  answer.value = ''
  try {
    const result = await getCaptcha()
    if (!disposed) captcha.value = result
  } catch (cause) {
    if (!disposed) error.value = authError(cause, '获取验证码失败，请重试。')
  } finally {
    if (!disposed) loadingCaptcha.value = false
  }
}

function stopCamera() {
  stream?.getTracks().forEach(track => track.stop())
  stream = null
  landmarker?.close()
  landmarker = null
  cameraReady.value = false
  modelReady.value = false
}

async function startCamera() {
  const current = ++operation
  stopCamera()
  try {
    if (!navigator.mediaDevices?.getUserMedia) throw new Error('摄像头不可用，请使用 HTTPS 或 localhost')
    const camera = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'user', width: { ideal: 880 }, height: { ideal: 880 } }, audio: false })
    if (disposed || current !== operation) {
      camera.getTracks().forEach(track => track.stop())
      return
    }
    stream = camera
    await nextTick()
    if (!video.value) throw new Error('摄像头预览不可用')
    video.value.srcObject = camera
    await video.value.play()
    if (disposed || current !== operation) return
    cameraReady.value = true
    const base = import.meta.env.BASE_URL
    const fileset = await FilesetResolver.forVisionTasks(`${base}mediapipe`)
    const model = await FaceLandmarker.createFromOptions(fileset, {
      baseOptions: { modelAssetPath: `${base}mediapipe/face_landmarker.task`, delegate: 'CPU' },
      runningMode: 'VIDEO',
      numFaces: 2,
    })
    if (disposed || current !== operation) {
      model.close()
      return
    }
    landmarker = model
    modelReady.value = true
  } catch (cause) {
    if (disposed || current !== operation) return
    console.error('认证摄像头或人脸模型启动失败', cause)
    stopCamera()
    error.value = '摄像头或人脸关键点模型无法启动，请检查权限和本地模型后重试。'
  }
}

async function verifyCaptcha() {
  if (busy.value || loadingCaptcha.value || guest.value) return
  if (!captcha.value || !answer.value.trim()) {
    error.value = '请输入图片验证码。'
    return
  }
  busy.value = true
  error.value = ''
  try {
    const result = await getGuest(captcha.value.captcha_id, answer.value.trim())
    if (disposed) return
    guest.value = result.guest_token
    guestExpires.value = Date.now() + result.expires_in * 1000
    captcha.value = null
    void startCamera()
  } catch (cause) {
    if (disposed) return
    error.value = authError(cause, '验证码校验失败，请重试。')
    await loadCaptcha()
  } finally {
    if (!disposed) busy.value = false
  }
}

async function submitFace() {
  if (busy.value || !guest.value) return
  const userName = name.value.trim()
  if (!userName || [...userName].length > 30) {
    error.value = '请输入 1 至 30 个字符的用户名。'
    return
  }
  if (Date.now() >= guestExpires.value) {
    guest.value = ''
    stopCamera()
    error.value = '临时凭证已过期，请重新验证验证码。'
    await loadCaptcha()
    return
  }
  const currentVideo = video.value
  if (!cameraReady.value || !landmarker || !currentVideo || currentVideo.readyState < HTMLMediaElement.HAVE_CURRENT_DATA) {
    error.value = '摄像头或人脸模型尚未准备好。'
    return
  }
  let image: string
  try {
    const faces = landmarker.detectForVideo(currentVideo, performance.now()).faceLandmarks
    if (faces.length !== 1 || !faces[0]) throw new Error('请确保画面中有且仅有一张人脸')
    image = alignFace(currentVideo, faces[0])
  } catch (cause) {
    console.error('采集人脸失败', cause)
    error.value = cause instanceof Error ? cause.message : '人脸采集失败，请重试。'
    return
  }
  busy.value = true
  error.value = ''
  const token = guest.value
  guest.value = ''
  try {
    const result = await authenticate(mode.value, token, userName, image)
    if (tokenExpiry(result.user_token) <= Date.now()) throw new Error('认证服务返回了过期凭证')
    if (!disposed) emit('authenticated', result)
  } catch (cause) {
    if (disposed) return
    error.value = `${authError(cause, '人脸验证失败，请重新验证。')} 如已注册成功，请选择登录。`
    stopCamera()
    await loadCaptcha()
  } finally {
    if (!disposed) busy.value = false
  }
}

onMounted(() => {
  dialog.value?.showModal()
  void loadCaptcha()
})

onUnmounted(() => {
  disposed = true
  operation++
  stopCamera()
})
</script>

<template>
  <dialog ref="dialog" class="m-auto max-h-[95vh] w-[calc(100%-2rem)] max-w-lg overflow-y-auto rounded-2xl bg-white p-0 text-slate-800 shadow-2xl backdrop:bg-slate-950/60" aria-labelledby="auth-title" @cancel.prevent>
    <div class="p-6 sm:p-8">
      <div class="flex size-12 items-center justify-center rounded-2xl bg-indigo-50 text-indigo-600"><ScanFace :size="23" /></div>
      <h2 id="auth-title" class="mt-4 text-xl font-bold text-slate-900">人脸注册与登录</h2>
      <p class="mt-2 text-sm leading-6 text-slate-500">通过验证码后采集人脸，验证成功才能进入工作台。</p>
      <p class="mt-3 rounded-lg bg-amber-50 p-3 text-xs leading-5 text-amber-800">实验功能：尚未实现活体检测，不能防止照片重放，不能用于可靠身份验证。验证图像将发送至服务器提取并加密保存人脸特征，原图不会保存；删除账户可删除特征。</p>
      <div class="mt-5 flex gap-2" role="group" aria-label="认证方式">
        <button v-for="option in (['register', 'login'] as const)" :key="option" type="button" :aria-pressed="mode === option" :disabled="busy" class="flex-1 rounded-lg border px-3 py-2 text-sm font-medium disabled:opacity-50" :class="mode === option ? 'border-indigo-400 bg-indigo-50 text-indigo-700' : 'border-slate-200 text-slate-500'" @click="mode = option">{{ option === 'register' ? '新用户注册' : '已有账号登录' }}</button>
      </div>
      <label for="auth-name" class="mt-5 block text-sm font-semibold">用户名</label>
      <input id="auth-name" v-model="name" maxlength="60" autocomplete="username" placeholder="输入用户名" class="mt-2 w-full rounded-xl border border-slate-200 px-4 py-3 text-sm outline-none focus:border-indigo-500 focus:ring-4 focus:ring-indigo-50" />

      <template v-if="!guest">
        <label for="captcha-answer" class="mt-4 block text-sm font-semibold">图片验证码</label>
        <div class="mt-2 flex items-center gap-2">
          <input id="captcha-answer" v-model="answer" inputmode="numeric" maxlength="6" autocomplete="off" placeholder="输入验证码" class="min-w-0 flex-1 rounded-xl border border-slate-200 px-4 py-3 text-sm outline-none focus:border-indigo-500" @keydown.enter.prevent="verifyCaptcha" />
          <img v-if="captcha" :src="captcha.image" alt="图片验证码" class="h-12 w-36 rounded-lg border border-slate-200 object-contain" />
          <button type="button" :disabled="loadingCaptcha || busy" aria-label="刷新验证码" class="rounded-lg p-2 text-indigo-600 hover:bg-indigo-50 disabled:opacity-50" @click="loadCaptcha"><RefreshCw :size="18" /></button>
        </div>
        <button type="button" :disabled="loadingCaptcha || busy || !captcha" class="mt-5 w-full rounded-xl bg-indigo-600 px-4 py-3 text-sm font-semibold text-white hover:bg-indigo-700 disabled:opacity-50" @click="verifyCaptcha">{{ busy ? '验证中…' : '验证并开启摄像头' }}</button>
      </template>
      <template v-else>
        <div class="relative mt-5 overflow-hidden rounded-xl bg-slate-900">
          <video ref="video" autoplay playsinline muted class="aspect-square max-h-64 w-full object-contain" aria-label="人脸采集预览"></video>
          <p v-if="!cameraReady" class="absolute inset-0 flex items-center justify-center text-sm text-white">等待摄像头…</p>
        </div>
        <p class="mt-2 text-xs leading-5 text-slate-500">{{ modelReady ? '请正对镜头，保持画面中只有一张完整人脸。' : cameraReady ? '正在加载人脸关键点模型…' : '请允许访问摄像头。' }}</p>
        <button v-if="!modelReady && !cameraReady" type="button" class="mt-3 text-sm font-medium text-indigo-600" @click="startCamera">重新启动摄像头</button>
        <button type="button" :disabled="busy || !modelReady" class="mt-4 flex w-full items-center justify-center gap-2 rounded-xl bg-indigo-600 px-4 py-3 text-sm font-semibold text-white hover:bg-indigo-700 disabled:opacity-50" @click="submitFace"><Camera :size="17" /> {{ busy ? '验证中…' : mode === 'register' ? '同意采集并注册' : '同意采集并登录' }}</button>
      </template>
      <p v-if="error" role="alert" class="mt-4 text-sm leading-5 text-red-600">{{ error }}</p>
    </div>
  </dialog>
</template>
