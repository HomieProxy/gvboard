package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
)

// 远程更新 v2node（决策 D84）：管理员点击「检查更新」时才查询 GitHub；下发的只有版本号，
// 下载地址固定在 v2node 程序中（gunzi-666/v2node 的 Release），节点机自行下载、校验 sha256 并替换。

// ReleasesURL gvboard 版 v2node 的 Release 列表。
const ReleasesURL = "https://api.github.com/repos/gunzi-666/v2node/releases?per_page=50"

var (
	versionPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+-gvboard[0-9A-Za-z.\-]*$`)

	errVersion = httpx.Validation("版本号格式不正确，应为 v0.4.9-gvboard.3 这样的 gvboard 版 v2node 版本")
)

// ValidVersion 是否为 gvboard 版 v2node 的版本号（与 v2node 侧校验一致）。
func ValidVersion(v string) bool { return len(v) <= 64 && versionPattern.MatchString(v) }

// Release 可更新到的版本。
type Release struct {
	Version     string `json:"version"`
	PublishedAt string `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
}

// Releases 查询 GitHub 上已发布且已构建完成（带安装包）的 gvboard 版 v2node，按发布时间倒序。
func (s *Service) Releases(ctx context.Context) ([]Release, error) {
	u := s.ReleasesURL
	if u == "" {
		u = ReleasesURL
	}
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Gvboard")
	resp, err := client.Do(req)
	if err != nil {
		s.log().Warn("查询 v2node 版本失败", zap.Error(err))
		return nil, httpx.Validation("连接 GitHub 失败，可以手动输入版本号")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpx.Validation(fmt.Sprintf("查询 GitHub 失败（HTTP %d），可以手动输入版本号", resp.StatusCode))
	}
	var list []struct {
		TagName     string `json:"tag_name"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return nil, httpx.Validation("GitHub 返回内容无法识别，可以手动输入版本号")
	}
	out := []Release{}
	for _, r := range list {
		built := false
		for _, a := range r.Assets {
			built = built || strings.HasSuffix(a.Name, ".zip")
		}
		if !r.Draft && built && ValidVersion(r.TagName) {
			out = append(out, Release{Version: r.TagName, PublishedAt: r.PublishedAt, Prerelease: r.Prerelease})
		}
	}
	return out, nil
}

// RequestUpdate 让节点机更新到指定版本，只对已接入且版本不同的节点机生效，返回生效的台数。
// 每次下发更新指令编号加 1，节点机对同一编号只尝试一次，失败后需管理员重新下发。
func (s *Service) RequestUpdate(ctx context.Context, ids []int64, version string) (int, error) {
	version = strings.TrimSpace(version)
	if !ValidVersion(version) {
		return 0, errVersion
	}
	ids = dedupe(ids)
	if len(ids) == 0 {
		return 0, httpx.Validation("请选择节点机")
	}
	var list []model.NodeMachine
	if err := s.DB.WithContext(ctx).Where("id IN ? AND token_hash IS NOT NULL AND version <> ?", ids, version).Find(&list).Error; err != nil {
		return 0, err
	}
	if len(list) == 0 {
		return 0, httpx.Validation("所选节点机未接入或已是该版本")
	}
	targets := make([]int64, 0, len(list))
	for _, m := range list {
		targets = append(targets, m.ID)
	}
	err := s.DB.WithContext(ctx).Model(&model.NodeMachine{}).Where("id IN ?", targets).Updates(map[string]any{
		"update_version": version, "update_id": gorm.Expr("update_id + 1"), "update_error": "",
	}).Error
	if err != nil {
		return 0, err
	}
	for i := range list {
		s.forget(&list[i])
	}
	s.log().Info("已下发 v2node 更新", zap.Int64s("machine_ids", targets), zap.String("version", version))
	return len(list), nil
}

// CancelUpdate 取消待执行的更新。节点机已开始下载时不会中断。
func (s *Service) CancelUpdate(ctx context.Context, id int64) error {
	m, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	err = s.DB.WithContext(ctx).Model(&model.NodeMachine{}).Where("id = ?", m.ID).Updates(map[string]any{
		"update_version": "", "update_error": "",
	}).Error
	s.forget(m)
	return err
}

// Update 下发给节点机的更新指令。
type Update struct {
	Version string `json:"version"`
	ID      int64  `json:"id"`
}

// Report 节点机拉取配置时附带的上次更新结果。
type Report struct {
	ID    int64
	Error string
}

// trackUpdate 节点机已运行目标版本时视为更新完成；上报的失败原因属于当前指令时记录下来。
func (s *Service) trackUpdate(ctx context.Context, m *model.NodeMachine, version string, rep Report) {
	if m.UpdateVersion == "" {
		return
	}
	values := map[string]any{}
	switch {
	case version == m.UpdateVersion:
		values["update_version"], values["update_error"] = "", ""
		s.log().Info("节点机已更新 v2node", zap.Int64("machine_id", m.ID), zap.String("version", version))
	case rep.ID == m.UpdateID && rep.Error != "":
		if e := clip(rep.Error, 255); e != m.UpdateError {
			values["update_error"] = e
		}
	}
	if len(values) == 0 {
		return
	}
	if err := s.DB.WithContext(ctx).Model(&model.NodeMachine{}).Where("id = ?", m.ID).Updates(values).Error; err != nil {
		s.log().Warn("记录节点机更新结果失败", zap.Error(err))
		return
	}
	s.forget(m)
	if v, ok := values["update_version"]; ok {
		m.UpdateVersion = v.(string)
	}
	if v, ok := values["update_error"]; ok {
		m.UpdateError = v.(string)
	}
}
