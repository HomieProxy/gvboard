// 去特征检查（需求文档第 7.4 节）：扫描前端构建产物，发现原项目特征字样时以非零状态退出。
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('../..', import.meta.url))
const dirs = ['internal/web/admin', 'internal/theme/builtin/default']

const rules = [
  { name: 'v2board 字样', re: /v2board/i },
  { name: 'umi 框架标识', re: /\bumi\b|__umi|umijs/i },
  { name: '原项目主题资源路径', re: /\/theme\/default\/assets/ },
  { name: '原项目管理端资源路径', re: /\/assets\/admin/ },
  { name: '原项目注入变量', re: /window\.settings\b/ },
  { name: '原项目公开配置接口', re: /guest\/comm\/config/ },
]

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else out.push(p)
  }
  return out
}

let failed = 0
for (const d of dirs) {
  const abs = join(root, d)
  for (const file of walk(abs)) {
    if (!/\.(html|js|css|json|svg|txt|map)$/.test(file)) continue
    const text = readFileSync(file, 'utf8')
    for (const r of rules) {
      const m = r.re.exec(text)
      if (m) {
        failed++
        const at = Math.max(0, m.index - 40)
        console.log(`[${r.name}] ${relative(root, file)}: …${text.slice(at, m.index + 40).replace(/\s+/g, ' ')}…`)
      }
    }
  }
}
if (failed) {
  console.log(`发现 ${failed} 处特征字样`)
  process.exit(1)
}
console.log('去特征检查通过')
