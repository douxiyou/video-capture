import vue from "@vitejs/plugin-vue";
import tailwindcss from "@tailwindcss/vite";
import { viteStaticCopy } from "vite-plugin-static-copy";
import { defineConfig } from "vite";

// https://vite.dev/config/
export default defineConfig({
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
      },
    },
  },
  plugins: [
    vue(),
    tailwindcss(),
    viteStaticCopy({
      targets: [
        {
          // 1. 动态追踪并捕获 node_modules 里的官方 Wasm 引擎文件
          src: "node_modules/@mediapipe/tasks-vision/wasm/vision_wasm_internal*",
          rename: { stripBase: 4 },
          // 2. 将 WASM 文件放到 dist/mediapipe/ 下，与运行时资源路径一致
          dest: "mediapipe",
        },
        {
          src: "node_modules/@ricky0123/vad-web/dist/vad.worklet.bundle.min.js",
          dest: "vad",
          rename: { stripBase: 4 },
        },
        {
          src: "node_modules/@ricky0123/vad-web/dist/silero_vad_v6.onnx",
          dest: "vad",
          rename: { stripBase: 4 },
        },
        {
          src: "node_modules/onnxruntime-web/dist/*.wasm",
          dest: "onnxruntime",
          rename: { stripBase: 3 },
        },
        {
          src: "node_modules/onnxruntime-web/dist/*.mjs",
          dest: "onnxruntime",
          rename: { stripBase: 3 },
        },
      ],
    }),
  ],
  // css: {
  //   preprocessorOptions: {
  //     scss: {
  //       additionalData: `@use "@/style/mixin.scss" as *;`,
  //     },
  //   },
  // },
});
