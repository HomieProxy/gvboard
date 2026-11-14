// 节点类型、选项与传输协议配置参考（与原版管理端节点表单一致）。

export const serverTypes = [
  { label: 'Shadowsocks', value: 'shadowsocks' },
  { label: 'VMess', value: 'vmess' },
  { label: 'Trojan', value: 'trojan' },
  { label: 'TUIC', value: 'tuic' },
  { label: 'Hysteria', value: 'hysteria' },
  { label: 'VLESS', value: 'vless' },
  { label: 'AnyTLS', value: 'anytls' },
  { label: 'v2node', value: 'v2node' },
]
export const typeLabel = (t: string) => serverTypes.find((s) => s.value === t)?.label || t

const o = (...pairs: [string | number | null, string][]) => pairs.map(([value, label]) => ({ value, label }))

export const yesNo = o([0, '否'], [1, '是'])

export const ssCiphers = o(
  ['aes-128-gcm', 'aes-128-gcm'],
  ['aes-192-gcm', 'aes-192-gcm'],
  ['aes-256-gcm', 'aes-256-gcm'],
  ['chacha20-ietf-poly1305', 'chacha20-ietf-poly1305'],
  ['2022-blake3-aes-128-gcm', '2022-blake3-aes-128-gcm'],
  ['2022-blake3-aes-256-gcm', '2022-blake3-aes-256-gcm'],
)

export const v2nodeProtocols = o(
  ['anytls', 'AnyTLS'],
  ['hysteria2', 'Hysteria2'],
  ['shadowsocks', 'Shadowsocks'],
  ['trojan', 'Trojan'],
  ['tuic', 'Tuic'],
  ['vless', 'VLess'],
  ['vmess', 'VMess'],
)

export const vmessNetworks = o(['tcp', 'TCP'], ['ws', 'WebSocket'], ['grpc', 'gRPC'], ['kcp', 'mKCP'], ['httpupgrade', 'HTTPUpgrade'], ['xhttp', 'XHTTP'])
export const trojanNetworks = o(['tcp', 'TCP'], ['ws', 'WebSocket'], ['grpc', 'gRPC'])
export const udpRelayModes = o(['native', 'native'], ['quic', 'quic'])
export const congestionControls = o(['cubic', 'cubic'], ['new_reno', 'new_reno'], ['bbr', 'bbr'])
export const vlessEncryptions = o([null, '无'], ['mlkem768x25519plus', 'MLKEM768X25519PLUS'])
export const flows = o([null, '无'], ['xtls-rprx-vision', 'xtls-rprx-vision'])

/** v2node 各协议可选的安全性 */
export function v2nodeTlsOptions(protocol: string) {
  const out: { value: number; label: string }[] = []
  if (protocol === 'vless' || protocol === 'vmess') out.push({ value: 0, label: '无' })
  out.push({ value: 1, label: 'TLS' })
  if (protocol === 'vless' || protocol === 'anytls') out.push({ value: 2, label: 'Reality' })
  return out
}

/** v2node 各协议可选的传输协议；hysteria2、tuic 不需要 */
export function v2nodeNetworks(protocol: string) {
  if (protocol === 'shadowsocks') return o(['tcp', 'TCP'], ['http', 'HTTP伪装'])
  if (protocol === 'hysteria2' || protocol === 'tuic') return []
  const base = o(['tcp', 'TCP'], ['ws', 'WebSocket'], ['grpc', 'gRPC'])
  return protocol === 'trojan' ? base : [...base, ...o(['httpupgrade', 'HTTPUpgrade'], ['xhttp', 'XHTTP'])]
}

const j = (v: unknown) => JSON.stringify(v, null, 4)

/** 传输协议详细配置参考（编辑框占位提示） */
export const networkExamples: Record<string, string> = {
  tcp: j({ header: { type: 'http', request: { path: ['/'], headers: { Host: ['www.baidu.com', 'www.bing.com'] } }, response: {} } }),
  ws: j({ path: '/', headers: { Host: 'xtls.github.io' } }),
  grpc: j({ serviceName: 'GunService' }),
  kcp: j({ header: { type: 'none' }, seed: '' }),
  httpupgrade: j({ path: '/', host: 'xtls.github.io' }),
  xhttp: j({ path: '/', host: 'xtls.github.io', mode: 'auto', extra: {} }),
}
/** v2node 的参考额外带 acceptProxyProtocol，且支持 HTTP伪装 */
export const v2nodeNetworkExamples: Record<string, string> = {
  tcp: j({ acceptProxyProtocol: false, header: { type: 'http', request: { path: ['/'], headers: { Host: ['www.baidu.com', 'www.bing.com'] } }, response: {} } }),
  http: j({ acceptProxyProtocol: false, path: '/', Host: 'xtls.github.io' }),
  ws: j({ acceptProxyProtocol: false, path: '/', headers: { Host: 'xtls.github.io' } }),
  grpc: j({ serviceName: 'GunService' }),
  httpupgrade: j({ acceptProxyProtocol: false, path: '/', host: 'xtls.github.io' }),
  xhttp: j({ path: '/', host: 'xtls.github.io', mode: 'auto', extra: {} }),
}

export const defaultPaddingScheme = j([
  'stop=8',
  '0=30-30',
  '1=100-400',
  '2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000',
  '3=9-9,500-1000',
  '4=500-1000',
  '5=500-1000',
  '6=500-1000',
  '7=500-1000',
])
