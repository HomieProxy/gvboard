# Gvboard

Gvboard 是用 Go 重写的代理服务面板，功能与原 v2board（wyx2685 分支）对齐，可以直接接入原项目的生产数据库继续使用。

- 单个可执行文件：用户端、管理端页面已编译在程序中，不需要 PHP 与 Web 根目录
- 数据库支持 MySQL 5.7+ / 8.0 与 PostgreSQL 13+，提供 MySQL → PostgreSQL 转换命令
- 兼容原项目：数据库、节点通信接口、订阅格式、支付回调；迁移后老订阅链接可继续使用

## 主要功能

- **单账号多订阅**：一个账号可购买多个套餐，每个订阅独立的流量、到期时间、订阅链接
- **订阅风控**：按 User-Agent、IP、请求头等规则拦截或记录订阅请求，频率限制、防分享，请求日志与按订阅统计
- **插件**：内置插件（Go，编译进程序）与 JS 插件（沙箱运行，需声明权限与可访问的域名），可监听订单、工单等事件
- **主题**：内置主题，支持安装外部主题
- **部署方式**：前后端不分离（一个程序提供全部页面）或分离部署（前端单独放在 CDN / 静态站点）
- **通知**：多个 SMTP 服务器（故障转移 / 轮询）、管理端可编辑的邮件模板；Telegram 机器人，管理员通知可发到话题群组（订单、工单、告警、每日统计）
- **管理端**：订单补单、订阅管理、节点与权限组、优惠券、礼品卡、公告、工单、知识库、队列监控、系统日志

## 快速安装

Linux 服务器（root 用户）使用安装脚本，支持全新安装、从原 v2board 迁移、升级：

```bash
mkdir -p /root/gvboard-install && cd /root/gvboard-install
curl -fsSLO https://github.com/gunzi-666/gvboard/releases/latest/download/install.sh
bash install.sh
```

国内服务器下载慢时可加速：`GITHUB_PROXY=https://ghfast.top/ bash install.sh`。

需要事先准备好空数据库（MySQL 字符集 `utf8mb4`）与 Redis 6.0+。Docker、宝塔面板、手动安装、从原生产库迁移的步骤见 [部署文档](docs/部署文档.md)。

## 从源码编译

需要 Go 1.26、Node.js 20+（只有修改前端时需要）。

```bash
# 前端（可选，仓库中已包含编译好的页面）
cd web
npm ci
npm run build
cd ..

# 后端
go build -trimpath -ldflags "-s -w" -o gvboard ./cmd/gvboard

# 生成配置、建表、创建管理员、启动
./gvboard init
./gvboard migrate
./gvboard create-admin admin@example.com
./gvboard serve
```

常用命令见 `gvboard --help` 与部署文档的“命令行速查”。

## 文档

| 文档 | 内容 |
|------|------|
| [部署文档](docs/部署文档.md) | 安装、Docker、迁移原生产库、PostgreSQL 转换、分离部署、多实例、升级、备份、宝塔面板 |
| [插件开发说明](docs/插件开发说明.md) | Go / JS 插件的结构、权限、事件与接口 |
| [主题开发说明](docs/主题开发说明.md) | 主题目录结构与安装 |
| [接口参考](docs/接口参考/) | 用户端与管理端 API |

## 发布

推送 `v` 开头的标签（如 `v1.0.0`）后，GitHub Actions 会运行测试、编译各平台程序并发布 Release，详见部署文档第 14 节。
