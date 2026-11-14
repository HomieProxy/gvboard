// 各节点类型的专有字段定义（字段名与数据库列名一致，vmess 的对象字段为驼峰）。
// json 类型在表单中以 JSON 文本编辑，提交时解析为对象；raw 类型原样作为文本提交（如 padding_scheme）。

export type FieldKind = 'text' | 'number' | 'switch' | 'select' | 'json' | 'raw'

export interface FieldSpec {
  key: string
  label: string
  kind: FieldKind
  options?: { label: string; value: string | number }[]
  required?: boolean
  default?: unknown
  placeholder?: string
  help?: string
  /** 仅当返回 true 时显示（v2node 按协议显示字段） */
  when?: (f: Record<string, unknown>) => boolean
}

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

const opts = (...v: (string | number)[]) => v.map((x) => ({ label: String(x), value: x }))
const optional = (...v: string[]) => [{ label: '无', value: '' }, ...opts(...v)]

const ciphers = opts(
  'aes-128-gcm',
  'aes-192-gcm',
  'aes-256-gcm',
  'chacha20-ietf-poly1305',
  '2022-blake3-aes-128-gcm',
  '2022-blake3-aes-256-gcm',
)
const tls3 = [
  { label: '无', value: 0 },
  { label: 'TLS', value: 1 },
  { label: 'REALITY', value: 2 },
]
const networkJSONHelp = 'ws：{"path":"/","headers":{"Host":"example.com"}}；grpc：{"serviceName":"name"}'
const tlsJSONHelp = 'TLS：{"server_name":"example.com","allow_insecure":0}；REALITY：{"server_name":"目标域名","server_port":"443"}，密钥留空自动生成'

const v2 = (...protocols: string[]) => (f: Record<string, unknown>) => protocols.includes(String(f.protocol))

export const typeFields: Record<string, FieldSpec[]> = {
  shadowsocks: [
    { key: 'cipher', label: '加密方式', kind: 'select', options: ciphers, required: true, default: 'aes-128-gcm' },
    { key: 'obfs', label: '混淆', kind: 'select', options: optional('http'), default: '' },
    { key: 'obfs_settings', label: '混淆配置', kind: 'json', placeholder: '{"host":"","path":"/"}' },
  ],
  vmess: [
    { key: 'tls', label: 'TLS', kind: 'select', options: [{ label: '关闭', value: 0 }, { label: '开启', value: 1 }], required: true, default: 0 },
    {
      key: 'network',
      label: '传输协议',
      kind: 'select',
      options: opts('tcp', 'kcp', 'ws', 'http', 'domainsocket', 'quic', 'grpc', 'httpupgrade', 'xhttp'),
      required: true,
      default: 'tcp',
    },
    { key: 'networkSettings', label: '传输配置', kind: 'json', help: networkJSONHelp },
    { key: 'tlsSettings', label: 'TLS 配置', kind: 'json', placeholder: '{"serverName":"example.com","allowInsecure":0}' },
    { key: 'ruleSettings', label: '规则配置', kind: 'json' },
    { key: 'dnsSettings', label: 'DNS 配置', kind: 'json' },
  ],
  trojan: [
    { key: 'network', label: '传输协议', kind: 'select', options: opts('tcp', 'ws', 'grpc'), required: true, default: 'tcp' },
    { key: 'network_settings', label: '传输配置', kind: 'json', help: networkJSONHelp },
    { key: 'server_name', label: 'SNI', kind: 'text' },
    { key: 'allow_insecure', label: '允许不安全证书', kind: 'switch', default: 0 },
  ],
  tuic: [
    { key: 'server_name', label: 'SNI', kind: 'text' },
    { key: 'insecure', label: '允许不安全证书', kind: 'switch', default: 0 },
    { key: 'disable_sni', label: '禁用 SNI', kind: 'switch', default: 0 },
    { key: 'zero_rtt_handshake', label: '0-RTT 握手', kind: 'switch', default: 0 },
    { key: 'udp_relay_mode', label: 'UDP 转发模式', kind: 'select', options: optional('native', 'quic'), default: 'native' },
    { key: 'congestion_control', label: '拥塞控制', kind: 'select', options: optional('bbr', 'cubic', 'new_reno'), default: 'bbr' },
  ],
  hysteria: [
    { key: 'version', label: '版本', kind: 'select', options: [{ label: 'v1', value: 1 }, { label: 'v2', value: 2 }], required: true, default: 2 },
    { key: 'up_mbps', label: '上行带宽 (Mbps)', kind: 'number', default: 0 },
    { key: 'down_mbps', label: '下行带宽 (Mbps)', kind: 'number', default: 0 },
    { key: 'obfs', label: '混淆类型', kind: 'text', placeholder: 'v2 常用 salamander，留空关闭' },
    { key: 'obfs_password', label: '混淆密码', kind: 'text', placeholder: '开启混淆时留空自动生成' },
    { key: 'server_name', label: 'SNI', kind: 'text' },
    { key: 'insecure', label: '允许不安全证书', kind: 'switch', default: 0 },
  ],
  vless: [
    { key: 'tls', label: '安全', kind: 'select', options: tls3, required: true, default: 0 },
    { key: 'tls_settings', label: 'TLS / REALITY 配置', kind: 'json', help: tlsJSONHelp },
    { key: 'flow', label: '流控', kind: 'select', options: optional('xtls-rprx-vision'), default: '' },
    { key: 'network', label: '传输协议', kind: 'select', options: opts('tcp', 'ws', 'grpc', 'httpupgrade', 'xhttp'), required: true, default: 'tcp' },
    { key: 'network_settings', label: '传输配置', kind: 'json', help: networkJSONHelp },
    { key: 'encryption', label: '加密', kind: 'select', options: optional('none', 'mlkem768x25519plus'), default: '' },
    { key: 'encryption_settings', label: '加密配置', kind: 'json', placeholder: '{"mode":"native","rtt":"0rtt"}，密钥留空自动生成' },
  ],
  anytls: [
    { key: 'server_name', label: 'SNI', kind: 'text' },
    { key: 'insecure', label: '允许不安全证书', kind: 'switch', default: 0 },
    { key: 'padding_scheme', label: '填充方案（JSON 数组）', kind: 'raw', placeholder: '["stop=8","0=30-30",...]，留空使用默认' },
  ],
  v2node: [
    {
      key: 'protocol',
      label: '协议',
      kind: 'select',
      options: opts('shadowsocks', 'vmess', 'vless', 'trojan', 'tuic', 'hysteria2', 'anytls'),
      required: true,
      default: 'vless',
    },
    { key: 'listen_ip', label: '监听 IP', kind: 'text', placeholder: '0.0.0.0' },
    { key: 'tls', label: '安全', kind: 'select', options: tls3, required: true, default: 0, help: 'trojan、tuic、hysteria2 强制 TLS' },
    {
      key: 'tls_settings',
      label: 'TLS / REALITY 配置',
      kind: 'json',
      help: `${tlsJSONHelp}；可选 ech、cert_mode（remote 时自动生成自签名证书）`,
    },
    { key: 'network', label: '传输协议', kind: 'select', options: opts('tcp', 'ws', 'grpc', 'http', 'httpupgrade', 'xhttp'), required: true, default: 'tcp' },
    { key: 'network_settings', label: '传输配置', kind: 'json', help: networkJSONHelp },
    { key: 'flow', label: '流控', kind: 'select', options: optional('xtls-rprx-vision'), default: '', when: v2('vless') },
    { key: 'encryption', label: '加密', kind: 'select', options: optional('mlkem768x25519plus'), default: '', when: v2('vless') },
    { key: 'encryption_settings', label: '加密配置', kind: 'json', when: v2('vless') },
    { key: 'cipher', label: '加密方式', kind: 'select', options: ciphers, default: 'aes-128-gcm', when: v2('shadowsocks') },
    { key: 'disable_sni', label: '禁用 SNI', kind: 'switch', default: 0 },
    { key: 'zero_rtt_handshake', label: '0-RTT 握手', kind: 'switch', default: 0 },
    { key: 'udp_relay_mode', label: 'UDP 转发模式', kind: 'select', options: optional('native', 'quic'), default: '', when: v2('tuic') },
    { key: 'congestion_control', label: '拥塞控制', kind: 'select', options: optional('bbr', 'cubic', 'new_reno'), default: '', when: v2('tuic') },
    { key: 'up_mbps', label: '上行带宽 (Mbps)', kind: 'number', default: 0, when: v2('hysteria2') },
    { key: 'down_mbps', label: '下行带宽 (Mbps)', kind: 'number', default: 0, when: v2('hysteria2') },
    { key: 'obfs', label: '混淆类型', kind: 'text', placeholder: 'salamander，留空关闭', when: v2('hysteria2') },
    { key: 'obfs_password', label: '混淆密码', kind: 'text', when: v2('hysteria2') },
    { key: 'padding_scheme', label: '填充方案（JSON 数组）', kind: 'raw', when: v2('anytls') },
    { key: 'trusted_x_forwarded_for', label: '信任的转发头', kind: 'json', placeholder: '["X-Forwarded-For"]' },
  ],
}
