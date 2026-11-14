package nodeapi

import (
	"context"
	"encoding/json"

	"gvboard/internal/model"
	"gvboard/internal/node"
)

// routes 节点关联的路由规则，按节点中 route_id 的顺序输出（原项目 ServerService::getRoutes）。
func (s *Service) routes(ctx context.Context, ids model.IntList) ([]obj, error) {
	var rows []model.ServerRoute
	if err := s.DB.WithContext(ctx).Where("id IN ?", []int64(ids)).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[int64]model.ServerRoute, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]obj, 0, len(rows))
	for _, id := range ids {
		r, ok := byID[id]
		if !ok {
			continue
		}
		delete(byID, id)
		var match any = r.Match
		var v any
		if json.Unmarshal([]byte(r.Match), &v) == nil {
			switch v.(type) {
			case map[string]any, []any:
				match = phpArray(model.JSON(r.Match))
			}
		}
		out = append(out, obj{{"id", r.ID}, {"match", match}, {"action", r.Action}, {"action_value", r.ActionValue}})
	}
	return out, nil
}

func (s *Service) withRoutes(ctx context.Context, o obj, srv node.Server) (obj, error) {
	ids := srv.Common().RouteID
	if len(ids) == 0 {
		return o, nil
	}
	rs, err := s.routes(ctx, ids)
	if err != nil {
		return nil, err
	}
	return append(o, field{"routes", rs}), nil
}

// UniConfig UniProxy 节点配置（原项目 UniProxyController::config）。
func (s *Service) UniConfig(ctx context.Context, srv node.Server) (obj, error) {
	var o obj
	switch v := srv.(type) {
	case *model.ServerShadowsocks:
		o = obj{{"server_port", v.ServerPort}, {"cipher", v.Cipher}, {"obfs", v.Obfs}, {"obfs_settings", phpArray(v.ObfsSettings)}}
		if k := ssServerKey(v.Cipher, v.CreatedAt); k != "" {
			o = append(o, field{"server_key", k})
		}
	case *model.ServerVmess:
		o = obj{{"server_port", v.ServerPort}, {"network", v.Network}, {"networkSettings", phpArray(v.NetworkSettings)}, {"tls", v.TLS}}
	case *model.ServerVless:
		o = obj{
			{"server_port", v.ServerPort}, {"network", v.Network}, {"networkSettings", phpArray(v.NetworkSettings)},
			{"tls", v.TLS}, {"flow", v.Flow}, {"tls_settings", phpArray(v.TLSSettings)},
			{"encryption", v.Encryption}, {"encryption_settings", phpArray(v.EncryptionSettings)},
		}
	case *model.ServerTrojan:
		o = obj{{"host", v.Host}, {"network", v.Network}, {"networkSettings", phpArray(v.NetworkSettings)}, {"server_port", v.ServerPort}, {"server_name", v.ServerName}}
	case *model.ServerTuic:
		o = obj{{"server_port", v.ServerPort}, {"server_name", v.ServerName}, {"congestion_control", v.CongestionControl}, {"zero_rtt_handshake", v.ZeroRTTHandshake != 0}}
	case *model.ServerHysteria:
		o = obj{{"version", v.Version}, {"host", v.Host}, {"server_port", v.ServerPort}, {"server_name", v.ServerName}, {"up_mbps", v.UpMbps}, {"down_mbps", v.DownMbps}}
		switch v.Version {
		case 1:
			o = append(o, field{"obfs", v.ObfsPassword})
		case 2:
			o = append(o, field{"ignore_client_bandwidth", v.UpMbps == 0 && v.DownMbps == 0}, field{"obfs", v.Obfs}, field{"obfs-password", v.ObfsPassword})
		}
	case *model.ServerAnytls:
		o = obj{{"server_port", v.ServerPort}, {"server_name", v.ServerName}, {"padding_scheme", phpArray(v.PaddingScheme)}}
	}
	// v2node 通过 UniProxy 拉取时原项目只返回 base_config 与 routes。
	o = append(o, field{"base_config", obj{
		{"push_interval", s.Settings.Int("server_push_interval")},
		{"pull_interval", s.Settings.Int("server_pull_interval")},
	}})
	return s.withRoutes(ctx, o, srv)
}

// V2Config v2node 节点配置（原项目 V2\Server\ServerController::config）。
func (s *Service) V2Config(ctx context.Context, v *model.ServerV2node) (obj, error) {
	o := obj{
		{"listen_ip", v.ListenIP}, {"server_port", v.ServerPort}, {"network", v.Network},
		{"network_settings", phpArray(v.NetworkSettings)}, {"trusted_x_forwarded_for", phpArray(v.TrustedXForwardedFor)},
		{"protocol", v.Protocol}, {"tls", v.TLS}, {"tls_settings", phpArray(v.TLSSettings)},
		{"encryption", v.Encryption}, {"encryption_settings", phpArray(v.EncryptionSettings)}, {"flow", v.Flow},
		{"cipher", v.Cipher}, {"congestion_control", v.CongestionControl}, {"zero_rtt_handshake", v.ZeroRTTHandshake != 0},
		{"up_mbps", v.UpMbps}, {"down_mbps", v.DownMbps}, {"obfs", v.Obfs}, {"obfs_password", v.ObfsPassword},
		{"padding_scheme", phpArray(v.PaddingScheme)},
	}
	if v.Cipher != nil {
		if k := ssServerKey(*v.Cipher, v.CreatedAt); k != "" {
			o = append(o, field{"server_key", k})
		}
	}
	o = append(o,
		field{"ignore_client_bandwidth", v.UpMbps == 0 && v.DownMbps == 0},
		field{"base_config", obj{
			{"push_interval", s.Settings.Int("server_push_interval")},
			{"pull_interval", s.Settings.Int("server_pull_interval")},
			{"node_report_min_traffic", s.Settings.Int("server_node_report_min_traffic")},
			{"device_online_min_traffic", s.Settings.Int("server_device_online_min_traffic")},
		}},
	)
	return s.withRoutes(ctx, o, v)
}
