package machine

import (
	"context"
	"encoding/json"
	"strings"

	"gvboard/internal/model"
)

// 被跳过节点的上报（决策 D90）：一台节点机接入多个面板时，节点可能因端口冲突或启动失败被跳过，
// v2node 把属于本面板的被跳过节点上报给面板，管理端「节点机」页面显示。

const (
	maxNodeErrors   = 50
	maxNodeErrorLen = 200
)

// NodeError 被跳过的节点与原因。
type NodeError struct {
	NodeID int64  `json:"node_id"`
	Error  string `json:"error"`
}

func clipRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ReportNodeErrors 保存节点机上报的被跳过节点，只保留属于这台节点机的节点（每个节点一条），空列表表示全部恢复。
func (s *Service) ReportNodeErrors(ctx context.Context, m *model.NodeMachine, list []NodeError) error {
	out := []NodeError{}
	seen := map[int64]bool{}
	for _, e := range list {
		if len(out) >= maxNodeErrors {
			break
		}
		if seen[e.NodeID] || !m.ServerIDs.Contains(e.NodeID) {
			continue
		}
		seen[e.NodeID] = true
		msg := clipRunes(e.Error, maxNodeErrorLen)
		if msg == "" {
			msg = "启动失败"
		}
		out = append(out, NodeError{NodeID: e.NodeID, Error: msg})
	}
	var v any
	if len(out) > 0 {
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		v = string(b)
	}
	return s.DB.WithContext(ctx).Model(&model.NodeMachine{}).Where("id = ?", m.ID).Updates(map[string]any{"node_errors": v}).Error
}

// nodeErrors 读取上报的被跳过节点，只返回仍属于这台节点机的节点。
func nodeErrors(m model.NodeMachine) []NodeError {
	var list []NodeError
	if err := m.NodeErrors.Decode(&list); err != nil {
		return []NodeError{}
	}
	out := make([]NodeError, 0, len(list))
	for _, e := range list {
		if m.ServerIDs.Contains(e.NodeID) {
			out = append(out, e)
		}
	}
	return out
}
