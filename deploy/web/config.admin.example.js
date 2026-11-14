// 分离部署时管理端的运行配置：复制为管理端站点根目录下的 config.js。
// 其中包含安全路径，请限制管理端站点的访问来源（见 nginx/separated.conf）。
window.APP_CONFIG = {
  // 原生 API 地址 = API 域名 + config.yaml 中的 api.prefix
  api: 'https://api.example.com/api/xxxxxxxxxx',
  // 管理端安全路径（系统配置 secure_path，gvboard create-admin 会显示）
  secure_path: 'xxxxxxxxxxxxxxxx',
  site: { name: '', logo: '' },
  theme: {
    sidebar: 'light', // light / dark
    header: 'dark', // light / dark
    color: 'default', // default / darkblue / black / green
    background_url: '',
  },
}
