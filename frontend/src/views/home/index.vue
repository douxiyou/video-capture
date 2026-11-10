<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, useTemplateRef } from 'vue'
import axios from 'axios'
import { FaceDetector, FilesetResolver } from '@mediapipe/tasks-vision'
import { ArrowDown, Camera, Check, CircleHelp, Keyboard, ScanFace, Sparkles, Trash2, UserRound, Video } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { useImageStore, type CapturedImage } from '../../store/image'
import { useUserStore } from '../../store/user'
import FaceAuth from '../../components/FaceAuth.vue'
import { authError, deleteUser, getUser, tokenExpiry, type AuthResult } from '../../utils/auth'
import { uploadQueueBlocked, useUploadQueue } from '../../utils/uploadService'

type CropRegion = { x: number; y: number; size: number }

const expressions = [
  { name: '自然', emoji: '😐', description: '放松面部' },
  { name: '微笑', emoji: '🙂', description: '轻轻上扬嘴角' },
  { name: '大笑', emoji: '😄', description: '展露笑容' },
  { name: '惊讶', emoji: '😮', description: '睁大双眼' },
  { name: '难过', emoji: '😢', description: '微微低落' },
  { name: '生气', emoji: '😠', description: '皱起眉头' },
]

const userStore = useUserStore()
const imageStore = useImageStore()
const uploadQueue = useUploadQueue(() => {
  toast.error('登录已过期，请重新登录')
  clearSession()
})
const selectedExpression = ref(expressions[0]!)
const showAuth = ref(false)
const sessionLoading = ref(true)
const sessionError = ref('')
const deleting = ref(false)
const confirmDelete = ref(false)
const tokenStorageKey = 'video-capture-user-token'
const videoRef = useTemplateRef<HTMLVideoElement>('videoRef')
const canvasRef = useTemplateRef<HTMLCanvasElement>('canvasRef')
const cameraReady = ref(false)
const cameraLoading = ref(false)
const cameraError = ref('')
const detectionHint = ref('正在准备摄像头…')
const captureNotice = ref('')
const photos = computed(() => imageStore.images.map((image, index) => ({
  image: typeof image === 'string'
    ? { src: image, expression: '未分类', createdAt: 0 }
    : image,
  index,
})).reverse())
const capturedExpressions = computed(() => new Set(photos.value.map(({ image }) => image.expression)))

let faceDetector: FaceDetector | null = null
let cameraStream: MediaStream | null = null
let animationFrameId: number | null = null
let noticeTimer: ReturnType<typeof setTimeout> | undefined
let sessionTimer: ReturnType<typeof setTimeout> | undefined
let smoothedCrop: CropRegion | null = null
let lastFaceDetectedAt = 0
let lastVideoTime = -1
let disposed = false
let cameraGeneration = 0

function drawFullFrame(video: HTMLVideoElement, canvas: HTMLCanvasElement, context: CanvasRenderingContext2D) {
  const scale = Math.min(canvas.width / video.videoWidth, canvas.height / video.videoHeight)
  const width = video.videoWidth * scale
  const height = video.videoHeight * scale
  context.clearRect(0, 0, canvas.width, canvas.height)
  context.drawImage(video, (canvas.width - width) / 2, (canvas.height - height) / 2, width, height)
}

function drawPreview() {
  const video = videoRef.value
  const canvas = canvasRef.value
  const context = canvas?.getContext('2d')
  if (!video || !canvas || !context || disposed) return

  if (video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA && video.videoWidth && video.videoHeight) {
    if (faceDetector && video.currentTime !== lastVideoTime) {
      try {
        lastVideoTime = video.currentTime
        const now = performance.now()
        const box = faceDetector.detectForVideo(video, now).detections[0]?.boundingBox
        if (box) {
          const padding = 300
          const size = Math.min(Math.max(box.width, box.height) + padding, video.videoWidth, video.videoHeight)
          const target = {
            x: Math.max(0, Math.min(video.videoWidth - size, box.originX + box.width / 2 - size / 2)),
            y: Math.max(0, Math.min(video.videoHeight - size, box.originY + box.height / 2 - size / 2)),
            size,
          }
          if (!smoothedCrop) smoothedCrop = target
          else {
            smoothedCrop.x += (target.x - smoothedCrop.x) * 0.18
            smoothedCrop.y += (target.y - smoothedCrop.y) * 0.18
            smoothedCrop.size += (target.size - smoothedCrop.size) * 0.18
          }
          lastFaceDetectedAt = now
          detectionHint.value = '已检测到人脸，保持在画面内'
        } else if (now - lastFaceDetectedAt >= 300) {
          smoothedCrop = null
          detectionHint.value = '请面对镜头，让面部保持在画面内'
        }
      } catch (error) {
        console.error('人脸检测失败', error)
        faceDetector.close()
        faceDetector = null
        smoothedCrop = null
        detectionHint.value = '人脸检测不可用，可继续手动拍照'
        toast.error('人脸检测失败，已切换到普通预览')
      }
    }

    if (smoothedCrop) {
      context.clearRect(0, 0, canvas.width, canvas.height)
      context.drawImage(video, smoothedCrop.x, smoothedCrop.y, smoothedCrop.size, smoothedCrop.size, 0, 0, canvas.width, canvas.height)
    } else {
      drawFullFrame(video, canvas, context)
    }
  }
  animationFrameId = requestAnimationFrame(drawPreview)
}

async function initDetector(generation: number) {
  try {
    const base = import.meta.env.BASE_URL
    const fileset = await FilesetResolver.forVisionTasks(`${base}mediapipe`)
    const detector = await FaceDetector.createFromOptions(fileset, {
      baseOptions: { modelAssetPath: `${base}mediapipe/face_detector.tflite`, delegate: 'GPU' },
      runningMode: 'VIDEO',
      minDetectionConfidence: 0.5,
    })
    if (disposed || generation !== cameraGeneration) {
      detector.close()
      return
    }
    faceDetector = detector
    detectionHint.value = '请面对镜头，让面部保持在画面内'
  } catch (error) {
    if (disposed || generation !== cameraGeneration) return
    console.error('人脸检测初始化失败', error)
    detectionHint.value = '人脸检测不可用，可继续手动拍照'
  }
}

async function startCamera() {
  if (cameraLoading.value || cameraReady.value) return
  const generation = ++cameraGeneration
  cameraLoading.value = true
  cameraError.value = ''
  try {
    if (!navigator.mediaDevices?.getUserMedia) throw new Error('浏览器不支持摄像头访问，请使用 HTTPS 或 localhost')
    const stream = await navigator.mediaDevices.getUserMedia({
      video: { width: { ideal: 880 }, height: { ideal: 880 }, facingMode: 'user' },
      audio: false,
    })
    if (disposed || generation !== cameraGeneration) {
      stream.getTracks().forEach(track => track.stop())
      return
    }
    cameraStream = stream
    const video = videoRef.value
    if (!video) throw new Error('摄像头预览不可用')
    video.srcObject = stream
    await video.play()
    if (disposed || generation !== cameraGeneration) return
    cameraReady.value = true
    detectionHint.value = '摄像头已就绪，正在加载人脸检测'
    drawPreview()
    void initDetector(generation)
  } catch (error) {
    if (disposed || generation !== cameraGeneration) return
    cameraStream?.getTracks().forEach(track => track.stop())
    cameraStream = null
    console.error('摄像头启动失败', error)
    cameraError.value = error instanceof Error && error.message.includes('HTTPS')
      ? error.message
      : '无法访问摄像头，请检查设备连接和浏览器权限'
    detectionHint.value = cameraError.value
  } finally {
    if (generation === cameraGeneration) cameraLoading.value = false
  }
}

function stopCamera() {
  cameraGeneration++
  cameraLoading.value = false
  if (animationFrameId !== null) cancelAnimationFrame(animationFrameId)
  animationFrameId = null
  faceDetector?.close()
  faceDetector = null
  cameraStream?.getTracks().forEach(track => track.stop())
  cameraStream = null
  cameraReady.value = false
  smoothedCrop = null
  lastVideoTime = -1
}

function clearSession() {
  uploadQueue.pause()
  clearTimeout(sessionTimer)
  try {
    sessionStorage.removeItem(tokenStorageKey)
  } catch (error) {
    console.error('清除会话失败', error)
    toast.error('无法清除浏览器会话，请检查浏览器存储设置')
  }
  userStore.clearSession()
  stopCamera()
  confirmDelete.value = false
  showAuth.value = true
}

function scheduleSessionExpiry(token: string) {
  clearTimeout(sessionTimer)
  const remaining = tokenExpiry(token) - Date.now()
  if (remaining <= 0) throw new Error('用户凭证已过期')
  sessionTimer = setTimeout(() => {
    if (userStore.token === token) {
      toast.error('登录已过期，请重新登录')
      clearSession()
    }
  }, Math.min(remaining, 2147483647))
}

async function restoreSession() {
  sessionLoading.value = true
  sessionError.value = ''
  try {
    const token = sessionStorage.getItem(tokenStorageKey)
    if (!token) {
      showAuth.value = true
      return
    }
    const user = await getUser(token)
    if (disposed) return
    scheduleSessionExpiry(token)
    userStore.setSession(user.name, user.user_id, token)
    uploadQueue.start(user.user_id, token)
    showAuth.value = false
    sessionLoading.value = false
    await nextTick()
    await startCamera()
  } catch (error) {
    if (disposed) return
    if (axios.isAxiosError(error) && error.response?.status === 401) {
      clearSession()
    } else {
      console.error('恢复会话失败', axios.isAxiosError(error) ? { status: error.response?.status, code: error.code } : error)
      sessionError.value = authError(error, '会话验证失败，请重试。')
    }
  } finally {
    if (!disposed) sessionLoading.value = false
  }
}

async function onAuthenticated(result: AuthResult) {
  try {
    scheduleSessionExpiry(result.user_token)
  } catch (error) {
    console.error('认证服务返回了无效凭证', error)
    toast.error('认证服务返回了无效凭证，请重新登录')
    return
  }
  try {
    sessionStorage.setItem(tokenStorageKey, result.user_token)
  } catch (error) {
    console.error('保存会话失败', error)
    toast.error('无法保存浏览器会话，刷新后需要重新登录')
  }
  userStore.setSession(result.name, result.user_id, result.user_token)
  uploadQueue.start(result.user_id, result.user_token)
  showAuth.value = false
  await nextTick()
  if (!disposed) await startCamera()
}

async function removeAccount() {
  if (!userStore.token || deleting.value) return
  deleting.value = true
  try {
    await deleteUser(userStore.token)
    toast.success('账户和人脸特征已删除')
    clearSession()
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 401) {
      toast.error('登录已过期，请重新登录')
      clearSession()
    } else {
      console.error('删除账户失败', axios.isAxiosError(error) ? { status: error.response?.status, code: error.code } : error)
      toast.error(axios.isAxiosError(error) && error.response?.status === 503
        ? '删除失败，请检查后端状态及是否存在关联上传记录。'
        : authError(error, '删除账户失败，请稍后重试。'))
    }
  } finally {
    deleting.value = false
  }
}

function capture() {
  const canvas = canvasRef.value
  if (!cameraReady.value || !canvas || !videoRef.value?.videoWidth || !userStore.userId || !userStore.token) {
    toast.error('摄像头尚未准备好，请稍后再试')
    return
  }
  try {
    const image: CapturedImage = {
      id: crypto.randomUUID(),
      userId: userStore.userId,
      src: canvas.toDataURL('image/jpeg', 0.85),
      expression: selectedExpression.value.name,
      createdAt: Date.now(),
      uploadStatus: 'queued',
    }
    imageStore.addImage(image)
    uploadQueue.pump()
    captureNotice.value = `已拍摄「${image.expression}」表情，后台上传中`
  } catch (error) {
    console.error('保存照片失败', error)
    toast.error('照片保存失败，请检查浏览器存储和摄像头')
    return
  }
  clearTimeout(noticeTimer)
  noticeTimer = setTimeout(() => { captureNotice.value = '' }, 2500)
}

function removeImage(index: number) {
  try {
    imageStore.removeImage(index)
  } catch (error) {
    console.error('删除照片失败', error)
    toast.error('删除失败，请检查浏览器存储设置')
  }
}

function retryUpload(image: CapturedImage) {
  if (!image.id || image.userId !== userStore.userId || image.uploadStatus !== 'failed') return
  try {
    imageStore.updateImage(image.id, { uploadStatus: 'queued', uploadMessage: undefined, uploadId: undefined })
    uploadQueue.pump()
  } catch (error) {
    console.error('重新排队失败', error)
    toast.error('无法保存上传状态，请检查浏览器存储')
  }
}

function uploadLabel(image: CapturedImage) {
  if (!image.uploadStatus) return '仅本地'
  return {
    queued: '等待上传',
    uploading: '上传中',
    uploaded: '已上传',
    failed: '上传失败',
    unknown: '结果待核对',
  }[image.uploadStatus]
}

function selectExpression(expression: typeof expressions[number], event: MouseEvent) {
  selectedExpression.value = expression
  if (event.detail > 0 && event.currentTarget instanceof HTMLElement) event.currentTarget.blur()
}

function handleKeydown(event: KeyboardEvent) {
  if (event.code !== 'Space' || event.repeat || showAuth.value || !userStore.token) return
  const target = event.target
  if (target instanceof HTMLElement && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT|BUTTON)$/.test(target.tagName))) return
  event.preventDefault()
  capture()
}

onMounted(async () => {
  window.addEventListener('keydown', handleKeydown)
  await restoreSession()
})

onUnmounted(() => {
  disposed = true
  window.removeEventListener('keydown', handleKeydown)
  clearTimeout(noticeTimer)
  clearTimeout(sessionTimer)
  uploadQueue.pause()
  stopCamera()
})
</script>

<template>
  <div class="min-h-screen bg-[#f7f8fa] text-slate-800">
    <header class="border-b border-slate-200/80 bg-white">
      <div class="mx-auto flex h-18 max-w-[1600px] items-center justify-between px-5 sm:px-8">
        <div class="flex items-center gap-3">
          <div class="flex size-10 items-center justify-center rounded-xl bg-indigo-600 text-white shadow-sm shadow-indigo-200">
            <ScanFace :size="23" />
          </div>
          <div>
            <div class="text-[15px] font-bold tracking-tight text-slate-900">表情采集工作台</div>
            <div class="mt-0.5 text-[10px] font-semibold tracking-[0.16em] text-slate-400">EXPRESSION STUDIO</div>
          </div>
        </div>
        <div v-if="userStore.token" class="flex flex-wrap items-center gap-2 rounded-full border border-slate-200 bg-slate-50 px-3 py-2 text-xs font-medium text-slate-600">
          <UserRound :size="15" class="text-slate-400" />
          <span>{{ userStore.name }}</span>
          <button type="button" :disabled="deleting" class="text-indigo-600 hover:underline disabled:opacity-50" @click="clearSession">退出登录</button>
          <button v-if="!confirmDelete" type="button" class="text-red-600 hover:underline" @click="confirmDelete = true">删除账户</button>
          <template v-else>
            <span>确定删除账户及人脸特征？本地照片不会删除。</span>
            <button type="button" :disabled="deleting" class="text-red-700 hover:underline disabled:opacity-50" @click="removeAccount">{{ deleting ? '删除中…' : '确认删除' }}</button>
            <button type="button" :disabled="deleting" class="hover:underline" @click="confirmDelete = false">取消</button>
          </template>
        </div>
      </div>
    </header>

    <div v-if="sessionLoading" class="p-8 text-center text-sm text-slate-500">正在验证登录状态…</div>
    <div v-else-if="sessionError" class="p-8 text-center">
      <p role="alert" class="text-sm text-red-600">{{ sessionError }}</p>
      <button type="button" class="mt-4 rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white" @click="restoreSession">重试验证</button>
      <button type="button" class="ml-3 text-sm text-indigo-600" @click="sessionError = ''; clearSession()">清除会话并重新登录</button>
    </div>
    <main v-else-if="userStore.token" class="mx-auto max-w-[1600px] px-1 py-2 sm:px-4 sm:py-5">
      <div class="grid items-start gap-5 lg:grid-cols-[minmax(210px,250px)_minmax(0,1fr)_minmax(230px,285px)]">
        <aside class="rounded-2xl border border-slate-200/80 bg-white p-5 shadow-sm shadow-slate-200/40">
          <div class="flex items-center gap-2">
            <div class="flex size-8 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600"><Sparkles :size="16" /></div>
            <h2 class="text-sm font-bold text-slate-900">选择表情</h2>
          </div>
          <p class="mt-3 text-xs leading-5 text-slate-400">点击选择一种表情，准备好后开始拍摄。</p>
          <div class="mt-5 grid grid-cols-2 gap-2 lg:grid-cols-1" role="group" aria-label="选择拍摄表情">
            <button v-for="expression in expressions" :key="expression.name" type="button"
              :aria-pressed="selectedExpression.name === expression.name"
              class="flex items-center gap-3 rounded-xl border px-3 py-3 text-left transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-500"
              :class="selectedExpression.name === expression.name ? 'border-indigo-300 bg-indigo-50/80 text-indigo-700' : 'border-transparent bg-slate-50 text-slate-600 hover:border-slate-200 hover:bg-slate-100'"
              @click="selectExpression(expression, $event)">
              <span class="text-2xl" aria-hidden="true">{{ expression.emoji }}</span>
              <span class="min-w-0 flex-1"><span class="block text-sm font-semibold">{{ expression.name }}</span><span class="mt-0.5 hidden text-[11px] text-slate-400 sm:block">{{ expression.description }}</span></span>
              <Check v-if="selectedExpression.name === expression.name" :size="16" class="shrink-0" />
              <span v-else-if="capturedExpressions.has(expression.name)" class="size-1.5 rounded-full bg-emerald-400" title="已拍摄"></span>
            </button>
          </div>
          <div class="mt-5 rounded-xl border border-slate-100 bg-slate-50 p-3.5">
            <div class="flex items-center gap-2 text-xs font-semibold text-slate-600"><Keyboard :size="15" /> 快捷操作</div>
            <div class="mt-2 flex items-center justify-between gap-2 text-xs text-slate-400"><span>拍摄当前画面</span><kbd class="rounded border border-slate-200 bg-white px-2 py-1 font-sans text-[10px] text-slate-500">空格键</kbd></div>
          </div>
        </aside>

        <section class="min-w-0 rounded-2xl border border-slate-200/80 bg-white p-4 shadow-sm shadow-slate-200/40 sm:p-5">
          <div class="mb-4 flex items-center justify-between gap-3">
            <div class="flex items-center gap-2"><Video :size="17" class="text-indigo-500" /><h2 class="text-sm font-bold text-slate-900">实时预览</h2></div>
            <div class="hidden items-center gap-2 text-xs text-slate-400 sm:flex">
          <span class="size-2 rounded-full" :class="cameraReady ? 'bg-emerald-400' : 'bg-slate-300'"></span>
          {{ cameraReady ? '摄像头已连接' : '摄像头未连接' }}
        </div>
            <span class="rounded-full bg-slate-100 px-3 py-1.5 text-xs font-medium text-slate-500">当前表情 <span class="ml-1 text-indigo-600">{{ selectedExpression.name }}</span></span>
          </div>
          <div class="relative mx-auto flex aspect-square max-h-[660px] items-center justify-center overflow-hidden rounded-xl bg-[#171c2b]">
            <video ref="videoRef" autoplay playsinline muted class="hidden" aria-hidden="true"></video>
            <canvas ref="canvasRef" width="640" height="640" class="h-full w-full object-contain" aria-label="摄像头实时画面"></canvas>
            <div v-if="!cameraReady" class="absolute inset-0 flex flex-col items-center justify-center px-6 text-center text-white">
              <div class="mb-4 flex size-16 items-center justify-center rounded-2xl bg-white/10"><Camera :size="28" class="text-white/70" /></div>
              <p class="text-sm font-medium">{{ cameraError || (cameraLoading ? '正在启动摄像头…' : '准备开启摄像头') }}</p>
              <p class="mt-2 text-xs leading-5 text-slate-400">请允许浏览器访问你的摄像头</p>
              <button v-if="cameraError" type="button" class="mt-4 rounded-lg bg-white/15 px-4 py-2 text-xs font-semibold hover:bg-white/25" @click="startCamera">重新连接</button>
            </div>
            <div v-if="captureNotice" role="status" class="absolute left-4 right-4 top-4 flex items-center justify-center gap-2 rounded-lg bg-emerald-500/95 px-3 py-2.5 text-sm font-medium text-white shadow-lg">
              <Check :size="16" /> {{ captureNotice }}
            </div>
            <div v-if="cameraReady" class="absolute bottom-4 left-4 flex items-center gap-2 rounded-full bg-slate-900/65 px-3 py-1.5 text-xs text-white/90">
              <span class="size-1.5 rounded-full bg-emerald-400"></span> 实时画面
            </div>
          </div>
          <div class="mt-4 flex flex-wrap items-center justify-between gap-3">
            <div class="flex items-center gap-2 text-xs text-slate-500"><CircleHelp :size="15" class="shrink-0 text-slate-400" /><span>{{ detectionHint }}</span></div>
            <button type="button" :disabled="!cameraReady" class="flex items-center gap-2 rounded-xl bg-indigo-600 px-5 py-3 text-sm font-semibold text-white shadow-sm shadow-indigo-200 transition hover:bg-indigo-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-500 disabled:cursor-not-allowed disabled:opacity-50" @click="capture">
              <Camera :size="17" /> 拍摄照片 <span class="ml-1 hidden border-l border-white/30 pl-3 text-xs font-normal text-indigo-100 sm:inline">空格</span>
            </button>
          </div>
        </section>

        <aside class="flex max-h-212 min-h-90 flex-col rounded-2xl border border-slate-200/80 bg-white p-5 shadow-sm shadow-slate-200/40">
          <div class="flex items-center justify-between gap-2">
            <div><h2 class="text-sm font-bold text-slate-900">已拍摄照片</h2><p class="mt-1 text-xs text-slate-400">你的表情记录</p></div>
            <span class="rounded-full bg-indigo-50 px-2.5 py-1 text-xs font-bold text-indigo-600">{{ photos.length }} 张</span>
          </div>
          <div class="mt-5 flex flex-wrap gap-1.5">
            <span v-for="expression in expressions" :key="expression.name" class="rounded-md px-2 py-1 text-[10px] font-medium" :class="capturedExpressions.has(expression.name) ? 'bg-indigo-50 text-indigo-600' : 'bg-slate-50 text-slate-400'">{{ expression.name }} {{ photos.filter(({ image }) => image.expression === expression.name).length }}</span>
          </div>
          <div v-if="uploadQueueBlocked" role="alert" class="mt-4 rounded-lg bg-red-50 p-3 text-xs text-red-700">
            后台上传已暂停，请检查浏览器存储后恢复。
            <button type="button" class="ml-1 font-semibold underline" @click="uploadQueue.resume()">恢复上传</button>
          </div>
          <div v-if="photos.length" class="mt-5 flex flex-wrap content-start gap-3 overflow-y-auto">
            <div v-for="{ image, index } in photos" :key="index" class="w-[calc((100%-0.75rem)/2)] group relative overflow-hidden rounded-xl border border-slate-100 bg-slate-50">
              <img :src="image.src" :alt="`${image.expression}表情照片`" class="aspect-square w-full object-cover" />
              <div class="flex items-center justify-between px-2 pt-2"><span class="text-xs font-medium text-slate-600">{{ image.expression }}</span><span class="text-[10px] text-slate-400">#{{ index + 1 }}</span></div>
              <div class="px-2 pb-2 pt-1 text-[10px]" :class="image.uploadStatus === 'failed' || image.uploadStatus === 'unknown' ? 'text-red-600' : 'text-slate-500'">
                <span>{{ uploadLabel(image) }}</span>
                <p v-if="image.uploadMessage" class="break-words">{{ image.uploadMessage }}<template v-if="image.uploadId">（记录 #{{ image.uploadId }}）</template></p>
                <button v-if="image.uploadStatus === 'failed' && image.userId === userStore.userId" type="button" class="mt-1 text-indigo-600 underline" @click="retryUpload(image)">重试</button>
              </div>
              <button type="button" :disabled="image.uploadStatus === 'uploading'" :aria-label="`删除第 ${index + 1} 张${image.expression}本地照片`" class="absolute right-2 top-2 flex size-8 items-center justify-center rounded-lg bg-slate-900/70 text-white opacity-100 transition hover:bg-red-600 focus:opacity-100 focus-visible:outline-2 focus-visible:outline-white disabled:cursor-not-allowed disabled:opacity-40 sm:opacity-0 sm:group-hover:opacity-100" @click="removeImage(index)"><Trash2 :size="15" /></button>
            </div>
          </div>
          <div v-else class="flex flex-1 flex-col items-center justify-center py-10 text-center">
            <div class="flex size-14 items-center justify-center rounded-2xl bg-slate-50 text-slate-300"><Camera :size="24" /></div>
            <p class="mt-4 text-sm font-medium text-slate-500">还没有照片</p>
            <p class="mt-1 text-xs leading-5 text-slate-400">选择表情，按下空格键<br />拍下第一张照片</p>
            <ArrowDown :size="15" class="mt-3 text-slate-300" />
          </div>
          <p class="mt-4 border-t border-slate-100 pt-4 text-[11px] leading-5 text-slate-400">照片保存在当前浏览器并后台上传。删除本地照片不会删除 OSS 中的图片；清除浏览器数据会丢失本地照片及上传状态。</p>
        </aside>
      </div>
    </main>

    <FaceAuth v-if="showAuth" :initial-name="userStore.name" @authenticated="onAuthenticated" />
  </div>
</template>
