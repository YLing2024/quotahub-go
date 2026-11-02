import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'node:path'

// 产物输出到 web/dist, 由根包 //go:embed all:web/dist 打进二进制 (后端同源托管)。
// 开发态 /api 代理到本机后端, 默认 127.0.0.1:19250, 可用 VITE_API_PROXY_TARGET 覆盖。
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  server: {
    proxy: {
      '/api': process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:19250',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks: {
          react: ['react', 'react-dom'],
          // 不要给 codemirror 单独命名 chunk：一旦列进 manualChunks，Rollup 会把它提升成
          // 入口的静态依赖，CodeEditor 的 React.lazy 就白做了（首屏照样多拉 520 KB）。
          // 交给 Rollup 自动切分后，它只会跟着 import('./CodeEditor') 出现在异步 chunk 里。
        },
      },
    },
  },
})
