package model

// 节点类型标识，与原项目及节点后端使用的 node_type 一致。
const (
	ServerTypeShadowsocks = "shadowsocks"
	ServerTypeVmess       = "vmess"
	ServerTypeVless       = "vless"
	ServerTypeTrojan      = "trojan"
	ServerTypeTuic        = "tuic"
	ServerTypeHysteria    = "hysteria"
	ServerTypeAnytls      = "anytls"
	ServerTypeV2node      = "v2node"
)

// ServerGroup 权限组（v2_server_group）。
type ServerGroup struct {
	ID        int64  `gorm:"column:id;primaryKey" json:"id"`
	Name      string `gorm:"column:name" json:"name"`
	CreatedAt int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (ServerGroup) TableName() string { return "v2_server_group" }

// ServerRoute 路由规则（v2_server_route）。
type ServerRoute struct {
	ID          int64   `gorm:"column:id;primaryKey" json:"id"`
	Remarks     string  `gorm:"column:remarks" json:"remarks"`
	Match       string  `gorm:"column:match" json:"match"`
	Action      string  `gorm:"column:action" json:"action"`
	ActionValue *string `gorm:"column:action_value" json:"action_value"`
	CreatedAt   int64   `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt   int64   `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (ServerRoute) TableName() string { return "v2_server_route" }

// ServerBase 各类节点共有字段。port 为字符串，可能是端口段（如 "10000-20000"）。
type ServerBase struct {
	ID         int64   `gorm:"column:id;primaryKey" json:"id"`
	GroupID    IntList `gorm:"column:group_id" json:"group_id"`
	RouteID    IntList `gorm:"column:route_id" json:"route_id"`
	Name       string  `gorm:"column:name" json:"name"`
	ParentID   *int64  `gorm:"column:parent_id" json:"parent_id"`
	Host       string  `gorm:"column:host" json:"host"`
	Port       string  `gorm:"column:port" json:"port"`
	ServerPort int64   `gorm:"column:server_port" json:"server_port"`
	Tags       StrList `gorm:"column:tags" json:"tags"`
	Rate       string  `gorm:"column:rate" json:"rate"`
	Show       int     `gorm:"column:show" json:"show"`
	Sort       *int64  `gorm:"column:sort" json:"sort"`
	CreatedAt  int64   `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt  int64   `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

// Common 返回共有字段，供按类型统一处理各类节点。
func (b *ServerBase) Common() *ServerBase { return b }

// ServerShadowsocks（v2_server_shadowsocks）。
type ServerShadowsocks struct {
	ServerBase
	Cipher       string  `gorm:"column:cipher" json:"cipher"`
	Obfs         *string `gorm:"column:obfs" json:"obfs"`
	ObfsSettings JSON    `gorm:"column:obfs_settings" json:"obfs_settings"`
}

func (ServerShadowsocks) TableName() string { return "v2_server_shadowsocks" }

// ServerVmess（v2_server_vmess）。注意部分列名为驼峰（networkSettings 等），与原表一致。
type ServerVmess struct {
	ServerBase
	TLS             int     `gorm:"column:tls" json:"tls"`
	Network         string  `gorm:"column:network" json:"network"`
	Rules           *string `gorm:"column:rules" json:"rules"`
	NetworkSettings JSON    `gorm:"column:networkSettings" json:"networkSettings"`
	TLSSettings     JSON    `gorm:"column:tlsSettings" json:"tlsSettings"`
	RuleSettings    JSON    `gorm:"column:ruleSettings" json:"ruleSettings"`
	DNSSettings     JSON    `gorm:"column:dnsSettings" json:"dnsSettings"`
}

func (ServerVmess) TableName() string { return "v2_server_vmess" }

// ServerVless（v2_server_vless）。
type ServerVless struct {
	ServerBase
	TLS                int     `gorm:"column:tls" json:"tls"`
	TLSSettings        JSON    `gorm:"column:tls_settings" json:"tls_settings"`
	Flow               *string `gorm:"column:flow" json:"flow"`
	Network            string  `gorm:"column:network" json:"network"`
	NetworkSettings    JSON    `gorm:"column:network_settings" json:"network_settings"`
	Encryption         *string `gorm:"column:encryption" json:"encryption"`
	EncryptionSettings JSON    `gorm:"column:encryption_settings" json:"encryption_settings"`
}

func (ServerVless) TableName() string { return "v2_server_vless" }

// ServerTrojan（v2_server_trojan）。
type ServerTrojan struct {
	ServerBase
	Network         *string `gorm:"column:network" json:"network"`
	NetworkSettings JSON    `gorm:"column:network_settings" json:"network_settings"`
	AllowInsecure   int     `gorm:"column:allow_insecure" json:"allow_insecure"`
	ServerName      *string `gorm:"column:server_name" json:"server_name"`
}

func (ServerTrojan) TableName() string { return "v2_server_trojan" }

// ServerTuic（v2_server_tuic）。
type ServerTuic struct {
	ServerBase
	ServerName        *string `gorm:"column:server_name" json:"server_name"`
	Insecure          int     `gorm:"column:insecure" json:"insecure"`
	DisableSNI        int     `gorm:"column:disable_sni" json:"disable_sni"`
	UDPRelayMode      *string `gorm:"column:udp_relay_mode" json:"udp_relay_mode"`
	ZeroRTTHandshake  int     `gorm:"column:zero_rtt_handshake" json:"zero_rtt_handshake"`
	CongestionControl *string `gorm:"column:congestion_control" json:"congestion_control"`
}

func (ServerTuic) TableName() string { return "v2_server_tuic" }

// ServerHysteria（v2_server_hysteria）。version：1 或 2。
type ServerHysteria struct {
	ServerBase
	Version      int     `gorm:"column:version" json:"version"`
	UpMbps       int64   `gorm:"column:up_mbps" json:"up_mbps"`
	DownMbps     int64   `gorm:"column:down_mbps" json:"down_mbps"`
	Obfs         *string `gorm:"column:obfs" json:"obfs"`
	ObfsPassword *string `gorm:"column:obfs_password" json:"obfs_password"`
	ServerName   *string `gorm:"column:server_name" json:"server_name"`
	Insecure     int     `gorm:"column:insecure" json:"insecure"`
}

func (ServerHysteria) TableName() string { return "v2_server_hysteria" }

// ServerAnytls（v2_server_anytls）。
type ServerAnytls struct {
	ServerBase
	ServerName    *string `gorm:"column:server_name" json:"server_name"`
	Insecure      int     `gorm:"column:insecure" json:"insecure"`
	PaddingScheme JSON    `gorm:"column:padding_scheme" json:"padding_scheme"`
}

func (ServerAnytls) TableName() string { return "v2_server_anytls" }

// ServerV2node 多协议节点（v2_server_v2node）。
type ServerV2node struct {
	ServerBase
	ListenIP             string  `gorm:"column:listen_ip" json:"listen_ip"`
	Protocol             string  `gorm:"column:protocol" json:"protocol"`
	TLS                  int     `gorm:"column:tls" json:"tls"`
	TLSSettings          JSON    `gorm:"column:tls_settings" json:"tls_settings"`
	Flow                 *string `gorm:"column:flow" json:"flow"`
	Network              string  `gorm:"column:network" json:"network"`
	NetworkSettings      JSON    `gorm:"column:network_settings" json:"network_settings"`
	TrustedXForwardedFor JSON    `gorm:"column:trusted_x_forwarded_for" json:"trusted_x_forwarded_for"`
	Encryption           *string `gorm:"column:encryption" json:"encryption"`
	EncryptionSettings   JSON    `gorm:"column:encryption_settings" json:"encryption_settings"`
	DisableSNI           int     `gorm:"column:disable_sni" json:"disable_sni"`
	UDPRelayMode         *string `gorm:"column:udp_relay_mode" json:"udp_relay_mode"`
	ZeroRTTHandshake     int     `gorm:"column:zero_rtt_handshake" json:"zero_rtt_handshake"`
	CongestionControl    *string `gorm:"column:congestion_control" json:"congestion_control"`
	Cipher               *string `gorm:"column:cipher" json:"cipher"`
	UpMbps               int64   `gorm:"column:up_mbps" json:"up_mbps"`
	DownMbps             int64   `gorm:"column:down_mbps" json:"down_mbps"`
	Obfs                 *string `gorm:"column:obfs" json:"obfs"`
	ObfsPassword         *string `gorm:"column:obfs_password" json:"obfs_password"`
	PaddingScheme        JSON    `gorm:"column:padding_scheme" json:"padding_scheme"`
}

func (ServerV2node) TableName() string { return "v2_server_v2node" }
