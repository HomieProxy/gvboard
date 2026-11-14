package model

// Setting 业务设置（v2_settings，Gvboard 新增）。value 为 JSON 编码的值。
type Setting struct {
	ID        int64  `gorm:"column:id;primaryKey" json:"id"`
	Name      string `gorm:"column:name" json:"name"`
	Value     string `gorm:"column:value" json:"value"`
	CreatedAt int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Setting) TableName() string { return "v2_settings" }

// 插件类型
const (
	PluginTypeGo = "go"
	PluginTypeJS = "js"
)

// Plugin 插件（v2_plugin，Gvboard 新增）。
type Plugin struct {
	ID        int64  `gorm:"column:id;primaryKey" json:"id"`
	Code      string `gorm:"column:code" json:"code"`
	Type      string `gorm:"column:type" json:"type"`
	Version   string `gorm:"column:version" json:"version"`
	Enabled   int    `gorm:"column:enabled" json:"enabled"`
	Config    JSON   `gorm:"column:config" json:"config"`
	CreatedAt int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Plugin) TableName() string { return "v2_plugin" }

// PluginKV 插件键值存储（v2_plugin_kv，Gvboard 新增）。
type PluginKV struct {
	ID         int64  `gorm:"column:id;primaryKey" json:"id"`
	PluginCode string `gorm:"column:plugin_code" json:"plugin_code"`
	K          string `gorm:"column:k" json:"k"`
	V          string `gorm:"column:v" json:"v"`
	ExpireAt   *int64 `gorm:"column:expire_at" json:"expire_at"`
	CreatedAt  int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt  int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (PluginKV) TableName() string { return "v2_plugin_kv" }

// PluginPackage JS 插件安装包（v2_plugin_package，Gvboard 新增）。Archive 为 zip 文件的 base64。
type PluginPackage struct {
	ID         int64  `gorm:"column:id;primaryKey" json:"id"`
	PluginCode string `gorm:"column:plugin_code" json:"plugin_code"`
	SHA256     string `gorm:"column:sha256" json:"sha256"`
	Size       int64  `gorm:"column:size" json:"size"`
	Manifest   string `gorm:"column:manifest" json:"manifest"`
	Archive    string `gorm:"column:archive" json:"-"`
	CreatedAt  int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt  int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (PluginPackage) TableName() string { return "v2_plugin_package" }
