#!/usr/bin/env bash
# Gvboard 交互式安装 / 迁移 / 升级脚本（Linux，需 root，支持宝塔面板）
#
#   bash install.sh            显示菜单
#   bash install.sh install    全新安装
#   bash install.sh migrate    从原 v2board 迁移（使用原数据库）
#   bash install.sh upgrade    升级到最新版本
#
# 环境变量（可选）：
#   GVB_REPO       GitHub 仓库（owner/name），发布时已自动填写
#   GVB_VERSION    安装的版本标签，如 v1.0.0，默认最新版本
#   GITHUB_PROXY   GitHub 下载加速前缀，如 https://ghfast.top/
#   GVB_BINARY     使用本地的 gvboard 可执行文件（不下载）
set -euo pipefail

REPO="${GVB_REPO:-__GVB_REPO__}"
VERSION="${GVB_VERSION:-latest}"
GITHUB_PROXY="${GITHUB_PROXY:-}"
SERVICE="gvboard"
UNIT_FILE="/etc/systemd/system/${SERVICE}.service"
SCRIPT_DIR=""
[ -f "${BASH_SOURCE[0]:-}" ] && SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR=""

# ---------- 输出与输入 ----------

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

# 交互输入从终端读取，脚本通过管道执行（curl ... | bash）时也能输入
TTY=/dev/tty
[ -r "$TTY" ] || die "需要在交互式终端中运行"

# ask 变量名 提示 [默认值]
ask() {
  local __var=$1 __prompt=$2 __def=${3:-} __val
  if [ -n "$__def" ]; then
    read -r -p "$__prompt [$__def]: " __val <"$TTY" || true
    __val=${__val:-$__def}
  else
    while :; do
      read -r -p "$__prompt: " __val <"$TTY" || true
      [ -n "$__val" ] && break
      warn "不能为空"
    done
  fi
  printf -v "$__var" '%s' "$__val"
}

# ask_secret 变量名 提示 [允许为空 1/0] [默认值]，输入不回显
ask_secret() {
  local __var=$1 __prompt=$2 __empty=${3:-0} __def=${4:-} __val __hint=""
  [ -n "$__def" ] && __hint="（直接回车使用已读取的值）"
  while :; do
    read -r -s -p "$__prompt$__hint: " __val <"$TTY" || true
    printf '\n'
    __val=${__val:-$__def}
    { [ -n "$__val" ] || [ "$__empty" = 1 ]; } && break
    warn "不能为空"
  done
  printf -v "$__var" '%s' "$__val"
}

# confirm 提示 [默认 y/n]，返回 0 表示是
confirm() {
  local __def=${2:-n} __val __hint="y/N"
  [ "$__def" = y ] && __hint="Y/n"
  read -r -p "$1 [$__hint]: " __val <"$TTY" || true
  __val=${__val:-$__def}
  [[ "$__val" =~ ^[Yy] ]]
}

pause() { read -r -p "$1（完成后按回车继续）" _ <"$TTY" || true; }

cleanup() { [ -n "$TMP_DIR" ] && rm -rf "$TMP_DIR"; }
trap cleanup EXIT

# ---------- 环境检测 ----------

require_root() { [ "$(id -u)" -eq 0 ] || die "请使用 root 用户运行（sudo bash $0）"; }

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) ARCH=amd64 ;;
    aarch64 | arm64) ARCH=arm64 ;;
    *) die "不支持的 CPU 架构：$(uname -m)（支持 x86_64、aarch64）" ;;
  esac
}

is_bt() { [ -d /www/server/panel ]; }

has_systemd() { command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; }

default_dir() { if is_bt; then echo /www/gvboard; else echo /opt/gvboard; fi; }

# 运行用户：宝塔使用 www，其它系统创建 gvboard 系统用户
ensure_user() {
  if is_bt && id www >/dev/null 2>&1; then
    RUN_USER=www
  else
    RUN_USER=gvboard
    if ! id "$RUN_USER" >/dev/null 2>&1; then
      useradd -r -s /usr/sbin/nologin -d "$INSTALL_DIR" "$RUN_USER" 2>/dev/null ||
        useradd -r -s /sbin/nologin "$RUN_USER" ||
        die "创建用户 $RUN_USER 失败"
    fi
  fi
}

port_in_use() {
  if command -v ss >/dev/null 2>&1; then
    ss -ltn 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]$1\$"
  else
    return 1
  fi
}

# ---------- 下载 ----------

fetch() { # fetch URL 输出文件
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 3 --connect-timeout 15 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    die "需要 curl 或 wget"
  fi
}

# 准备可执行文件，结果路径写入 NEW_BINARY
get_binary() {
  TMP_DIR=$(mktemp -d)
  local local_bin="${GVB_BINARY:-}"
  if [ -z "$local_bin" ] && [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/gvboard" ] && [ "$SCRIPT_DIR" != "${INSTALL_DIR:-}" ]; then
    local_bin="$SCRIPT_DIR/gvboard"
  fi
  if [ -n "$local_bin" ]; then
    [ -f "$local_bin" ] || die "找不到 $local_bin"
    info "使用本地程序：$local_bin"
    cp "$local_bin" "$TMP_DIR/gvboard"
  else
    [[ "$REPO" == *__GVB_REPO__* ]] && die "未设置下载仓库：请把 gvboard 可执行文件放在脚本同一目录，或设置环境变量 GVB_REPO=owner/name"
    local base="https://github.com/$REPO/releases"
    if [ "$VERSION" = latest ]; then base="$base/latest/download"; else base="$base/download/$VERSION"; fi
    local name="gvboard_linux_${ARCH}.tar.gz"
    info "下载 ${GITHUB_PROXY}${base}/${name}"
    fetch "${GITHUB_PROXY}${base}/${name}" "$TMP_DIR/$name" || die "下载失败，可设置 GITHUB_PROXY 加速，或手动下载后用 GVB_BINARY 指定"
    if fetch "${GITHUB_PROXY}${base}/sha256sums.txt" "$TMP_DIR/sha256sums.txt" 2>/dev/null; then
      (cd "$TMP_DIR" && grep " ${name}\$" sha256sums.txt | sha256sum -c - >/dev/null) || die "文件校验失败（sha256 不一致），请重试"
      ok "文件校验通过"
    else
      warn "未能下载校验文件，跳过校验"
    fi
    tar -xzf "$TMP_DIR/$name" -C "$TMP_DIR" gvboard || die "解压失败"
  fi
  chmod 755 "$TMP_DIR/gvboard"
  "$TMP_DIR/gvboard" version >/dev/null 2>&1 || die "程序无法运行（架构不匹配或文件损坏）"
  NEW_BINARY="$TMP_DIR/gvboard"
  info "版本：$("$NEW_BINARY" version 2>/dev/null | head -n 1)"
}

# ---------- 配置 ----------

gvb() { (cd "$INSTALL_DIR" && ./gvboard "$@"); }

ask_install_dir() {
  ask INSTALL_DIR "安装目录（不要放在网站根目录下）" "$(default_dir)"
  if [ -f "$INSTALL_DIR/config.yaml" ]; then
    warn "$INSTALL_DIR 中已有 config.yaml"
    confirm "覆盖已有配置？（选否则退出，升级请使用 upgrade）" n || exit 0
  fi
  mkdir -p "$INSTALL_DIR"
  install -m 755 "$NEW_BINARY" "$INSTALL_DIR/gvboard"
}

ask_listen() {
  local port
  while :; do
    ask port "Gvboard 监听端口（只监听本机，由 Nginx 反向代理）" "${1:-8080}"
    [[ "$port" =~ ^[0-9]+$ ]] && [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || { warn "端口无效"; continue; }
    if port_in_use "$port"; then warn "端口 $port 已被占用"; continue; fi
    break
  done
  PORT=$port
}

ask_database() { # ask_database [是否迁移 1/0]
  if [ "${1:-0}" = 1 ]; then
    DB_DRIVER=mysql
  else
    ask DB_DRIVER "数据库类型 mysql / postgres" "${DB_DRIVER:-mysql}"
    [[ "$DB_DRIVER" =~ ^(mysql|postgres)$ ]] || die "数据库类型只能是 mysql 或 postgres"
  fi
  local def_port=3306
  [ "$DB_DRIVER" = postgres ] && def_port=5432
  ask DB_HOST "数据库地址" "${DB_HOST:-127.0.0.1}"
  ask DB_PORT "数据库端口" "${DB_PORT:-$def_port}"
  ask DB_NAME "数据库名" "${DB_NAME:-}"
  ask DB_USER "数据库用户名" "${DB_USER:-}"
  ask_secret DB_PASS "数据库密码" 0 "${DB_PASS:-}"
}

ask_redis() { # ask_redis [默认库号]
  ask REDIS_ADDR "Redis 地址" "${REDIS_ADDR:-127.0.0.1:6379}"
  ask_secret REDIS_PASS "Redis 密码（没有设置直接回车）" 1 "${REDIS_PASS:-}"
  ask REDIS_DB "Redis 库号（0～15）" "${1:-0}"
}

# write_config [额外的 init 参数...]
write_config() {
  rm -f "$INSTALL_DIR/config.yaml"
  (cd "$INSTALL_DIR" && GVB_DATABASE_PASSWORD="$DB_PASS" GVB_REDIS_PASSWORD="$REDIS_PASS" ./gvboard init \
    --db "$DB_DRIVER" --db-host "$DB_HOST" --db-port "$DB_PORT" --db-name "$DB_NAME" --db-user "$DB_USER" \
    --redis-addr "$REDIS_ADDR" --redis-db "$REDIS_DB" --listen "127.0.0.1:$PORT" "$@" >/dev/null)
  chmod 600 "$INSTALL_DIR/config.yaml"
}

# 写入配置并检查数据库连接，失败时重新输入
configure() { # configure 是否迁移 [额外的 init 参数...]
  local migrated=$1; shift
  while :; do
    write_config "$@"
    info "检查数据库与 Redis 连接……"
    if gvb migrate --status >/dev/null 2>&1; then
      ok "连接正常"
      return
    fi
    warn "连接失败，错误信息："
    gvb migrate --status 2>&1 | tail -n 5 || true
    confirm "重新输入数据库与 Redis 信息？（选否退出）" y || exit 1
    ask_database "$migrated"
    ask_redis "$REDIS_DB"
  done
}

# ---------- 服务 ----------

install_service() {
  chown -R "$RUN_USER":"$RUN_USER" "$INSTALL_DIR"
  chmod 600 "$INSTALL_DIR/config.yaml"
  if ! has_systemd; then
    warn "系统没有 systemd，请自行配置进程守护（宝塔：进程守护管理器）："
    echo "  运行目录：$INSTALL_DIR"
    echo "  启动命令：$INSTALL_DIR/gvboard serve"
    echo "  启动用户：$RUN_USER"
    return
  fi
  cat >"$UNIT_FILE" <<EOF
[Unit]
Description=Gvboard
After=network.target mysql.service mysqld.service mariadb.service postgresql.service redis.service redis-server.service

[Service]
User=$RUN_USER
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/gvboard serve
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable "$SERVICE" >/dev/null 2>&1
  systemctl restart "$SERVICE"
  health_check
}

health_check() {
  local port code i
  port=$(grep -E '^[[:space:]]*listen:' "$INSTALL_DIR/config.yaml" | head -n 1 | sed -E 's/.*:([0-9]+)"?[[:space:]]*$/\1/')
  for i in $(seq 1 15); do
    if command -v curl >/dev/null 2>&1; then
      code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/" 2>/dev/null || true)
    else
      code=000
      port_in_use "$port" && systemctl is-active --quiet "$SERVICE" && code=200
    fi
    if [ -n "$code" ] && [ "$code" != 000 ]; then
      ok "Gvboard 已启动（端口 $port）"
      return 0
    fi
    sleep 1
  done
  warn "Gvboard 未能启动，最近的日志："
  journalctl -u "$SERVICE" -n 20 --no-pager 2>/dev/null || true
  return 1
}

proxy_guide() {
  step "配置网站反向代理"
  if is_bt; then
    cat <<EOF
在宝塔面板中操作：
  1. 网站 → 添加站点（已有站点则打开其“设置”）：PHP 版本选“纯静态”
  2. 设置 → 伪静态：清空内容并保存
  3. 设置 → SSL：申请 Let's Encrypt 证书并开启“强制 HTTPS”
  4. 设置 → 反向代理 → 添加反向代理：目标 URL 填 ${C_BOLD}http://127.0.0.1:$PORT${C_RESET}，发送域名 \$host，不要开启缓存
  5. 防火墙只放行 80、443，不要放行 $PORT
EOF
  elif command -v nginx >/dev/null 2>&1 && [ -d /etc/nginx/conf.d ] && confirm "检测到 Nginx，是否生成反向代理配置？" y; then
    local domain
    ask domain "网站域名（多个用空格分隔）"
    cat >/etc/nginx/conf.d/gvboard.conf <<EOF
server {
    listen 80;
    server_name $domain;
    client_max_body_size 20m;
    location / {
        proxy_pass http://127.0.0.1:$PORT;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
EOF
    if nginx -t >/dev/null 2>&1; then
      systemctl reload nginx 2>/dev/null || nginx -s reload
      ok "已写入 /etc/nginx/conf.d/gvboard.conf 并重新加载 Nginx"
      echo "  HTTPS 证书可使用：certbot --nginx -d 你的域名"
    else
      warn "Nginx 配置检查未通过，已保留文件 /etc/nginx/conf.d/gvboard.conf，请手动修改：nginx -t"
    fi
  else
    echo "请把网站反向代理到 http://127.0.0.1:$PORT（示例见 deploy/nginx/embedded.conf）"
  fi
}

# ---------- 全新安装 ----------

do_install() {
  step "全新安装 Gvboard"
  get_binary
  ask_install_dir
  ensure_user
  step "数据库（请先在宝塔“数据库”或命令行中创建空数据库，字符集 utf8mb4）"
  ask_database 0
  step "Redis"
  ask_redis 0
  step "监听端口"
  ask_listen
  configure 0
  step "创建数据表"
  gvb migrate
  step "创建管理员"
  local email
  ask email "管理员邮箱"
  gvb create-admin "$email"
  warn "请记下上面的管理员密码与管理端地址（只显示一次）"
  step "启动服务"
  install_service || true
  proxy_guide
  step "安装完成"
  cat <<EOF
程序目录：$INSTALL_DIR（配置文件 config.yaml，请妥善备份）
常用命令：
  systemctl status|restart|stop $SERVICE     查看 / 重启 / 停止
  journalctl -u $SERVICE -f                    查看日志
  cd $INSTALL_DIR && ./gvboard create-admin 邮箱   重置管理员 / 查看管理端地址
登录管理端后，先在“系统配置”中填写站点网址、节点通信密钥、邮件等设置。
EOF
}

# ---------- 从原 v2board 迁移 ----------

# env_get 文件 键：读取 .env 中的值（去掉引号与行尾回车，null 视为空）
env_get() {
  local v
  v=$(grep -E "^[[:space:]]*$2[[:space:]]*=" "$1" 2>/dev/null | tail -n 1 | cut -d= -f2- | sed -E 's/\r$//; s/^[[:space:]]+//; s/[[:space:]]+$//') || true
  v=${v#\"}; v=${v%\"}; v=${v#\'}; v=${v%\'}
  [ "$v" = null ] && v=""
  printf '%s' "$v"
}

find_v2board() {
  local d candidates=()
  for d in /www/wwwroot/* /var/www/* /home/*/* /home/wwwroot/* /opt/*; do
    [ -f "$d/artisan" ] && [ -f "$d/.env" ] && [ -f "$d/config/v2board.php" ] && candidates+=("$d")
  done
  if [ ${#candidates[@]} -gt 0 ]; then
    info "找到以下 v2board 站点："
    local i=1
    for d in "${candidates[@]}"; do echo "  $i) $d"; i=$((i + 1)); done
    local n
    ask n "选择序号，或直接输入站点目录" 1
    if [[ "$n" =~ ^[0-9]+$ ]] && [ "$n" -ge 1 ] && [ "$n" -le ${#candidates[@]} ]; then
      V2B_DIR=${candidates[$((n - 1))]}
    else
      V2B_DIR=$n
    fi
  else
    ask V2B_DIR "原 v2board 站点目录（包含 .env 与 artisan 的目录）"
  fi
  V2B_DIR=${V2B_DIR%/}
  [ -f "$V2B_DIR/.env" ] && [ -f "$V2B_DIR/config/v2board.php" ] || die "$V2B_DIR 中没有 .env 或 config/v2board.php"
}

supervisorctl_cmds() {
  # 输出可用的 supervisorctl 调用方式（系统自带、宝塔进程守护管理器）
  if command -v supervisorctl >/dev/null 2>&1; then echo "supervisorctl"; fi
  local bt_ctl=/www/server/panel/pyenv/bin/supervisorctl bt_conf
  for bt_conf in /www/server/panel/plugin/supervisor/supervisor.conf /etc/supervisor/supervisord.conf; do
    [ -x "$bt_ctl" ] && [ -f "$bt_conf" ] && echo "$bt_ctl -c $bt_conf"
  done
}

stop_old_queue() {
  local ctl name stopped=0
  while read -r ctl; do
    [ -z "$ctl" ] && continue
    while read -r name; do
      [ -z "$name" ] && continue
      if confirm "停止进程守护中的 $name ？" y; then
        $ctl stop "$name" >/dev/null 2>&1 && { ok "已停止 $name"; stopped=1; } || warn "停止 $name 失败"
      fi
    done < <($ctl status 2>/dev/null | grep -Ei 'horizon|queue|v2board' | awk '{print $1}')
  done < <(supervisorctl_cmds)
  while pgrep -f 'artisan (horizon|queue:work)' >/dev/null 2>&1; do
    warn "原队列进程仍在运行：$(pgrep -af 'artisan (horizon|queue:work)' | head -n 3 | tr '\n' ' ')"
    echo "  请在宝塔“进程守护管理器”（或 supervisor）中停止 v2board 的 horizon / queue 进程，"
    echo "  只结束进程会被自动拉起。"
    confirm "已经停止，重新检查？（选否则忽略继续）" y || break
  done
  [ "$stopped" = 1 ] && info "以后如需回退，可在进程守护中重新启动这些进程"
  return 0
}

disable_old_cron() {
  local u tab tmp changed file line
  for u in root www; do
    id "$u" >/dev/null 2>&1 || continue
    tab=$(crontab -l -u "$u" 2>/dev/null) || continue
    tmp=$(mktemp)
    changed=0
    while IFS= read -r line; do
      local hit=0
      if [[ "$line" != \#* ]]; then
        if [[ "$line" == *"schedule:run"* ]]; then
          hit=1
        else
          # 宝塔计划任务：crontab 中执行的是 /www/server/cron/ 下的脚本文件
          for file in $(grep -oE '/www/server/cron/[A-Za-z0-9_]+' <<<"$line" || true); do
            grep -q "schedule:run" "$file" 2>/dev/null && hit=1
          done
        fi
      fi
      if [ "$hit" = 1 ]; then
        echo "#gvboard-disabled# $line" >>"$tmp"
        changed=1
      else
        echo "$line" >>"$tmp"
      fi
    done <<<"$tab"
    if [ "$changed" = 1 ]; then
      printf '%s\n' "$tab" >"$BACKUP_DIR/crontab-$u.bak"
      crontab -u "$u" "$tmp"
      ok "已停用用户 $u 的 v2board 计划任务（原 crontab 备份在 $BACKUP_DIR/crontab-$u.bak）"
      is_bt && warn "宝塔“计划任务”页面中仍会显示该任务，请在面板中把它停用或删除，避免以后被面板恢复"
    fi
    rm -f "$tmp"
  done
}

backup_database() {
  local dump file
  dump=$(command -v mysqldump || true)
  [ -z "$dump" ] && [ -x /www/server/mysql/bin/mysqldump ] && dump=/www/server/mysql/bin/mysqldump
  file="$BACKUP_DIR/v2board-$(date +%Y%m%d-%H%M%S).sql.gz"
  if [ -z "$dump" ]; then
    warn "找不到 mysqldump，无法自动备份"
    pause "请先在宝塔“数据库”中备份 $DB_NAME 并下载到本地"
    return
  fi
  info "正在备份数据库 $DB_NAME ……"
  if MYSQL_PWD="$DB_PASS" "$dump" --single-transaction --quick --routines --triggers --default-character-set=utf8mb4 \
    -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" "$DB_NAME" | gzip >"$file" && [ "${PIPESTATUS[0]}" = 0 ]; then
    chmod 600 "$file"
    ok "数据库已备份：$file（$(du -h "$file" | cut -f1)），含全部用户数据，请下载保存，不要公开"
    BACKUP_FILE=$file
  else
    rm -f "$file"
    warn "备份失败（检查数据库账号是否有权限）"
    confirm "不备份继续迁移？（强烈不建议）" n || exit 1
  fi
}

do_migrate() {
  step "从原 v2board 迁移到 Gvboard"
  cat <<EOF
迁移会直接使用原数据库（只新增表和列，不修改、不删除原有数据），流程：
  读取原站配置 → 停止原队列与计划任务 → 备份数据库 → 安装 Gvboard → 升级表结构
  → 导入原站设置 → 启动 → 切换网站到 Gvboard
迁移期间请暂停新订单。用户登录状态会失效（需重新登录），订阅链接与节点不受影响。
EOF
  confirm "开始迁移？" y || exit 0

  step "1/8 读取原站配置"
  find_v2board
  local env="$V2B_DIR/.env" app_key
  app_key=$(env_get "$env" APP_KEY)
  [ -n "$app_key" ] || die "$env 中没有 APP_KEY"
  DB_HOST=$(env_get "$env" DB_HOST); DB_PORT=$(env_get "$env" DB_PORT)
  DB_NAME=$(env_get "$env" DB_DATABASE); DB_USER=$(env_get "$env" DB_USERNAME); DB_PASS=$(env_get "$env" DB_PASSWORD)
  local rh rp
  rh=$(env_get "$env" REDIS_HOST); rp=$(env_get "$env" REDIS_PORT)
  REDIS_ADDR="${rh:-127.0.0.1}:${rp:-6379}"; REDIS_PASS=$(env_get "$env" REDIS_PASSWORD)
  DB_HOST=${DB_HOST:-127.0.0.1}; DB_PORT=${DB_PORT:-3306}
  [ "$DB_HOST" = localhost ] && DB_HOST=127.0.0.1
  echo "  站点目录：$V2B_DIR"
  echo "  数据库：  ${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME}（密码已读取）"
  echo "  Redis：   $REDIS_ADDR"
  echo "  APP_KEY： 已读取（用于保持 Shadowsocks 2022 节点密钥不变）"
  if ! confirm "以上信息正确？（选否则逐项修改）" y; then
    ask_database 1
    ask_redis 5
  fi

  BACKUP_DIR="/root/gvboard-migrate-backup-$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$BACKUP_DIR"
  chmod 700 "$BACKUP_DIR"
  cp -p "$env" "$BACKUP_DIR/v2board.env"
  cp -p "$V2B_DIR/config/v2board.php" "$BACKUP_DIR/v2board.php"

  step "2/8 停止原面板的队列与计划任务"
  stop_old_queue
  disable_old_cron
  warn "建议现在暂停原站点（宝塔：网站 → 停用），避免迁移期间产生新订单"
  pause "确认后"
  info "等待 90 秒，让原面板把 Redis 中的流量数据写入数据库（输入回车跳过）"
  read -r -t 90 _ <"$TTY" || true

  step "3/8 备份数据库"
  backup_database

  step "4/8 安装 Gvboard"
  get_binary
  ask_install_dir
  ensure_user
  ask REDIS_DB "Gvboard 使用的 Redis 库号（建议用原站未使用的库号）" "${REDIS_DB:-5}"
  ask_listen
  configure 1 --app-key "$app_key" --migrated

  step "5/8 升级表结构"
  gvb migrate --status || true
  confirm "执行以上迁移步骤？" y || exit 1
  gvb migrate

  step "6/8 导入原站设置（config/v2board.php）"
  gvb import-config -f "$BACKUP_DIR/v2board.php" --dry-run || true
  if confirm "导入以上设置？" y; then
    gvb import-config -f "$BACKUP_DIR/v2board.php"
  fi
  local email
  ask email "原管理员邮箱（用于显示管理端地址，不会修改密码）"
  gvb create-admin "$email" || true

  step "7/8 启动 Gvboard"
  install_service || true

  step "8/8 切换网站"
  if is_bt; then
    cat <<EOF
在宝塔中打开原站点的“设置”：
  1. 如果刚才停用了站点，先“启用”
  2. 伪静态：清空内容并保存（原 Laravel 规则会与反向代理冲突）
  3. 网站目录 / PHP 版本：PHP 版本改为“纯静态”
  4. 反向代理 → 添加反向代理：目标 URL 填 ${C_BOLD}http://127.0.0.1:$PORT${C_RESET}，发送域名 \$host，不要开启缓存
  域名与 SSL 证书不需要改动。
EOF
  else
    echo "把原站点的 Nginx 配置改为反向代理到 http://127.0.0.1:$PORT（示例见 deploy/nginx/embedded.conf），去掉 PHP 与伪静态规则。"
  fi
  pause "切换完成后"

  step "迁移完成"
  cat <<EOF
请检查：
  1. 原用户用原密码登录，订阅、流量、到期时间正确
  2. 原订阅链接能获取节点
  3. 几分钟后管理端“节点管理”显示节点在线、流量有增长
  4. 管理端“系统状态”中定时任务与队列为运行中
  5. Stripe 等在支付平台固定配置 Webhook 的网关，改为管理端“支付配置”中显示的新回调地址
  6. Telegram 机器人在“系统配置 → Telegram”中重新设置 Webhook

备份目录：$BACKUP_DIR（数据库、原 .env、v2board.php、crontab）
如需回退到原面板：
  systemctl stop $SERVICE && systemctl disable $SERVICE
EOF
  [ -n "${BACKUP_FILE:-}" ] && echo "  gunzip -c $BACKUP_FILE | mysql -h $DB_HOST -P $DB_PORT -u $DB_USER -p $DB_NAME"
  cat <<EOF
  恢复 crontab：crontab -u root $BACKUP_DIR/crontab-root.bak（存在时）
  在宝塔中恢复原站点的 PHP 版本与伪静态、删除反向代理，并在进程守护中启动 horizon
EOF
}

# ---------- 升级 ----------

do_upgrade() {
  step "升级 Gvboard"
  local dir=""
  [ -f "$UNIT_FILE" ] && dir=$(grep -E '^WorkingDirectory=' "$UNIT_FILE" | cut -d= -f2-)
  ask INSTALL_DIR "Gvboard 安装目录" "${dir:-$(default_dir)}"
  [ -x "$INSTALL_DIR/gvboard" ] && [ -f "$INSTALL_DIR/config.yaml" ] || die "$INSTALL_DIR 中没有 gvboard 或 config.yaml"
  info "当前版本：$("$INSTALL_DIR/gvboard" version 2>/dev/null | head -n 1)"
  get_binary
  warn "升级前请先备份数据库（宝塔“数据库”→ 备份，或 mysqldump）"
  confirm "已备份，继续升级？" y || exit 0
  local owner
  owner=$(stat -c %U "$INSTALL_DIR/gvboard")
  has_systemd && [ -f "$UNIT_FILE" ] && systemctl stop "$SERVICE"
  cp -p "$INSTALL_DIR/gvboard" "$INSTALL_DIR/gvboard.bak"
  install -m 755 "$NEW_BINARY" "$INSTALL_DIR/gvboard"
  chown "$owner" "$INSTALL_DIR/gvboard" 2>/dev/null || true
  if ! gvb migrate; then
    warn "表结构升级失败，恢复旧版本"
    mv -f "$INSTALL_DIR/gvboard.bak" "$INSTALL_DIR/gvboard"
    has_systemd && [ -f "$UNIT_FILE" ] && systemctl start "$SERVICE"
    die "升级失败，已恢复旧版本"
  fi
  if has_systemd && [ -f "$UNIT_FILE" ]; then
    systemctl start "$SERVICE"
    if health_check; then
      ok "升级完成，旧版本保留为 $INSTALL_DIR/gvboard.bak"
    else
      warn "新版本启动失败，可执行以下命令恢复旧版本："
      echo "  mv -f $INSTALL_DIR/gvboard.bak $INSTALL_DIR/gvboard && systemctl restart $SERVICE"
    fi
  else
    ok "程序已替换，请在进程守护管理器中重启 gvboard"
  fi
}

# ---------- 入口 ----------

main() {
  require_root
  detect_arch
  is_bt && info "检测到宝塔面板"
  local action=${1:-}
  if [ -z "$action" ]; then
    cat <<EOF

${C_BOLD}Gvboard 部署脚本${C_RESET}
  1) 全新安装
  2) 从原 v2board 迁移（使用原数据库）
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
