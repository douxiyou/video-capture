import type { NormalizedLandmark } from '@mediapipe/tasks-vision'

type Point = { x: number; y: number }

const reference: Point[] = [
  { x: 38.2946, y: 51.6963 },
  { x: 73.5318, y: 51.5014 },
  { x: 56.0252, y: 71.7366 },
  { x: 41.5493, y: 92.3655 },
  { x: 70.7299, y: 92.2041 },
]

export function alignFace(video: HTMLVideoElement, landmarks: NormalizedLandmark[]): string {
  function point(index: number): Point {
    const landmark = landmarks[index]
    if (!landmark || !Number.isFinite(landmark.x) || !Number.isFinite(landmark.y) ||
      landmark.x < 0 || landmark.x > 1 || landmark.y < 0 || landmark.y > 1) {
      throw new Error('请将完整面部保持在画面内')
    }
    return { x: landmark.x * video.videoWidth, y: landmark.y * video.videoHeight }
  }
  function middle(a: Point, b: Point): Point {
    return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 }
  }
  const eyes = [middle(point(33), point(133)), middle(point(362), point(263))].sort((a, b) => a.x - b.x)
  const mouth = [point(61), point(291)].sort((a, b) => a.x - b.x)
  const points = [eyes[0]!, eyes[1]!, point(1), mouth[0]!, mouth[1]!]
  if (Math.hypot(eyes[1]!.x - eyes[0]!.x, eyes[1]!.y - eyes[0]!.y) < 30) {
    throw new Error('请靠近摄像头，使面部更清晰')
  }
  const sourceMean = points.reduce((p, v) => ({ x: p.x + v.x / 5, y: p.y + v.y / 5 }), { x: 0, y: 0 })
  const targetMean = reference.reduce((p, v) => ({ x: p.x + v.x / 5, y: p.y + v.y / 5 }), { x: 0, y: 0 })
  let denominator = 0
  let a = 0
  let b = 0
  points.forEach((point, i) => {
    const x = point.x - sourceMean.x
    const y = point.y - sourceMean.y
    const target = reference[i]!
    const u = target.x - targetMean.x
    const v = target.y - targetMean.y
    denominator += x * x + y * y
    a += x * u + y * v
    b += x * v - y * u
  })
  if (denominator < 1) throw new Error('人脸关键点不可用，请调整姿势')
  a /= denominator
  b /= denominator
  const canvas = document.createElement('canvas')
  canvas.width = canvas.height = 112
  const context = canvas.getContext('2d')
  if (!context) throw new Error('无法创建人脸图像')
  context.setTransform(a, b, -b, a, targetMean.x - a * sourceMean.x + b * sourceMean.y, targetMean.y - b * sourceMean.x - a * sourceMean.y)
  context.drawImage(video, 0, 0)
  const data = canvas.toDataURL('image/jpeg', 0.9).split(',')[1]
  if (!data) throw new Error('无法生成验证图像')
  return data
}
