// 首页 3D 点阵地球（原生 WebGL，不依赖任何库）。
// 点阵与经纬网的顶点固定在显存中，由着色器完成旋转投影；光束、信标、轨道等少量动态图形
// 在 CPU 上投影后合并成一批四边形，每帧只上传一次。
import { isLand } from './land'

export interface GlobeStats {
  /** 当前正对屏幕的经纬度（度） */
  lon: number
  lat: number
  /** 正在传输的光束数量 */
  links: number
}

export interface GlobeOptions {
  /** 系统开启“减少动态效果”时不自动旋转、不发射光束，只在拖动时重绘 */
  reducedMotion?: boolean
  onStats?: (s: GlobeStats) => void
}

type Vec = [number, number, number]

const DEG = Math.PI / 180
const CAMERA = 4.2
const TILT_MIN = -0.25
const TILT_MAX = 0.75
/** 球体半径占画布短边的比例；四周留出卫星轨道与光束的空间 */
const RADIUS = 0.25
/** 画布最大边长（设备像素），避免高分屏上像素量过大 */
const MAX_SIDE = 1800
/** 最短帧间隔：高刷新率屏幕上不超过约 75 帧 */
const FRAME_MS = 13
const MAX_QUADS = 12000
/** 动态四边形每个顶点：x, y, u, v, r, g, b, a, 类型 */
const VF = 9

// 光束端点（主要网络枢纽城市）
const HUBS: [number, number][] = [
  [114.2, 22.3], [139.7, 35.7], [103.8, 1.35], [127, 37.5], [121.5, 25], [121.5, 31.2], [-118.2, 34], [-121.9, 37.3],
  [-74, 40.7], [-79.4, 43.7], [-0.1, 51.5], [8.7, 50.1], [4.9, 52.4], [151.2, -33.9], [72.9, 19], [55.3, 25.2],
  [-46.6, -23.5], [37.6, 55.8], [28, -26.2], [100.5, 13.7],
]

function toVec(lon: number, lat: number, r = 1): Vec {
  const c = Math.cos(lat * DEG)
  return [r * c * Math.sin(lon * DEG), r * Math.sin(lat * DEG), r * c * Math.cos(lon * DEG)]
}

function slerp(a: Vec, b: Vec, t: number): Vec {
  const d = Math.min(1, Math.max(-1, a[0] * b[0] + a[1] * b[1] + a[2] * b[2]))
  const w = Math.acos(d)
  if (w < 1e-6) return a
  const s = Math.sin(w)
  const ka = Math.sin((1 - t) * w) / s
  const kb = Math.sin(t * w) / s
  return [a[0] * ka + b[0] * kb, a[1] * ka + b[1] * kb, a[2] * ka + b[2] * kb]
}

/** 球面上某点的切平面基向量，用于在地表画圆 */
function tangent(v: Vec): [Vec, Vec] {
  const up: Vec = Math.abs(v[1]) > 0.9 ? [1, 0, 0] : [0, 1, 0]
  const e1: Vec = [up[1] * v[2] - up[2] * v[1], up[2] * v[0] - up[0] * v[2], up[0] * v[1] - up[1] * v[0]]
  const l = Math.hypot(e1[0], e1[1], e1[2])
  e1[0] /= l
  e1[1] /= l
  e1[2] /= l
  const e2: Vec = [v[1] * e1[2] - v[2] * e1[1], v[2] * e1[0] - v[0] * e1[2], v[0] * e1[1] - v[1] * e1[0]]
  return [e1, e2]
}

// ---------- 着色器 ----------

const PROJECT = `
uniform vec4 uRot;
uniform vec3 uView;
uniform vec2 uRes;
vec4 project(vec3 p) {
  float x1 = p.x * uRot.y + p.z * uRot.x;
  float z1 = -p.x * uRot.x + p.z * uRot.y;
  float y2 = p.y * uRot.w - z1 * uRot.z;
  float z2 = p.y * uRot.z + z1 * uRot.w;
  float s = ${CAMERA.toFixed(1)} / (${CAMERA.toFixed(1)} - z2);
  return vec4(uView.xy + vec2(x1, -y2) * uView.z * s, z2, s);
}
vec4 toClip(vec2 px) {
  return vec4(px.x / uRes.x * 2.0 - 1.0, 1.0 - px.y / uRes.y * 2.0, 0.0, 1.0);
}
`
const FS_PRECISION = `
#ifdef GL_FRAGMENT_PRECISION_HIGH
precision highp float;
#else
precision mediump float;
#endif
`

const DOT_VS = `${PROJECT}
attribute vec3 aPos;
attribute vec2 aInfo;
uniform vec4 uDot;
varying float vAlpha;
varying float vLit;
void main() {
  vec4 p = project(aPos);
  if (p.z <= 0.02) {
    gl_Position = vec4(2.0, 2.0, 0.0, 1.0);
    gl_PointSize = 0.0;
    vAlpha = 0.0;
    vLit = 0.0;
    return;
  }
  float d = abs(aInfo.x - uDot.y);
  if (d > 180.0) d = 360.0 - d;
  float lit = d < 7.0 ? 1.0 - d / 7.0 : 0.0;
  float tw = uDot.w * 0.18 * sin(uDot.x * 1.6 + aInfo.y * 40.0);
  vAlpha = min(1.0, (0.3 + 0.7 * p.z) * (0.85 + tw) + lit * 0.9 * p.z);
  vLit = lit;
  gl_PointSize = uDot.z * (0.75 + 0.5 * p.z) * (1.0 + lit * 0.6);
  gl_Position = toClip(p.xy);
}`
const DOT_FS = `${FS_PRECISION}
varying float vAlpha;
varying float vLit;
void main() {
  float d = length(gl_PointCoord - 0.5) * 2.0;
  float a = vAlpha * (1.0 - smoothstep(0.55, 1.0, d));
  vec3 c = mix(vec3(0.49, 0.83, 0.99), vec3(0.88, 0.98, 1.0), vLit);
  gl_FragColor = vec4(c * a, a);
}`

const WIRE_VS = `${PROJECT}
attribute vec3 aPos;
varying float vZ;
void main() {
  vec4 p = project(aPos);
  vZ = p.z;
  gl_Position = toClip(p.xy);
}`
const WIRE_FS = `${FS_PRECISION}
varying float vZ;
void main() {
  if (vZ < 0.0) discard;
  float a = 0.09;
  gl_FragColor = vec4(vec3(0.404, 0.91, 0.976) * a, a);
}`

const BATCH_VS = `
uniform vec2 uRes;
attribute vec2 aPx;
attribute vec2 aUv;
attribute vec4 aColor;
attribute float aKind;
varying vec2 vUv;
varying vec4 vColor;
varying float vKind;
void main() {
  vUv = aUv;
  vColor = aColor;
  vKind = aKind;
  gl_Position = vec4(aPx.x / uRes.x * 2.0 - 1.0, 1.0 - aPx.y / uRes.y * 2.0, 0.0, 1.0);
}`
// 类型 0：线段（u 为半宽，v 为到中心线的距离，边缘 1 像素抗锯齿）；1：光晕；2：实心方块
const BATCH_FS = `${FS_PRECISION}
varying vec2 vUv;
varying vec4 vColor;
varying float vKind;
void main() {
  float a = 1.0;
  if (vKind < 0.5) {
    a = clamp(vUv.x + 0.5 - abs(vUv.y), 0.0, 1.0);
  } else if (vKind < 1.5) {
    float r = length(vUv);
    a = r < 0.25 ? mix(1.0, 0.55, r / 0.25) : 0.55 * max(0.0, 1.0 - (r - 0.25) / 0.75);
  }
  a *= vColor.a;
  gl_FragColor = vec4(vColor.rgb * a, a);
}`

// 全画布四边形：模式 0 绘制大气光晕与球体，模式 1 绘制边缘高光
const QUAD_VS = `
attribute vec2 aPos;
void main() { gl_Position = vec4(aPos, 0.0, 1.0); }`
const QUAD_FS = `${FS_PRECISION}
uniform vec3 uView;
uniform vec2 uRes;
uniform float uMode;
uniform float uDpr;
vec4 pm(vec3 c, float a) { return vec4(c * a, a); }
vec4 over(vec4 top, vec4 bottom) { return top + bottom * (1.0 - top.a); }
void main() {
  vec2 p = vec2(gl_FragCoord.x, uRes.y - gl_FragCoord.y);
  float R = uView.z;
  float d = length(p - uView.xy);
  if (uMode < 0.5) {
    float t = clamp((d - 0.92 * R) / (0.53 * R), 0.0, 1.0);
    vec4 h = t < 0.35
      ? mix(vec4(0.133, 0.827, 0.933, 0.28), vec4(0.231, 0.51, 0.965, 0.12), t / 0.35)
      : mix(vec4(0.231, 0.51, 0.965, 0.12), vec4(0.231, 0.51, 0.965, 0.0), (t - 0.35) / 0.65);
    vec4 col = pm(h.rgb, h.a);
    float cov = clamp(R - d + 0.5, 0.0, 1.0);
    if (cov > 0.0) {
      vec2 L = uView.xy + vec2(-0.35, -0.4) * R;
      float bt = clamp((length(p - L) - 0.1 * R) / (1.3 * R), 0.0, 1.0);
      vec4 b = bt < 0.5
        ? mix(vec4(0.09, 0.165, 0.369, 0.96), vec4(0.035, 0.063, 0.149, 0.98), bt / 0.5)
        : mix(vec4(0.035, 0.063, 0.149, 0.98), vec4(0.016, 0.031, 0.078, 1.0), (bt - 0.5) / 0.5);
      col = over(pm(b.rgb, b.a * cov), col);
    }
    float ring = clamp(0.6 * uDpr + 0.5 - abs(d - R), 0.0, 1.0) * 0.35;
    gl_FragColor = over(pm(vec3(0.404, 0.91, 0.976), ring), col);
  } else {
    float t = clamp((d - 0.82 * R) / (0.2 * R), 0.0, 1.0);
    gl_FragColor = pm(vec3(0.133, 0.827, 0.933), t * 0.22 * clamp(1.02 * R - d + 0.5, 0.0, 1.0));
  }
}`

interface Program {
  prog: WebGLProgram
  u: Record<string, WebGLUniformLocation | null>
}

function compile(gl: WebGLRenderingContext, vs: string, fs: string, attribs: string[], uniforms: string[]): Program {
  const prog = gl.createProgram()!
  for (const [src, type] of [[vs, gl.VERTEX_SHADER], [fs, gl.FRAGMENT_SHADER]] as const) {
    const sh = gl.createShader(type)!
    gl.shaderSource(sh, src)
    gl.compileShader(sh)
    if (!gl.getShaderParameter(sh, gl.COMPILE_STATUS) && !gl.isContextLost()) throw new Error(gl.getShaderInfoLog(sh) || 'shader')
    gl.attachShader(prog, sh)
  }
  attribs.forEach((name, i) => gl.bindAttribLocation(prog, i, name))
  gl.linkProgram(prog)
  if (!gl.getProgramParameter(prog, gl.LINK_STATUS) && !gl.isContextLost()) throw new Error(gl.getProgramInfoLog(prog) || 'link')
  const u: Program['u'] = {}
  for (const name of uniforms) u[name] = gl.getUniformLocation(prog, name)
  return { prog, u }
}

interface Arc {
  pts: Float32Array
  to: Vec
  t: number
  speed: number
  arrived: boolean
}

interface Ripple {
  at: Vec
  e1: Vec
  e2: Vec
  t: number
}

export function createGlobe(canvas: HTMLCanvasElement, opts: GlobeOptions = {}) {
  const noop = { destroy() {} }
  const reduced = !!opts.reducedMotion
  const gl = canvas.getContext('webgl', {
    alpha: true,
    antialias: false,
    depth: false,
    stencil: false,
    premultipliedAlpha: true,
    powerPreference: 'low-power',
  })
  if (!gl) return noop

  // 陆地点阵：按纬度分行、每行按周长均匀取点，保证球面上分布均匀
  const step = 1.55
  const dotData: number[] = []
  for (let lat = -84; lat <= 84; lat += step) {
    const n = Math.max(1, Math.round((360 * Math.cos(lat * DEG)) / step))
    for (let i = 0; i < n; i++) {
      const lon = -180 + (i + (Math.abs(lat / step) % 2) * 0.5) * (360 / n)
      if (!isLand(lon, lat)) continue
      dotData.push(...toVec(lon, lat), lon, Math.random())
    }
  }
  const dotCount = dotData.length / 5
  const dotArray = new Float32Array(dotData)

  // 经纬网（线段对）
  const wire: number[] = []
  for (let lat = -60; lat <= 60; lat += 30) {
    for (let lon = -180; lon < 180; lon += 4) wire.push(...toVec(lon, lat), ...toVec(lon + 4, lat))
  }
  for (let lon = -180; lon < 180; lon += 30) {
    for (let lat = -88; lat < 88; lat += 4) wire.push(...toVec(lon, lat), ...toVec(lon, lat + 4))
  }
  const wireArray = new Float32Array(wire)
  const wireCount = wire.length / 3

  const hubs = HUBS.map(([lon, lat]) => toVec(lon, lat))
  const hubFrames = hubs.map(tangent)

  const batch = new Float32Array(MAX_QUADS * 4 * VF)
  const indices = new Uint16Array(MAX_QUADS * 6)
  for (let i = 0; i < MAX_QUADS; i++) {
    const v = i * 4
    indices.set([v, v + 1, v + 2, v + 2, v + 1, v + 3], i * 6)
  }

  let dotProg: Program
  let wireProg: Program
  let batchProg: Program
  let quadProg: Program
  let dotBuf: WebGLBuffer
  let wireBuf: WebGLBuffer
  let batchBuf: WebGLBuffer
  let quadBuf: WebGLBuffer
  let indexBuf: WebGLBuffer

  function init() {
    const g = gl!
    dotProg = compile(g, DOT_VS, DOT_FS, ['aPos', 'aInfo'], ['uRot', 'uView', 'uRes', 'uDot'])
    wireProg = compile(g, WIRE_VS, WIRE_FS, ['aPos'], ['uRot', 'uView', 'uRes'])
    batchProg = compile(g, BATCH_VS, BATCH_FS, ['aPx', 'aUv', 'aColor', 'aKind'], ['uRes'])
    quadProg = compile(g, QUAD_VS, QUAD_FS, ['aPos'], ['uView', 'uRes', 'uMode', 'uDpr'])
    dotBuf = g.createBuffer()!
    g.bindBuffer(g.ARRAY_BUFFER, dotBuf)
    g.bufferData(g.ARRAY_BUFFER, dotArray, g.STATIC_DRAW)
    wireBuf = g.createBuffer()!
    g.bindBuffer(g.ARRAY_BUFFER, wireBuf)
    g.bufferData(g.ARRAY_BUFFER, wireArray, g.STATIC_DRAW)
    quadBuf = g.createBuffer()!
    g.bindBuffer(g.ARRAY_BUFFER, quadBuf)
    g.bufferData(g.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), g.STATIC_DRAW)
    batchBuf = g.createBuffer()!
    g.bindBuffer(g.ARRAY_BUFFER, batchBuf)
    g.bufferData(g.ARRAY_BUFFER, batch.byteLength, g.DYNAMIC_DRAW)
    indexBuf = g.createBuffer()!
    g.bindBuffer(g.ELEMENT_ARRAY_BUFFER, indexBuf)
    g.bufferData(g.ELEMENT_ARRAY_BUFFER, indices, g.STATIC_DRAW)
    g.disable(g.DEPTH_TEST)
    g.enable(g.BLEND)
    g.blendFunc(g.ONE, g.ONE_MINUS_SRC_ALPHA)
    g.clearColor(0, 0, 0, 0)
  }

  /** 绑定顶点缓冲并设置属性：每项为 [位置编号, 分量数, 偏移]，单位为 float */
  function attribs(buf: WebGLBuffer, stride: number, layout: readonly (readonly [number, number, number])[]) {
    const g = gl!
    g.bindBuffer(g.ARRAY_BUFFER, buf)
    for (let i = 0; i < 4; i++) g.disableVertexAttribArray(i)
    for (const [loc, size, off] of layout) {
      g.enableVertexAttribArray(loc)
      g.vertexAttribPointer(loc, size, g.FLOAT, false, stride * 4, off * 4)
    }
  }
  const DOT_LAYOUT = [[0, 3, 0], [1, 2, 3]] as const
  const POS3_LAYOUT = [[0, 3, 0]] as const
  const POS2_LAYOUT = [[0, 2, 0]] as const
  const BATCH_LAYOUT = [[0, 2, 0], [1, 2, 2], [2, 4, 4], [3, 1, 8]] as const

  let w = 1
  let h = 1
  let dpr = 1
  let cx = 0
  let cy = 0
  let R = 0
  let rotY = -105 * DEG
  let rotX = 0.32
  let velY = 0
  let velX = 0
  let dragging = false
  let lastX = 0
  let lastY = 0
  let arcs: Arc[] = []
  let ripples: Ripple[] = []
  let spawnIn = 0.2
  let time = 0
  let lastDraw = 0
  let raf = 0
  let visible = true
  let running = false
  let lost = false
  let statsIn = 0

  let sy = 0
  let cyR = 1
  let sx = 0
  let cxR = 1
  function setRotation() {
    sy = Math.sin(rotY)
    cyR = Math.cos(rotY)
    sx = Math.sin(rotX)
    cxR = Math.cos(rotX)
  }

  // 世界坐标 → 画布像素；结果写入 out：[x, y, 深度 z（朝向观察者为正）, 缩放]
  const out: [number, number, number, number] = [0, 0, 0, 1]
  function project(x: number, y: number, z: number) {
    const x1 = x * cyR + z * sy
    const z1 = -x * sy + z * cyR
    const y2 = y * cxR - z1 * sx
    const z2 = y * sx + z1 * cxR
    const s = CAMERA / (CAMERA - z2)
    out[0] = cx + x1 * R * s
    out[1] = cy - y2 * R * s
    out[2] = z2
    out[3] = s
  }

  // 被球体挡住：在球体背面且投影落在球面圆盘内
  function hidden(px: number, py: number, z: number) {
    if (z >= 0) return false
    const dx = px - cx
    const dy = py - cy
    return dx * dx + dy * dy < R * R * 1.02
  }

  // ---------- 动态图形批次 ----------
  let quads = 0
  function put(o: number, x: number, y: number, u: number, v: number, r: number, g: number, b: number, a: number, k: number) {
    batch[o] = x
    batch[o + 1] = y
    batch[o + 2] = u
    batch[o + 3] = v
    batch[o + 4] = r
    batch[o + 5] = g
    batch[o + 6] = b
    batch[o + 7] = a
    batch[o + 8] = k
    return o + VF
  }
  function seg(x0: number, y0: number, x1: number, y1: number, width: number, r: number, g: number, b: number, a: number) {
    if (quads >= MAX_QUADS || a <= 0.003) return
    const dx = x1 - x0
    const dy = y1 - y0
    const len = Math.hypot(dx, dy)
    if (len < 1e-3) return
    const hw = width / 2 + 1
    const nx = (-dy / len) * hw
    const ny = (dx / len) * hw
    const hu = width / 2
    let o = quads * 4 * VF
    o = put(o, x0 + nx, y0 + ny, hu, hw, r, g, b, a, 0)
    o = put(o, x0 - nx, y0 - ny, hu, -hw, r, g, b, a, 0)
    o = put(o, x1 + nx, y1 + ny, hu, hw, r, g, b, a, 0)
    put(o, x1 - nx, y1 - ny, hu, -hw, r, g, b, a, 0)
    quads++
  }
  function box(x: number, y: number, size: number, kind: number, r: number, g: number, b: number, a: number) {
    if (quads >= MAX_QUADS || a <= 0.003) return
    const s = size / 2
    let o = quads * 4 * VF
    o = put(o, x - s, y - s, -1, -1, r, g, b, a, kind)
    o = put(o, x + s, y - s, 1, -1, r, g, b, a, kind)
    o = put(o, x - s, y + s, -1, 1, r, g, b, a, kind)
    put(o, x + s, y + s, 1, 1, r, g, b, a, kind)
    quads++
  }
  const glow = (x: number, y: number, size: number, r: number, g: number, b: number, a: number) => box(x, y, size, 1, r, g, b, a)
  const dot = (x: number, y: number, size: number, r: number, g: number, b: number, a: number) => box(x, y, size, 2, r, g, b, a)

  // 折线：不可见的点断开线条
  let penOn = false
  let penX = 0
  let penY = 0
  let penW = 1
  let penR = 0
  let penG = 0
  let penB = 0
  let penA = 1
  function pathStart(width: number, r: number, g: number, b: number, a: number) {
    penOn = false
    penW = width
    penR = r
    penG = g
    penB = b
    penA = a
  }
  function pathTo(x: number, y: number, show: boolean) {
    if (!show) {
      penOn = false
      return
    }
    if (penOn) seg(penX, penY, x, y, penW, penR, penG, penB, penA)
    penX = x
    penY = y
    penOn = true
  }

  function spawnArc() {
    for (let tries = 0; tries < 8; tries++) {
      const a = hubs[(Math.random() * hubs.length) | 0]
      const b = hubs[(Math.random() * hubs.length) | 0]
      const d = Math.acos(Math.min(1, a[0] * b[0] + a[1] * b[1] + a[2] * b[2]))
      if (d < 0.25 || d > 2.2) continue
      const lift = 0.04 + d * 0.09
      const pts = new Float32Array(49 * 3)
      for (let i = 0; i <= 48; i++) {
        const t = i / 48
        const p = slerp(a, b, t)
        const k = 1 + lift * Math.sin(Math.PI * t)
        pts[i * 3] = p[0] * k
        pts[i * 3 + 1] = p[1] * k
        pts[i * 3 + 2] = p[2] * k
      }
      arcs.push({ pts, to: b, t: 0, speed: 0.55 / (0.6 + d), arrived: false })
      return
    }
  }

  function buildScanLine(scan: number) {
    for (let pass = 0; pass < 2; pass++) {
      if (pass === 0) pathStart(7 * dpr, 0.133, 0.827, 0.933, 0.1)
      else pathStart(1.5 * dpr, 0.647, 0.953, 0.988, 0.5)
      for (let lat = -88; lat <= 88; lat += 3) {
        const c = Math.cos(lat * DEG) * 1.005
        project(c * Math.sin(scan * DEG), Math.sin(lat * DEG) * 1.005, c * Math.cos(scan * DEG))
        pathTo(out[0], out[1], out[2] >= 0)
      }
    }
  }

  function buildArcs(dt: number) {
    const tail = 0.32
    let keep = 0
    for (const a of arcs) {
      a.t += dt * a.speed
      if (a.t >= 1 && !a.arrived) {
        a.arrived = true
        const [e1, e2] = tangent(a.to)
        ripples.push({ at: a.to, e1, e2, t: 0 })
      }
      if (a.t > 1 + tail + 0.4) continue
      arcs[keep++] = a
      const n = a.pts.length / 3 - 1
      const fade = a.t > 1 + tail ? 1 - (a.t - 1 - tail) / 0.4 : 1
      // 整条航线的淡色轨迹
      pathStart(1 * dpr, 0.22, 0.741, 0.973, 0.18 * fade)
      const upto = Math.min(n, Math.ceil(Math.min(a.t, 1) * n))
      for (let i = 0; i <= upto; i++) {
        project(a.pts[i * 3], a.pts[i * 3 + 1], a.pts[i * 3 + 2])
        pathTo(out[0], out[1], !hidden(out[0], out[1], out[2]))
      }
      // 彗星：头部最亮、向尾部渐隐
      const head = Math.min(a.t, 1)
      const from = Math.max(0, a.t - tail)
      if (from >= head) continue
      const i0 = Math.floor(from * n)
      const i1 = Math.ceil(head * n)
      let px = 0
      let py = 0
      let ok = false
      for (let i = i0; i <= i1; i++) {
        project(a.pts[i * 3], a.pts[i * 3 + 1], a.pts[i * 3 + 2])
        const x = out[0]
        const y = out[1]
        const vis = !hidden(x, y, out[2])
        if (ok && vis) {
          const k = (i - i0) / Math.max(1, i1 - i0)
          seg(px, py, x, y, (0.8 + 1.6 * k) * dpr, (56 + 168 * k) / 255, (189 + 53 * k) / 255, (248 + 6 * k) / 255, k * fade)
        }
        px = x
        py = y
        ok = vis
      }
      if (a.t < 1 && ok) glow(px, py, 30 * dpr, 0.878, 0.949, 0.996, fade)
    }
    arcs.length = keep
  }

  /** 在球面某点的切平面上画圆，投影后自然呈现透视 */
  function surfaceCircle(v: Vec, e1: Vec, e2: Vec, rad: number) {
    for (let i = 0; i <= 28; i++) {
      const a = (i / 28) * Math.PI * 2
      const c = Math.cos(a) * rad
      const s = Math.sin(a) * rad
      project(v[0] + e1[0] * c + e2[0] * s, v[1] + e1[1] * c + e2[1] * s, v[2] + e1[2] * c + e2[2] * s)
      pathTo(out[0], out[1], out[2] >= 0)
    }
  }

  function buildRipples(dt: number) {
    let keep = 0
    for (const r of ripples) {
      r.t += dt
      if (r.t > 1.4) continue
      ripples[keep++] = r
      const k = r.t / 1.4
      pathStart(1.4 * dpr, 0.659, 0.333, 0.969, (1 - k) * 0.9)
      surfaceCircle(r.at, r.e1, r.e2, 0.02 + k * 0.13)
      if (k < 0.5) {
        project(r.at[0], r.at[1], r.at[2])
        if (out[2] > 0) glow(out[0], out[1], 46 * dpr * (1 - k), 0.659, 0.333, 0.969, 1 - k * 2)
      }
    }
    ripples.length = keep
  }

  function buildHubs() {
    for (let i = 0; i < hubs.length; i++) {
      const v = hubs[i]
      project(v[0], v[1], v[2])
      const z = out[2]
      if (z <= 0.05) continue
      const bx = out[0]
      const by = out[1]
      // 信标沿屏幕径向伸出：正对观察者时缩成一点，靠近边缘时最长
      const dx = bx - cx
      const dy = by - cy
      const dl = Math.hypot(dx, dy) || 1
      const len = R * 0.07 * Math.sqrt(Math.max(0, 1 - z * z)) + 4 * dpr
      const tx = bx + (dx / dl) * len
      const ty = by + (dy / dl) * len
      seg(bx, by, tx, ty, 1.2 * dpr, 0.404, 0.91, 0.976, (0.35 + 0.65 * z) * 0.85)
      const pulse = reduced ? 0.5 : (time * 0.8 + i * 0.37) % 1
      const [e1, e2] = hubFrames[i]
      pathStart(1.1 * dpr, 0.133, 0.827, 0.933, (1 - pulse) * z * 0.9)
      surfaceCircle(v, e1, e2, 0.012 + pulse * 0.055)
      glow(tx, ty, 22 * dpr, 0.133, 0.827, 0.933, 0.6 + 0.4 * z)
      dot(tx, ty, 2.4 * dpr, 0.925, 0.996, 1, 1)
    }
  }

  function buildOrbit(radius: number, incl: number, node: number, speed: number, r: number, g: number, b: number) {
    const ci = Math.cos(incl)
    const si = Math.sin(incl)
    const cn = Math.cos(node)
    const sn = Math.sin(node)
    // 先绕 x 轴倾斜，再绕 y 轴转到升交点方向
    const at = (a: number) => {
      const x = Math.cos(a) * radius
      const z = Math.sin(a) * radius
      const z1 = z * ci
      project(x * cn + z1 * sn, -z * si, -x * sn + z1 * cn)
    }
    const n = 300
    for (let i = 0; i < n; i++) {
      at((i / n) * Math.PI * 2)
      if (hidden(out[0], out[1], out[2])) continue
      dot(out[0], out[1], 1.3 * dpr, 0.22, 0.741, 0.973, 0.4)
    }
    for (let k = 0; k < 2; k++) {
      at(time * speed + k * Math.PI)
      if (hidden(out[0], out[1], out[2])) continue
      glow(out[0], out[1], 26 * dpr * out[3], r, g, b, 0.95)
      dot(out[0], out[1], 2 * dpr, 0.878, 0.949, 0.996, 1)
    }
  }

  function draw(dt = 0) {
    if (lost) return
    const g = gl!
    setRotation()
    const scan = ((time * 28) % 360) - 180

    quads = 0
    if (!reduced) buildScanLine(scan)
    buildArcs(dt)
    buildRipples(dt)
    buildHubs()
    const mainEnd = quads
    buildOrbit(1.38, 1.15, 0.6, 0.35, 0.659, 0.333, 0.969)
    buildOrbit(1.62, -0.42, -0.9, -0.22, 0.133, 0.827, 0.933)

    g.viewport(0, 0, w, h)
    g.clear(g.COLOR_BUFFER_BIT)

    // 大气光晕与球体
    g.useProgram(quadProg.prog)
    g.uniform3f(quadProg.u.uView, cx, cy, R)
    g.uniform2f(quadProg.u.uRes, w, h)
    g.uniform1f(quadProg.u.uDpr, dpr)
    g.uniform1f(quadProg.u.uMode, 0)
    attribs(quadBuf, 2, POS2_LAYOUT)
    g.drawArrays(g.TRIANGLE_STRIP, 0, 4)

    // 经纬网
    g.useProgram(wireProg.prog)
    g.uniform4f(wireProg.u.uRot, sy, cyR, sx, cxR)
    g.uniform3f(wireProg.u.uView, cx, cy, R)
    g.uniform2f(wireProg.u.uRes, w, h)
    attribs(wireBuf, 3, POS3_LAYOUT)
    g.drawArrays(g.LINES, 0, wireCount)

    // 陆地点阵
    g.useProgram(dotProg.prog)
    g.uniform4f(dotProg.u.uRot, sy, cyR, sx, cxR)
    g.uniform3f(dotProg.u.uView, cx, cy, R)
    g.uniform2f(dotProg.u.uRes, w, h)
    const base = Math.max(1.4, 2 * dpr * (R / (260 * dpr)) ** 0.35)
    g.uniform4f(dotProg.u.uDot, time % 1000, scan, base, reduced ? 0 : 1)
    attribs(dotBuf, 5, DOT_LAYOUT)
    g.drawArrays(g.POINTS, 0, dotCount)

    // 扫描线、光束、波纹、信标
    g.useProgram(batchProg.prog)
    g.uniform2f(batchProg.u.uRes, w, h)
    g.bindBuffer(g.ARRAY_BUFFER, batchBuf)
    g.bufferSubData(g.ARRAY_BUFFER, 0, batch.subarray(0, quads * 4 * VF))
    attribs(batchBuf, VF, BATCH_LAYOUT)
    g.bindBuffer(g.ELEMENT_ARRAY_BUFFER, indexBuf)
    if (mainEnd) g.drawElements(g.TRIANGLES, mainEnd * 6, g.UNSIGNED_SHORT, 0)

    // 边缘高光
    g.useProgram(quadProg.prog)
    g.uniform1f(quadProg.u.uMode, 1)
    attribs(quadBuf, 2, POS2_LAYOUT)
    g.drawArrays(g.TRIANGLE_STRIP, 0, 4)

    // 卫星轨道（在球体前方）
    if (quads > mainEnd) {
      g.useProgram(batchProg.prog)
      attribs(batchBuf, VF, BATCH_LAYOUT)
      g.drawElements(g.TRIANGLES, (quads - mainEnd) * 6, g.UNSIGNED_SHORT, mainEnd * 12)
    }
  }

  function resize() {
    const rect = canvas.getBoundingClientRect()
    const side = Math.max(1, Math.min(rect.width, rect.height))
    dpr = Math.min(window.devicePixelRatio || 1, 2, MAX_SIDE / side)
    w = Math.max(1, Math.round(rect.width * dpr))
    h = Math.max(1, Math.round(rect.height * dpr))
    canvas.width = w
    canvas.height = h
    cx = w / 2
    cy = h / 2
    R = Math.min(w, h) * RADIUS
    // 改变画布尺寸会清空画面，立即重画一帧
    draw()
  }

  function frame(now: number) {
    raf = 0
    if (lastDraw && now - lastDraw < FRAME_MS) {
      schedule()
      return
    }
    const dt = lastDraw ? Math.min(0.05, (now - lastDraw) / 1000) : 0.016
    lastDraw = now
    time += dt
    if (!dragging) {
      rotY += velY + 0.0011 * (dt * 60)
      rotX += velX
      velY *= 0.94
      velX *= 0.94
      rotX += (0.32 - rotX) * 0.01
    }
    rotX = Math.min(TILT_MAX, Math.max(TILT_MIN, rotX))
    spawnIn -= dt
    if (spawnIn <= 0 && arcs.length < 10) {
      spawnArc()
      spawnIn = 0.35 + Math.random() * 0.5
    }
    draw(dt)
    statsIn -= dt
    if (opts.onStats && statsIn <= 0) {
      statsIn = 0.2
      let lon = (-rotY / DEG) % 360
      if (lon > 180) lon -= 360
      if (lon < -180) lon += 360
      let links = 0
      for (const a of arcs) if (a.t < 1) links++
      opts.onStats({ lon, lat: rotX / DEG, links })
    }
    schedule()
  }

  function schedule() {
    if (running && visible && !lost && !document.hidden && !raf) raf = requestAnimationFrame(frame)
  }
  function stop() {
    if (raf) cancelAnimationFrame(raf)
    raf = 0
    lastDraw = 0
  }

  function inGlobe(e: PointerEvent) {
    const rect = canvas.getBoundingClientRect()
    const x = (e.clientX - rect.left) * dpr
    const y = (e.clientY - rect.top) * dpr
    return (x - cx) ** 2 + (y - cy) ** 2 <= (R * 1.15) ** 2
  }
  function onDown(e: PointerEvent) {
    if (!inGlobe(e)) return
    dragging = true
    lastX = e.clientX
    lastY = e.clientY
    velX = velY = 0
    canvas.setPointerCapture(e.pointerId)
    canvas.style.cursor = 'grabbing'
  }
  function onMove(e: PointerEvent) {
    if (!dragging) {
      canvas.style.cursor = inGlobe(e) ? 'grab' : ''
      return
    }
    const dx = e.clientX - lastX
    const dy = e.clientY - lastY
    lastX = e.clientX
    lastY = e.clientY
    velY = dx * 0.0045
    velX = dy * 0.0035
    rotY += velY
    rotX = Math.min(TILT_MAX, Math.max(TILT_MIN, rotX + velX))
    if (!running) draw()
  }
  function onUp(e: PointerEvent) {
    if (!dragging) return
    dragging = false
    canvas.style.cursor = 'grab'
    if (canvas.hasPointerCapture(e.pointerId)) canvas.releasePointerCapture(e.pointerId)
    if (!running) velX = velY = 0
  }
  function onVisibility() {
    if (document.hidden) stop()
    else schedule()
  }
  function onLost(e: Event) {
    e.preventDefault()
    lost = true
    stop()
  }
  function onRestored() {
    lost = false
    init()
    resize()
    schedule()
  }

  try {
    init()
  } catch {
    return noop
  }

  const ro = new ResizeObserver(resize)
  ro.observe(canvas)
  const io = new IntersectionObserver((entries) => {
    visible = entries.some((e) => e.isIntersecting)
    if (visible) schedule()
    else stop()
  })
  io.observe(canvas)
  canvas.addEventListener('pointerdown', onDown)
  canvas.addEventListener('pointermove', onMove)
  canvas.addEventListener('pointerup', onUp)
  canvas.addEventListener('pointercancel', onUp)
  canvas.addEventListener('webglcontextlost', onLost)
  canvas.addEventListener('webglcontextrestored', onRestored)
  document.addEventListener('visibilitychange', onVisibility)

  if (reduced) {
    // 静态画面：预先放几条完整的光束
    for (let i = 0; i < 6; i++) spawnArc()
    for (const a of arcs) a.t = 0.999
    opts.onStats?.({ lon: 105, lat: rotX / DEG, links: arcs.length })
  } else {
    running = true
  }
  resize()
  schedule()

  return {
    destroy() {
      running = false
      stop()
      ro.disconnect()
      io.disconnect()
      canvas.removeEventListener('pointerdown', onDown)
      canvas.removeEventListener('pointermove', onMove)
      canvas.removeEventListener('pointerup', onUp)
      canvas.removeEventListener('pointercancel', onUp)
      canvas.removeEventListener('webglcontextlost', onLost)
      canvas.removeEventListener('webglcontextrestored', onRestored)
      document.removeEventListener('visibilitychange', onVisibility)
      // 主动释放 WebGL 上下文，避免多次进出首页后浏览器积累上下文
      gl.getExtension('WEBGL_lose_context')?.loseContext()
    },
  }
}
