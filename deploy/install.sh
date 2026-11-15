#!/usr/bin/env bash
# Gvboard 安装 / 迁移 / 升级脚本（Linux x86_64 / arm64）
#
#   bash install.sh            显示菜单
#   bash install.sh install    全新安装
#   bash install.sh migrate    从原 v2board 迁移
#   bash install.sh upgrade    升级
#
# 环境变量（可选）：
#   GITHUB_PROXY   GitHub 下载加速前缀，如 https://ghfast.top/
#   GVB_VERSION    安装的版本标签，如 v1.0.0，默认最新版本
#   GVB_BINARY     使用本地的 gvboard 可执行文件，不下载（放在脚本同一目录时自动使用）
#   GVB_REPO       GitHub 仓库 owner/name，发布时已自动填写
set -euo pipefail

REPO="${GVB_REPO:-__GVB_REPO__}"
VERSION="${GVB_VERSION:-latest}"
GITHUB_PROXY="${GITHUB_PROXY:-}"
SCRIPT_DIR="$(pwd)"
[ -f "${BASH_SOURCE[0]:-}" ] && SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR=""

# ---------- 输入输出 ----------

if [ -t 1 ]; then
  C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_BLUE=$'\033[36m'; C_BOLD=$'\033[1m'; C_RESET=$'\033[0m'
else
  C_RED=""; C_GREEN=""; C_YELLOW=""; C_BLUE=""; C_BOLD=""; C_RESET=""
fi

info() { printf '%s\n' "${C_BLUE}[信息]${C_RESET} $*"; }
ok()   { printf '%s\n' "${C_GREEN}[完成]${C_RESET} $*"; }
warn() { printf '%s\n' "${C_YELLOW}[注意]${C_RESET} $*"; }
die()  { printf '%s\n' "${C_RED}[错误]${C_RESET} $*" >&2; exit 1; }
step() { printf '\n%s\n' "${C_BOLD}==> $*${C_RESET}"; }

TTY=/dev/tty
[ -r "$TTY" ] || die "需要在交互式终端中运行"

# ask 变量名 提示 [默认值]
ask() {
  local __var=$1 __prompt=$2 __def=${3:-} __val
  while :; do
    if [ -n "$__def" ]; then
      read -r -p "$__prompt [$__def]: " __val <"$TTY" || true
      __val=${__val:-$__def}
    else
      read -r -p "$__prompt: " __val <"$TTY" || true
    fi
    [ -n "$__val" ] && break
    warn "不能为空"
  done
  printf -v "$__var" '%s' "$__val"
}

# ask_secret 变量名 提示 [允许为空 1/0]，输入不回显
ask_secret() {
  local __var=$1 __prompt=$2 __empty=${3:-0} __val
  while :; do
    read -r -s -p "$__prompt: " __val <"$TTY" || true
    printf '\n'
    { [ -n "$__val" ] || [ "$__empty" = 1 ]; } && break
    warn "不能为空"
  done
  printf -v "$__var" '%s' "$__val"
}

# confirm 提示 [默认 y/n]
confirm() {
  local __def=${2:-n} __val __hint="y/N"
  [ "$__def" = y ] && __hint="Y/n"
  read -r -p "$1 [$__hint]: " __val <"$TTY" || true
  __val=${__val:-$__def}
  [[ "$__val" =~ ^[Yy] ]]
}

pause() { read -r -p "$1，完成后按回车继续……" _ <"$TTY" || true; }

cleanup() { [ -n "$TMP_DIR" ] && rm -rf "$TMP_DIR"; }
trap cleanup EXIT

# ---------- 程序 ----------

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) ARCH=amd64 ;;
    aarch64 | arm64) ARCH=arm64 ;;
    *) die "不支持的 CPU 架构：$(uname -m)" ;;
  esac
}

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 3 --connect-timeout 15 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    die "需要 curl 或 wget"
  fi
}

# 准备新程序，路径写入 NEW_BINARY
get_binary() {
  TMP_DIR=$(mktemp -d)
  local bin="${GVB_BINARY:-}"
  if [ -z "$bin" ] && [ -f "$SCRIPT_DIR/gvboard" ] && [ "$SCRIPT_DIR" != "${INSTALL_DIR:-}" ]; then
    bin="$SCRIPT_DIR/gvboard"
  fi
  if [ -n "$bin" ]; then
    [ -f "$bin" ] || die "找不到 $bin"
    info "使用本地程序：$bin"
    cp "$bin" "$TMP_DIR/gvboard"
  else
    # 发布时占位符会被替换为 owner/name；这里不能写占位符原文，否则会被一起替换
    [[ "$REPO" == */* ]] || die "请把 gvboard 可执行文件放在脚本同一目录，或设置 GVB_REPO=owner/name"
    local base="https://github.com/$REPO/releases" name="gvboard_linux_${ARCH}.tar.gz"
    if [ "$VERSION" = latest ]; then base="$base/latest/download"; else base="$base/download/$VERSION"; fi
    info "下载 ${GITHUB_PROXY}${base}/${name}"
    fetch "${GITHUB_PROXY}${base}/${name}" "$TMP_DIR/$name" || die "下载失败：可设置 GITHUB_PROXY 加速，或手动下载后把 gvboard 放在脚本同一目录"
    if fetch "${GITHUB_PROXY}${base}/sha256sums.txt" "$TMP_DIR/sha256sums.txt" 2>/dev/null; then
      (cd "$TMP_DIR" && grep " ${name}\$" sha256sums.txt | sha256sum -c - >/dev/null) || die "文件校验失败，请重新下载"
      ok "文件校验通过"
    fi
    tar -xzf "$TMP_DIR/$name" -C "$TMP_DIR" gvboard || die "解压失败"
  fi
  chmod 755 "$TMP_DIR/gvboard"
  "$TMP_DIR/gvboard" version >/dev/null 2>&1 || die "程序无法运行（架构不匹配或文件损坏）"
  NEW_BINARY="$TMP_DIR/gvboard"
}

default_dir() { if [ -d /www/server/panel ]; then echo /www/gvboard; else echo /opt/gvboard; fi; }

gvb() { (cd "$INSTALL_DIR" && ./gvboard "$@"); }

prepare_dir() {
  ask INSTALL_DIR "安装目录（不要放在网站根目录下）" "$(default_dir)"
  INSTALL_DIR=${INSTALL_DIR%/}
  mkdir -p "$INSTALL_DIR"
  install -m 755 "$NEW_BINARY" "$INSTALL_DIR/gvboard"
  ok "程序已放到 $INSTALL_DIR/gvboard（$("$INSTALL_DIR/gvboard" version 2>/dev/null | head -n 1)）"
}

# ---------- 配置 ----------

ask_db() { # ask_db [固定的数据库类型]
  if [ -n "${1:-}" ]; then
    DB_DRIVER=$1
  else
    ask DB_DRIVER "数据库类型（mysql / postgres）" mysql
    [[ "$DB_DRIVER" =~ ^(mysql|postgres)$ ]] || die "数据库类型只能是 mysql 或 postgres"
  fi
  local def_port=3306
  [ "$DB_DRIVER" = postgres ] && def_port=5432
  ask DB_HOST "数据库地址" 127.0.0.1
  ask DB_PORT "数据库端口" "$def_port"
  ask DB_NAME "数据库名"
  ask DB_USER "数据库用户名"
  ask_secret DB_PASS "数据库密码（输入时不显示）"
}

# gen_config [额外的 init 参数...]：生成配置文件，已有时询问是否保留
gen_config() {
  local cfg="$INSTALL_DIR/config.yaml"
  if [ -f "$cfg" ]; then
    warn "$cfg 已存在"
    if confirm "保留已有配置文件？（选否则重新生成，原文件备份为 config.yaml.bak）" y; then
      return
    fi
    mv -f "$cfg" "$cfg.bak"
  fi
  (cd "$INSTALL_DIR" && GVB_DATABASE_PASSWORD="$DB_PASS" ./gvboard init \
    --db "$DB_DRIVER" --db-host "$DB_HOST" --db-port "$DB_PORT" --db-name "$DB_NAME" --db-user "$DB_USER" \
    --listen "127.0.0.1:8080" "$@" >/dev/null)
  chmod 600 "$cfg"
  ok "已生成配置文件 $cfg"
}

edit_config() {
  step "检查并修改配置文件"
  cat <<EOF
请打开 ${C_BOLD}$INSTALL_DIR/config.yaml${C_RESET}（宝塔：文件 → 双击编辑；或 vi），确认以下配置项：
  server.listen     监听地址，默认 127.0.0.1:8080，端口被占用时修改
  database.*        数据库连接（已按你的输入填写）
  redis.addr        Redis 地址，默认 127.0.0.1:6379
  redis.password    Redis 密码，没有设置则留空
  redis.db          Redis 库号，与其它程序共用 Redis 时改成未使用的库号
其它配置项文件中都有中文注释，一般保持默认即可。
EOF
  pause "修改并保存"
}

# 检查数据库连接，失败时让用户修改配置后重试（Redis 在启动服务时检查）
check_config() {
  while :; do
    info "检查数据库连接……"
    if gvb migrate --status >/dev/null 2>&1; then
      ok "连接正常"
      return
    fi
    warn "连接失败："
    gvb migrate --status 2>&1 | tail -n 5 || true
    confirm "修改 config.yaml 后重试？（选否退出）" y || exit 1
    pause "修改 $INSTALL_DIR/config.yaml"
  done
}

create_admin() {
  local email
  ask email "$1"
  gvb create-admin "$email"
}

finish_guide() {
  local port
  port=$(grep -E '^[[:space:]]*listen:' "$INSTALL_DIR/config.yaml" | head -n 1 | sed -E 's/.*:([0-9]+)"?[[:space:]]*$/\1/')
  step "最后：进程守护与网站（请自行配置）"
  cat <<EOF
1. 设置文件归属（以 www 用户运行为例，其它用户自行替换）：
     chown -R www:www $INSTALL_DIR && chmod 600 $INSTALL_DIR/config.yaml

2. 添加进程守护，让 Gvboard 常驻运行（任选一种）：
   宝塔“进程守护管理器 → 添加守护进程”：
     名称：gvboard    启动用户：www    运行目录：$INSTALL_DIR
     启动命令：$INSTALL_DIR/gvboard serve    进程数量：1
   或 systemd：参考部署文档第 4.2 节的 gvboard.service 示例
   启动失败时查看守护进程日志，常见原因：Redis 地址或密码错误、端口被占用

3. 网站反向代理到 ${C_BOLD}http://127.0.0.1:${port:-8080}${C_RESET}
   宝塔：网站 → 站点设置 → 反向代理，目标 URL 填上面的地址，发送域名 \$host，不要开启缓存；
   站点 PHP 版本选“纯静态”、伪静态清空。防火墙不要放行 ${port:-8080} 端口。

4. 登录管理端（地址见上方 create-admin 的输出），在“系统配置”中填写站点网址、节点通信密钥、邮件等。
EOF
}

# ---------- 全新安装 ----------

do_install() {
  step "全新安装"
  echo "请先创建一个空数据库（MySQL 字符集 utf8mb4），并准备好 Redis。"
  confirm "已创建数据库，继续？" y || exit 0
  get_binary
  prepare_dir
  step "填写数据库连接"
  ask_db
  gen_config
  edit_config
  check_config
  step "创建数据表"
  gvb migrate
  step "创建管理员"
  create_admin "管理员邮箱"
  warn "请记下上面的管理员密码与管理端地址（密码只显示一次）"
  finish_guide
}

# ---------- 从原 v2board 迁移 ----------

env_get() { # env_get 文件 键
  local v
  v=$(grep -E "^[[:space:]]*$2[[:space:]]*=" "$1" 2>/dev/null | tail -n 1 | cut -d= -f2- | sed -E 's/\r$//; s/^[[:space:]]+//; s/[[:space:]]+$//') || true
  v=${v#\"}; v=${v%\"}; v=${v#\'}; v=${v%\'}
  printf '%s' "$v"
}

find_mysql() {
  MYSQL_BIN=$(command -v mysql || true)
  [ -z "$MYSQL_BIN" ] && [ -x /www/server/mysql/bin/mysql ] && MYSQL_BIN=/www/server/mysql/bin/mysql
  [ -n "$MYSQL_BIN" ] || die "找不到 mysql 命令行客户端（宝塔安装 MySQL 后自带；其它系统安装 mysql-client）"
}

db_exec() { MYSQL_PWD="$DB_PASS" "$MYSQL_BIN" -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" "$@"; }

# 在迁移文件夹中找数据库备份，解压后路径写入 SQL_FILE
prepare_dump() {
  local files=() f n
  while IFS= read -r f; do files+=("$f"); done < <(find "$SRC_DIR" -maxdepth 1 -type f \( -iname '*.zip' -o -iname '*.sql' -o -iname '*.gz' \) | sort)
  [ ${#files[@]} -gt 0 ] || die "$SRC_DIR 中没有数据库备份（.zip / .sql / .sql.gz）"
  f=${files[0]}
  if [ ${#files[@]} -gt 1 ]; then
    info "找到多个备份文件："
    for n in "${!files[@]}"; do echo "  $((n + 1))) $(basename "${files[$n]}")"; done
    ask n "选择序号" 1
    [[ "$n" =~ ^[0-9]+$ ]] && [ "$n" -ge 1 ] && [ "$n" -le ${#files[@]} ] || die "序号无效"
    f=${files[$((n - 1))]}
  fi
  info "数据库备份：$(basename "$f")"
  local out="$TMP_DIR/dump"
  mkdir -p "$out"
  case "${f,,}" in
    *.zip)
      command -v unzip >/dev/null 2>&1 || die "需要 unzip（yum install -y unzip 或 apt install -y unzip）"
      unzip -q -o "$f" -d "$out" || die "解压失败"
      SQL_FILE=$(find "$out" -type f -iname '*.sql' -printf '%s %p\n' | sort -rn | head -n 1 | cut -d' ' -f2-)
      ;;
    *.gz)
      gunzip -c "$f" >"$out/dump.sql" || die "解压失败"
      SQL_FILE="$out/dump.sql"
      ;;
    *) SQL_FILE=$f ;;
  esac
  [ -n "$SQL_FILE" ] && [ -s "$SQL_FILE" ] || die "备份中没有找到 .sql 文件"
}

import_dump() {
  find_mysql
  [[ "$DB_NAME" =~ ^[A-Za-z0-9_-]+$ ]] || die "数据库名只能包含字母、数字、下划线、短横线"
  local tables
  tables=$(db_exec -N -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='$DB_NAME'" 2>/dev/null) ||
    die "无法连接数据库，请检查地址、端口、用户名、密码"
  if [ "$tables" != 0 ]; then
    warn "数据库 $DB_NAME 中已有 $tables 张表"
    if confirm "跳过导入，直接使用库中现有的数据？（选否退出，请换一个空数据库）" y; then
      return
    fi
    exit 1
  fi
  prepare_dump
  info "正在导入 $(du -h "$SQL_FILE" | cut -f1) 的备份到 $DB_NAME，数据量大时需要几分钟……"
  db_exec --default-character-set=utf8mb4 "$DB_NAME" <"$SQL_FILE" || die "导入失败，请检查备份文件"
  rm -f "$SQL_FILE"
  ok "数据库导入完成"
}

do_migrate() {
  step "从原 v2board 迁移"
  cat <<EOF
迁移前请准备：
  1. 在原服务器上停止 v2board 的队列（horizon）与计划任务，等待 1～2 分钟后导出数据库
  2. 在本脚本所在目录（$SCRIPT_DIR）新建一个文件夹，例如 v2board，放入：
       数据库备份（宝塔导出的 .zip，或 .sql / .sql.gz）
       v2board.php（原站 config/v2board.php）
       .env（原站根目录的 .env）
  3. 在新服务器上创建一个空数据库（字符集 utf8mb4），原数据会导入到这个库
EOF
  local dir
  ask dir "迁移文件夹路径" "$SCRIPT_DIR/v2board"
  [[ "$dir" = /* ]] || dir="$SCRIPT_DIR/$dir"
  SRC_DIR=${dir%/}
  [ -d "$SRC_DIR" ] || die "文件夹不存在：$SRC_DIR"
  [ -f "$SRC_DIR/.env" ] || die "$SRC_DIR 中没有 .env"
  [ -f "$SRC_DIR/v2board.php" ] || die "$SRC_DIR 中没有 v2board.php"
  local app_key
  app_key=$(env_get "$SRC_DIR/.env" APP_KEY)
  [ -n "$app_key" ] || die ".env 中没有 APP_KEY"
  ok "已读取原站 APP_KEY"

  get_binary
  prepare_dir
  step "填写新数据库连接（原数据将导入到这个库）"
  ask_db mysql
  step "导入数据库备份"
  import_dump
  gen_config --app-key "$app_key" --migrated
  edit_config
  check_config

  step "升级表结构"
  gvb migrate --status || true
  confirm "执行以上迁移步骤？（只新增表和列，不修改原数据）" y || exit 1
  gvb migrate

  step "导入原站设置"
  gvb import-config -f "$SRC_DIR/v2board.php" --dry-run || true
  if confirm "导入以上设置？" y; then
    gvb import-config -f "$SRC_DIR/v2board.php"
  fi

  step "管理端地址"
  create_admin "原管理员邮箱（只显示管理端地址，不修改密码）" || true
  finish_guide
  cat <<EOF

迁移后：
  - 把域名解析到本服务器（或在原服务器上把站点改为反向代理），用户与节点无需改动
  - 用户需重新登录一次；原订阅链接继续可用
  - Stripe 等在支付平台固定填写回调地址的网关，改为管理端“支付配置”中显示的新地址
  - Telegram 机器人在“系统配置 → Telegram”中重新设置 Webhook
  - 迁移文件夹 $SRC_DIR 含全部用户数据与密钥，确认无误后请删除
EOF
}

# ---------- 升级 ----------

do_upgrade() {
  step "升级"
  ask INSTALL_DIR "Gvboard 安装目录" "$(default_dir)"
  INSTALL_DIR=${INSTALL_DIR%/}
  [ -x "$INSTALL_DIR/gvboard" ] && [ -f "$INSTALL_DIR/config.yaml" ] || die "$INSTALL_DIR 中没有 gvboard 或 config.yaml"
  info "当前版本：$("$INSTALL_DIR/gvboard" version 2>/dev/null | head -n 1)"
  get_binary
  info "新版本：$("$NEW_BINARY" version 2>/dev/null | head -n 1)"
  confirm "已备份数据库，继续升级？" y || exit 0
  local owner
  owner=$(stat -c %U:%G "$INSTALL_DIR/gvboard")
  cp -p "$INSTALL_DIR/gvboard" "$INSTALL_DIR/gvboard.bak"
  install -m 755 "$NEW_BINARY" "$INSTALL_DIR/gvboard"
  chown "$owner" "$INSTALL_DIR/gvboard" 2>/dev/null || true
  if ! gvb migrate; then
    mv -f "$INSTALL_DIR/gvboard.bak" "$INSTALL_DIR/gvboard"
    die "表结构升级失败，已恢复旧版本"
  fi
  ok "升级完成，旧版本保留为 $INSTALL_DIR/gvboard.bak"
  warn "请在进程守护管理器（或 systemctl restart gvboard）中重启 Gvboard"
}

# ---------- 入口 ----------

main() {
  [ "$(id -u)" -eq 0 ] || die "请使用 root 用户运行"
  detect_arch
  local action=${1:-}
  if [ -z "$action" ]; then
    cat <<EOF

${C_BOLD}Gvboard 部署脚本${C_RESET}
  1) 全新安装
  2) 从原 v2board 迁移
  3) 升级
  0) 退出
EOF
    local n
    ask n "请选择" 1
    case "$n" in 1) action=install ;; 2) action=migrate ;; 3) action=upgrade ;; *) exit 0 ;; esac
  fi
  case "$action" in
    install) do_install ;;
    migrate) do_migrate ;;
    upgrade) do_upgrade ;;
    *) die "未知操作：$action（install / migrate / upgrade）" ;;
  esac
}

main "$@"
