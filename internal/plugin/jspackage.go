package plugin

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dop251/goja"

	sdk "gvboard/pkg/plugin"
)

// JS 插件安装包限制。
const (
	maxPackageSize   = 2 << 20 // zip 文件
	maxPackageFiles  = 200
	maxFileSize      = 2 << 20 // 单个文件解压后
	maxUnpackedSize  = 8 << 20 // 全部文件解压后
	maxHTTPDomains   = 20
	manifestFile     = "plugin.json"
	defaultMainFile  = "main.js"
	maxManifestText  = 64
	maxManifestLines = 2000
)

// JS 插件权限（决策 D57）。
const (
	PermUserRead          = "user.read"          // 读取用户基本信息
	PermUserBalance       = "user.balance"       // 调整用户余额
	PermSubscriptionRead  = "subscription.read"  // 读取订阅
	PermSubscriptionWrite = "subscription.write" // 给订阅增加流量、延长到期时间
	PermOrderRead         = "order.read"         // 读取订单
)

// Permissions 全部权限及说明（管理端安装确认时显示）。
func Permissions() map[string]string {
	return map[string]string{
		PermUserRead:          "读取用户基本信息（邮箱、余额、佣金、封禁状态等，不含密码）",
		PermUserBalance:       "增加或扣除用户余额",
		PermSubscriptionRead:  "读取用户的订阅（套餐、流量、到期时间，不含订阅令牌与 UUID）",
		PermSubscriptionWrite: "给订阅增加流量、延长到期时间",
		PermOrderRead:         "读取用户的订单",
	}
}

// manifest plugin.json。
type manifest struct {
	Code        string      `json:"code"`
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	Author      string      `json:"author"`
	Description string      `json:"description"`
	URL         string      `json:"url"`
	Main        string      `json:"main"`
	HTTP        []string    `json:"http"`
	Permissions []string    `json:"permissions"`
	Config      []sdk.Field `json:"config"`
}

func (m *manifest) info() sdk.Info {
	return sdk.Info{Code: m.Code, Name: m.Name, Version: m.Version, Author: m.Author, Description: m.Description, URL: m.URL}
}

func (m *manifest) can(perm string) bool { return slices.Contains(m.Permissions, perm) }

// jsPackage 解析后的安装包。
type jsPackage struct {
	manifest manifest
	sha256   string
	size     int64
	raw      []byte
	files    map[string][]byte        // 路径 → 内容
	programs map[string]*goja.Program // 已编译的 .js 模块（可在多个运行时间共享）
	domains  []domain
}

// domain 允许访问的域名。
type domain struct {
	host     string // 小写；通配时为后缀（不含 "*."）
	wildcard bool
	port     string // 为空表示只允许 80 / 443
}

var (
	hostPattern    = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)
	versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,31}$`)
)

func parseDomain(s string) (domain, error) {
	var d domain
	s = strings.ToLower(strings.TrimSpace(s))
	host := s
	if h, p, ok := strings.Cut(s, ":"); ok {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != p {
			return d, fmt.Errorf("域名 %q 的端口无效", s)
		}
		host, d.port = h, p
	}
	if rest, ok := strings.CutPrefix(host, "*."); ok {
		host, d.wildcard = rest, true
	}
	if !hostPattern.MatchString(host) {
		return d, fmt.Errorf("域名 %q 无效（只能填写域名，可用 *. 开头表示子域名，不能填写 IP）", s)
	}
	d.host = host
	return d, nil
}

func (d domain) String() string {
	s := d.host
	if d.wildcard {
		s = "*." + s
	}
	if d.port != "" {
		s += ":" + d.port
	}
	return s
}

// match 主机名与端口（port 为空表示协议默认端口）是否允许。
func (d domain) match(host, port, scheme string) bool {
	if d.wildcard {
		if !strings.HasSuffix(host, "."+d.host) {
			return false
		}
	} else if host != d.host {
		return false
	}
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	if d.port == "" {
		return port == "80" || port == "443"
	}
	return port == d.port
}

// readPackage 解析并校验 zip 安装包。zip 中可以有一层顶级目录。
func readPackage(raw []byte) (*jsPackage, error) {
	if len(raw) == 0 {
		return nil, errors.New("安装包为空")
	}
	if len(raw) > maxPackageSize {
		return nil, fmt.Errorf("安装包不能超过 %d MB", maxPackageSize>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, errors.New("安装包不是有效的 zip 文件")
	}
	files := map[string][]byte{}
	var total int64
	count := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return nil, fmt.Errorf("安装包中不能包含链接等特殊文件：%s", f.Name)
		}
		name, err := cleanPath(f.Name)
		if err != nil {
			return nil, err
		}
		if count++; count > maxPackageFiles {
			return nil, fmt.Errorf("安装包中的文件不能超过 %d 个", maxPackageFiles)
		}
		if _, dup := files[name]; dup {
			return nil, fmt.Errorf("安装包中的文件重复：%s", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败：%w", name, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxFileSize+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败：%w", name, err)
		}
		if len(data) > maxFileSize {
			return nil, fmt.Errorf("文件 %s 超过 %d MB", name, maxFileSize>>20)
		}
		if total += int64(len(data)); total > maxUnpackedSize {
			return nil, fmt.Errorf("安装包解压后不能超过 %d MB", maxUnpackedSize>>20)
		}
		files[name] = data
	}
	files = stripTopDir(files)
	mf, ok := files[manifestFile]
	if !ok {
		return nil, errors.New("安装包中缺少 plugin.json")
	}
	var m manifest
	d := json.NewDecoder(bytes.NewReader(mf))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return nil, fmt.Errorf("plugin.json 格式错误：%v", err)
	}
	p := &jsPackage{manifest: m, raw: raw, size: int64(len(raw)), files: files, programs: map[string]*goja.Program{}}
	sum := sha256.Sum256(raw)
	p.sha256 = hex.EncodeToString(sum[:])
	if err := p.validate(); err != nil {
		return nil, err
	}
	for name, data := range files {
		if !strings.HasSuffix(name, ".js") {
			continue
		}
		if !utf8.Valid(data) {
			return nil, fmt.Errorf("%s 不是 UTF-8 编码", name)
		}
		prog, err := goja.Compile(name, "(function(exports, require, module, __filename) {"+string(data)+"\n})", false)
		if err != nil {
			return nil, fmt.Errorf("%s 语法错误：%v", name, err)
		}
		p.programs[name] = prog
	}
	return p, nil
}

func cleanPath(name string) (string, error) {
	if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("安装包中的路径无效：%s", name)
	}
	c := path.Clean(name)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") || c != name && c+"/" != name {
		return "", fmt.Errorf("安装包中的路径无效：%s", name)
	}
	return c, nil
}

// stripTopDir 全部文件都在同一个顶级目录下、根目录没有 plugin.json 时去掉该目录。
func stripTopDir(files map[string][]byte) map[string][]byte {
	if _, ok := files[manifestFile]; ok {
		return files
	}
	top := ""
	for name := range files {
		dir, _, ok := strings.Cut(name, "/")
		if !ok || top != "" && dir != top {
			return files
		}
		top = dir
	}
	out := make(map[string][]byte, len(files))
	for name, data := range files {
		out[strings.TrimPrefix(name, top+"/")] = data
	}
	return out
}

func checkText(label, s string, required bool, limit int) error {
	if required && strings.TrimSpace(s) == "" {
		return fmt.Errorf("plugin.json 缺少 %s", label)
	}
	if utf8.RuneCountInString(s) > limit {
		return fmt.Errorf("plugin.json 的 %s 不能超过 %d 个字符", label, limit)
	}
	return nil
}

func (p *jsPackage) validate() error {
	m := &p.manifest
	if !codePattern.MatchString(m.Code) {
		return fmt.Errorf("插件标识 %q 无效：必须以小写字母开头，只能包含小写字母、数字、下划线、短横线，2～32 个字符", m.Code)
	}
	if !versionPattern.MatchString(m.Version) {
		return fmt.Errorf("版本号 %q 无效", m.Version)
	}
	for _, c := range []struct {
		label, v string
		req      bool
		limit    int
	}{{"name", m.Name, true, maxManifestText}, {"author", m.Author, false, maxManifestText},
		{"description", m.Description, false, maxManifestLines}, {"url", m.URL, false, 255}} {
		if err := checkText(c.label, c.v, c.req, c.limit); err != nil {
			return err
		}
	}
	if m.Main == "" {
		m.Main = defaultMainFile
	}
	main, err := cleanPath(m.Main)
	if err != nil || !strings.HasSuffix(main, ".js") {
		return fmt.Errorf("入口文件 %q 无效", m.Main)
	}
	if _, ok := p.files[main]; !ok {
		return fmt.Errorf("缺少入口文件 %s", main)
	}
	m.Main = main
	if len(m.HTTP) > maxHTTPDomains {
		return fmt.Errorf("可访问的域名不能超过 %d 个", maxHTTPDomains)
	}
	seen := map[string]bool{}
	for _, s := range m.HTTP {
		d, err := parseDomain(s)
		if err != nil {
			return err
		}
		if !seen[d.String()] {
			seen[d.String()] = true
			p.domains = append(p.domains, d)
		}
	}
	m.HTTP = m.HTTP[:0]
	for _, d := range p.domains {
		m.HTTP = append(m.HTTP, d.String())
	}
	perms := Permissions()
	var ps []string
	for _, s := range m.Permissions {
		if _, ok := perms[s]; !ok {
			return fmt.Errorf("未知权限 %q", s)
		}
		if !slices.Contains(ps, s) {
			ps = append(ps, s)
		}
	}
	slices.Sort(ps)
	m.Permissions = ps
	if err := validateFields(m.Config); err != nil {
		return fmt.Errorf("配置项定义错误：%w", err)
	}
	return nil
}
