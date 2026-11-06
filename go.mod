module shizukubot-plugins

go 1.26.0

require shizukubot v0.0.0

// 本地开发时指向同级的 shizukubot 框架仓库，用于编译校验插件。
replace shizukubot => ../shizukubot
