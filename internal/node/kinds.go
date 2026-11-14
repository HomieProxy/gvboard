// Package node 节点管理：权限组、路由规则、8 类节点的增删改复制、节点总览与排序，
// 对应原项目 Admin/Server/* 控制器与 ServerService 的管理端部分。
package node

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"gvboard/internal/model"
)

// Server 各类节点模型（均内嵌 model.ServerBase）。
type Server interface {
	Common() *model.ServerBase
	TableName() string
}

// Types 全部节点类型，顺序与原项目节点总览的合并顺序一致。
var Types = []string{
	model.ServerTypeShadowsocks, model.ServerTypeVmess, model.ServerTypeTrojan, model.ServerTypeTuic,
	model.ServerTypeHysteria, model.ServerTypeVless, model.ServerTypeAnytls, model.ServerTypeV2node,
}

// New 按类型创建空模型，类型无效时返回 nil。
func New(typ string) Server {
	switch typ {
	case model.ServerTypeShadowsocks:
		return &model.ServerShadowsocks{}
	case model.ServerTypeVmess:
		return &model.ServerVmess{}
	case model.ServerTypeTrojan:
		return &model.ServerTrojan{}
	case model.ServerTypeTuic:
		return &model.ServerTuic{}
	case model.ServerTypeHysteria:
		return &model.ServerHysteria{}
	case model.ServerTypeVless:
		return &model.ServerVless{}
	case model.ServerTypeAnytls:
		return &model.ServerAnytls{}
	case model.ServerTypeV2node:
		return &model.ServerV2node{}
	}
	return nil
}

// List 读取某一类型的全部节点（按 ID 排序），类型无效时返回 nil。
func List(ctx context.Context, db *gorm.DB, typ string) ([]Server, error) {
	db = db.WithContext(ctx)
	switch typ {
	case model.ServerTypeShadowsocks:
		return list[model.ServerShadowsocks](db)
	case model.ServerTypeVmess:
		return list[model.ServerVmess](db)
	case model.ServerTypeTrojan:
		return list[model.ServerTrojan](db)
	case model.ServerTypeTuic:
		return list[model.ServerTuic](db)
	case model.ServerTypeHysteria:
		return list[model.ServerHysteria](db)
	case model.ServerTypeVless:
		return list[model.ServerVless](db)
	case model.ServerTypeAnytls:
		return list[model.ServerAnytls](db)
	case model.ServerTypeV2node:
		return list[model.ServerV2node](db)
	}
	return nil, nil
}

func list[T any, P interface {
	*T
	Server
}](db *gorm.DB) ([]Server, error) {
	var rows []T
	if err := db.Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Server, len(rows))
	for i := range rows {
		out[i] = P(&rows[i])
	}
	return out, nil
}

// Typed 带类型的节点。
type Typed struct {
	Type string
	Server
}

// All 读取全部类型的节点，按 Types 顺序拼接。
func All(ctx context.Context, db *gorm.DB) ([]Typed, error) {
	var out []Typed
	for _, typ := range Types {
		rows, err := List(ctx, db, typ)
		if err != nil {
			return nil, err
		}
		for _, s := range rows {
			out = append(out, Typed{Type: typ, Server: s})
		}
	}
	return out, nil
}

// Find 按类型与 ID 读取节点，不存在时返回 nil。
func Find(ctx context.Context, db *gorm.DB, typ string, id int64) (Server, error) {
	s := New(typ)
	if s == nil {
		return nil, nil
	}
	err := db.WithContext(ctx).Take(s, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}
