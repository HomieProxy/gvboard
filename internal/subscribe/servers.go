package subscribe

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gvboard/internal/cache"
	"gvboard/internal/model"
	"gvboard/internal/phpx"
	"gvboard/internal/util"
)

// serverTypes 节点合并顺序，与原项目 getAvailableServers 一致（决策 D41：同 sort 时保持此顺序）。
var serverTypes = []string{
	model.ServerTypeShadowsocks, model.ServerTypeVmess, model.ServerTypeTrojan, model.ServerTypeTuic,
	model.ServerTypeHysteria, model.ServerTypeVless, model.ServerTypeAnytls, model.ServerTypeV2node,
}

// keyedType 原项目按 ID 建索引的类型：父节点存在时继承父节点的 last_check_at 与 created_at。
func keyedType(typ string) bool {
	switch typ {
	case model.ServerTypeVless, model.ServerTypeVmess, model.ServerTypeTrojan:
		return false
	}
	return true
}

// secretTLSKeys 下发前从 tls_settings 中移除的服务端私密字段。
var secretTLSKeys = []string{"private_key", "ech_key", "tls_cert", "tls_key", "cert_file", "key_file", "provider", "dns_env"}

func newRows(typ string) any {
	switch typ {
	case model.ServerTypeShadowsocks:
		return &[]model.ServerShadowsocks{}
	case model.ServerTypeVmess:
		return &[]model.ServerVmess{}
	case model.ServerTypeTrojan:
		return &[]model.ServerTrojan{}
	case model.ServerTypeTuic:
		return &[]model.ServerTuic{}
	case model.ServerTypeHysteria:
		return &[]model.ServerHysteria{}
	case model.ServerTypeVless:
		return &[]model.ServerVless{}
	case model.ServerTypeAnytls:
		return &[]model.ServerAnytls{}
	case model.ServerTypeV2node:
		return &[]model.ServerV2node{}
	}
	return nil
}

// loadServers 读取全部节点并转为 PHP 数组（字段与原项目模型 toArray 一致），每类按 sort 稳定排序。
func (s *Service) loadServers(ctx context.Context) (map[string][]*phpx.Arr, error) {
	out := make(map[string][]*phpx.Arr, len(serverTypes))
	for _, typ := range serverTypes {
		rows := newRows(typ)
		if err := s.DB.WithContext(ctx).Order("id ASC").Find(rows).Error; err != nil {
			return nil, err
		}
		rv := reflect.ValueOf(rows).Elem()
		list := make([]*phpx.Arr, rv.Len())
		for i := range list {
			list[i] = rowArr(rv.Index(i))
		}
		sortBySort(list)
		out[typ] = list
	}
	return out, nil
}

func sortBySort(list []*phpx.Arr) {
	sort.SliceStable(list, func(i, j int) bool {
		return phpx.Compare(list[i].Get("sort"), list[j].Get("sort")) < 0
	})
}

func rowArr(rv reflect.Value) *phpx.Arr {
	a := phpx.New()
	addFields(a, rv)
	return a
}

func addFields(a *phpx.Arr, rv reflect.Value) {
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Anonymous {
			addFields(a, rv.Field(i))
			continue
		}
		col := gormColumn(f.Tag.Get("gorm"))
		if col == "" {
			continue
		}
		a.Set(col, phpValue(rv.Field(i).Interface()))
	}
}

func gormColumn(tag string) string {
	for _, part := range strings.Split(tag, ";") {
		if v, ok := strings.CutPrefix(part, "column:"); ok {
			return v
		}
	}
	return ""
}

// phpValue 把模型字段转为原项目 toArray 后的值：JSON 列按 array 转换解码，可空列为 null。
func phpValue(v any) any {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64, string:
		return x
	case *int64:
		if x == nil {
			return nil
		}
		return *x
	case *int:
		if x == nil {
			return nil
		}
		return int64(*x)
	case *string:
		if x == nil {
			return nil
		}
		return *x
	case model.JSON:
		if len(x) == 0 {
			return nil
		}
		d, err := phpx.JSONDecode(x)
		if err != nil {
			return nil
		}
		return d
	case model.IntList:
		if x == nil {
			return nil
		}
		a := phpx.New()
		for _, n := range x {
			a.Push(n)
		}
		return a
	case model.StrList:
		if x == nil {
			return nil
		}
		return phpx.Strings(x...)
	}
	return nil
}

// lastCheck 批量读取节点最后检查时间（单元 10 写入），键为 "类型:ID"。
func (s *Service) lastCheck(ctx context.Context, rows map[string][]*phpx.Arr) (map[string]any, error) {
	seen := map[string]bool{}
	var keys, ids []string
	add := func(typ string, id int64) {
		k := cache.ServerKey(typ, id)
		if !seen[k] {
			seen[k] = true
			ids = append(ids, k)
			keys = append(keys, s.Cache.KeyOf(cache.KeyServerLastCheckAt, k))
		}
	}
	for typ, list := range rows {
		for _, r := range list {
			add(typ, phpx.Int(r.Get("id")))
			if p := r.Get("parent_id"); phpx.Truthy(p) {
				add(typ, phpx.Int(p))
			}
		}
	}
	out := make(map[string]any, len(keys))
	for start := 0; start < len(keys); start += 500 {
		end := min(start+500, len(keys))
		vals, err := s.Cache.MGet(ctx, keys[start:end]...).Result()
		if err != nil {
			return nil, err
		}
		for i, v := range vals {
			if str, ok := v.(string); ok {
				if n, err := strconv.ParseInt(strings.TrimSpace(str), 10, 64); err == nil {
					out[ids[start+i]] = n
				}
			}
		}
	}
	return out, nil
}

// Servers 订阅可用的节点，对应原项目 ServerService::getAvailableServers：
// 只含显示中且权限组包含订阅权限组的节点；端口段随机取端口；移除 TLS 私密字段；
// 按 sort 排序后补充 mport / is_online / cache_key。
func (s *Service) Servers(ctx context.Context, groupID *int64) ([]*phpx.Arr, error) {
	if groupID == nil {
		return []*phpx.Arr{}, nil
	}
	rows, err := s.loadServers(ctx)
	if err != nil {
		return nil, err
	}
	checks, err := s.lastCheck(ctx, rows)
	if err != nil {
		return nil, err
	}
	lc := func(typ string, id any) any { return checks[cache.ServerKey(typ, phpx.Int(id))] }

	var out []*phpx.Arr
	for _, typ := range serverTypes {
		list := rows[typ]
		byID := make(map[int64]*phpx.Arr, len(list))
		for _, r := range list {
			byID[phpx.Int(r.Get("id"))] = r
		}
		for _, r := range list {
			if !phpx.Truthy(r.Get("show")) {
				continue
			}
			r.Set("type", typ)
			id := r.Get("id")
			if keyedType(typ) {
				r.Set("last_check_at", lc(typ, id))
			}
			if !inGroup(*groupID, r.Get("group_id")) {
				continue
			}
			if !keyedType(typ) {
				if strings.Contains(r.Str("port"), "-") {
					r.Set("port", s.randomPort(r.Str("port")))
				}
				if p := r.Get("parent_id"); phpx.Truthy(p) {
					r.Set("last_check_at", lc(typ, p))
				} else {
					r.Set("last_check_at", lc(typ, id))
				}
				if typ == model.ServerTypeVless {
					stripSecrets(r)
				}
				out = append(out, r.Clone())
				continue
			}
			if (typ == model.ServerTypeShadowsocks || typ == model.ServerTypeAnytls) && strings.Contains(r.Str("port"), "-") {
				r.Set("port", s.randomPort(r.Str("port")))
			}
			if p := r.Get("parent_id"); p != nil {
				if parent, ok := byID[phpx.Int(p)]; ok {
					r.Set("last_check_at", lc(typ, p))
					r.Set("created_at", parent.Get("created_at"))
				}
			}
			switch typ {
			case model.ServerTypeHysteria:
				r.Set("server_key", phpx.ServerKey(r.Get("created_at"), 16))
			case model.ServerTypeShadowsocks:
				if r.Get("obfs") == "http" {
					r.Set("obfs", "http")
					r.Set("obfs-host", r.Get("obfs_settings", "host"))
					r.Set("obfs-path", r.Get("obfs_settings", "path"))
				}
			case model.ServerTypeV2node:
				stripSecrets(r)
			}
			c := r.Clone()
			if typ == model.ServerTypeV2node {
				c.Del("trusted_x_forwarded_for")
			}
			out = append(out, c)
		}
	}

	sortBySort(out)
	now := s.now().Unix()
	for _, r := range out {
		port := r.Get("port")
		if strings.Index(phpx.Str(port), "-") > 0 {
			r.Set("mport", phpx.Str(port))
		} else {
			r.Set("port", phpx.Int(port))
		}
		online := int64(1)
		if phpx.Compare(now-300, r.Get("last_check_at")) > 0 {
			online = 0
		}
		r.Set("is_online", online)
		r.Set("cache_key", fmt.Sprintf("%s-%s-%s-%d", r.Str("type"), r.Str("id"), r.Str("updated_at"), online))
	}
	return out, nil
}

func inGroup(groupID int64, groups any) bool {
	a, ok := groups.(*phpx.Arr)
	return ok && phpx.InArray(groupID, a)
}

// stripSecrets 移除 tls_settings 与 encryption_settings 中的私密字段。原项目经模型 array 转换写回，
// 相当于一次 JSON 编解码，这里同样做一次以保持数值等表示一致。
func stripSecrets(r *phpx.Arr) {
	if tls, ok := r.Get("tls_settings").(*phpx.Arr); ok {
		c := tls.Clone()
		for _, k := range secretTLSKeys {
			c.Del(k)
		}
		r.Set("tls_settings", jsonRoundTrip(c))
	}
	if enc, ok := r.Get("encryption_settings").(*phpx.Arr); ok && enc.Isset("private_key") {
		c := enc.Clone()
		c.Del("private_key")
		r.Set("encryption_settings", jsonRoundTrip(c))
	}
}

func jsonRoundTrip(a *phpx.Arr) any {
	v, err := phpx.JSONDecode(phpx.JSONEncode(a, 0))
	if err != nil {
		return a
	}
	return v
}

// randomPort 原项目 Helper::randomPort：rand(起始, 结束)。
func (s *Service) randomPort(r string) int64 {
	parts := strings.Split(r, "-")
	lo := phpx.Int(parts[0])
	hi := lo
	if len(parts) > 1 {
		hi = phpx.Int(parts[1])
	}
	if hi < lo {
		lo, hi = hi, lo
	}
	if s.Rand != nil {
		return s.Rand(lo, hi)
	}
	return util.RandomInt(lo, hi)
}
