// 每日签到送余额（Gvboard JS 插件示例）
//   POST {api}/plugin/daily_bonus/checkin   签到（需登录）
//   GET  {api}/plugin/daily_bonus/status    今天是否已签到、连续签到天数（需登录）
// 打包：把本目录中的 plugin.json 与 main.js 压缩为 zip，在管理端“插件”页面上传。

var DAY = 86400;

// 按服务器时区取日期，形如 2026-11-14
function dateKey(offsetDays) {
  var d = new Date(Date.now() + (offsetDays || 0) * DAY * 1000);
  var m = d.getMonth() + 1;
  var day = d.getDate();
  return d.getFullYear() + '-' + (m < 10 ? '0' : '') + m + '-' + (day < 10 ? '0' : '') + day;
}

function streakOf(userID) {
  var s = kv.get('streak:' + userID);
  if (!s) return { last: '', days: 0 };
  return JSON.parse(s);
}

function intConfig(key) {
  var v = Number(plugin.config()[key]);
  return isFinite(v) && v > 0 ? Math.floor(v) : 0;
}

plugin.route('user', 'POST', '/checkin', function (req) {
  var uid = req.user.id;
  var today = dateKey(0);
  // setnx 保证并发请求时同一天只能签到一次
  if (!kv.setnx('done:' + uid + ':' + today, '1', 2 * DAY)) {
    return plugin.fail(400, '今天已经签到过了');
  }
  var s = streakOf(uid);
  s.days = s.last === dateKey(-1) ? s.days + 1 : 1;
  s.last = today;
  kv.set('streak:' + uid, JSON.stringify(s), 3 * DAY);

  var reward = intConfig('reward');
  var bonus = 0;
  var need = intConfig('streak_days');
  if (need > 0 && s.days % need === 0) bonus = intConfig('streak_reward');
  var balance = users.addBalance(uid, reward + bonus);
  log.info('用户 ' + uid + ' 签到，连续 ' + s.days + ' 天，奖励 ' + (reward + bonus) + ' 分');
  return { reward: reward, bonus: bonus, streak: s.days, balance: balance };
});

plugin.route('user', 'GET', '/status', function (req) {
  var uid = req.user.id;
  var s = streakOf(uid);
  var checked = !!kv.get('done:' + uid + ':' + dateKey(0));
  var alive = s.last === dateKey(0) || s.last === dateKey(-1);
  return { checked_in: checked, streak: alive ? s.days : 0, reward: intConfig('reward') };
});
