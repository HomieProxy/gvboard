// 订单与工单通知（Gvboard JS 插件示例）
// 演示：读取配置、订阅事件、调用外部接口（只能访问 plugin.json 中 http 声明的域名）、启用回调。
// 打包：把本目录中的 plugin.json 与 main.js 压缩为 zip，在管理端“插件”页面上传。
//
// 事件处理函数抛出异常时，队列最多重试 3 次，因此推送失败可能收到重复消息。

var PERIODS = {
  month_price: '月付', quarter_price: '季付', half_year_price: '半年付', year_price: '年付',
  two_year_price: '两年付', three_year_price: '三年付', onetime_price: '一次性', reset_price: '流量重置包',
  deposit: '充值'
};
var ORDER_TYPES = { 1: '新购', 2: '续费', 3: '升级', 4: '流量重置包', 9: '充值' };

function money(cents) {
  return (Number(cents || 0) / 100).toFixed(2);
}

function emailOf(userID) {
  var u = users.get(userID);
  return u ? u.email : '用户 #' + userID;
}

// 检查推送平台的返回结果，失败时抛出异常（启用时提示管理员，事件中触发重试）
function check(resp) {
  var body = null;
  try { body = resp.json(); } catch (e) { /* 非 JSON 返回 */ }
  if (resp.status >= 300 || !body) {
    throw new Error('推送失败：HTTP ' + resp.status + ' ' + String(resp.body).slice(0, 200));
  }
  if (body.ok === false) throw new Error('Telegram 推送失败：' + body.description);
  if (body.errcode) throw new Error('机器人推送失败：' + body.errmsg);
  if (body.code !== undefined && body.code !== 200 && body.code !== 0) throw new Error('Bark 推送失败：' + body.message);
}

function send(title, lines) {
  var c = plugin.config();
  var text = '【通知】' + title + '\n' + lines.join('\n');
  switch (c.channel) {
    case 'telegram':
      if (!c.telegram_token || !c.telegram_chat_id) throw new Error('请填写 Telegram 机器人 Token 与 Chat ID');
      return check(http.post('https://api.telegram.org/bot' + c.telegram_token + '/sendMessage',
        { chat_id: c.telegram_chat_id, text: text }, { timeout: 10000 }));
    case 'wecom':
    case 'dingtalk':
      if (!c.webhook_url) throw new Error('请填写机器人 Webhook 地址');
      return check(http.post(c.webhook_url, { msgtype: 'text', text: { content: text } }, { timeout: 10000 }));
    case 'bark':
      if (!c.bark_key) throw new Error('请填写 Bark 设备 Key');
      return check(http.post('https://api.day.app/' + encodeURIComponent(c.bark_key),
        { title: '【通知】' + title, body: lines.join('\n'), group: 'Gvboard' }, { timeout: 10000 }));
    default:
      throw new Error('未知推送渠道：' + c.channel);
  }
}

// 启用前发送测试消息，配置有误时抛出异常，插件不会被启用
plugin.onEnable(function () {
  send('插件已启用', ['如果你收到这条消息，说明推送配置正确。']);
});

plugin.on('order.paid', function (ev) {
  if (!plugin.config().notify_paid) return;
  var o = ev.data;
  send('用户付款', [
    '用户：' + emailOf(o.user_id),
    '订单号：' + o.trade_no,
    '类型：' + (ORDER_TYPES[o.type] || o.type) + (o.plan_id ? '（套餐 #' + o.plan_id + '，' + (PERIODS[o.period] || o.period) + '）' : ''),
    '实付：' + money(o.total_amount) + (o.balance_amount ? '，余额抵扣：' + money(o.balance_amount) : '')
  ]);
});

plugin.on('ticket.created', function (ev) {
  if (!plugin.config().notify_ticket) return;
  var t = ev.data;
  send(t.kind === 'withdraw' ? '新的提现申请' : '新工单', [
    '用户：' + emailOf(t.user_id),
    '主题：' + t.subject,
    '内容：' + String(t.message || '').slice(0, 300)
  ]);
});

plugin.on('user.registered', function (ev) {
  if (!plugin.config().notify_register) return;
  var u = ev.data;
  send('新用户注册', ['邮箱：' + u.email, u.invite_user_id ? '邀请人：' + emailOf(u.invite_user_id) : '无邀请人']);
});
