import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

// 本地开发：页面没有经过服务器渲染，由这里提供 config.js（GV_API、GV_BACKEND 环境变量）。
function devConfig(): Plugin {
  return {
    name: 'dev-config',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use('/config.js', (_req, res) => {
        res.setHeader('Content-Type', 'text/javascript')
        res.end(`window.APP_CONFIG=${JSON.stringify({ api: process.env.GV_API || '/api/dev' })}`)
      })
    },
  }
}

// 构建产物即内置默认主题（public/theme.json 会一并复制）。
export default defineConfig({
  base: './',
  plugins: [vue(), devConfig()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    proxy: { '/api': process.env.GV_BACKEND || 'http://127.0.0.1:8080' },
  },
  build: {
    outDir: '../../internal/theme/builtin/default',
    emptyOutDir: true,
    assetsDir: 'assets',
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('naive-ui') || id.includes('vueuc') || id.includes('css-render')) return 'ui'
          if (id.includes('node_modules')) return 'vendor'
        },
      },
    },
  },
})
