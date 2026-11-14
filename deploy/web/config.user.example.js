// 分离部署时用户端的运行配置：复制为用户端站点根目录下的 config.js。
// 站点名称、注册设置等从 API 的 /site 接口获取，不需要在这里填写。
window.APP_CONFIG = {
  // 原生 API 地址 = API 域名 + config.yaml 中的 api.prefix
  api: 'https://api.example.com/api/xxxxxxxxxx',
  theme: {
    id: 'default',
    // 与默认主题 theme.json 中的配置项相同
    config: {
      theme_color: 'default', // default / green / black / darkblue
      dark_mode: 'auto', // auto / light / dark
      background_url: '',
      custom_html: '',
    },
  },
}
