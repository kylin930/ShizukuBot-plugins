<div align="center">

<img src="shizukubot.svg" alt="ShizukuBot" width="120">

# ShizukuBot-plugins

**ShizukuBot 的插件合集**

93 个原生 Go 插件 · 9 个分类 · 移植自 ZeroBot-Plugin / NanoBot-Plugin / Koishi / AstrBot / Yunzai

<p>
  <img src="https://img.shields.io/badge/Go-1.26.1-00ADD8?logo=go&logoColor=white" alt="Go 1.26.1">
  <img src="https://img.shields.io/badge/%E6%8F%92%E4%BB%B6-93%20%E4%B8%AA-2EA043" alt="93 个插件">
  <img src="https://img.shields.io/badge/%E5%88%86%E7%B1%BB-9-8957E5" alt="9 个分类">
  <img src="https://img.shields.io/badge/License-Apache--2.0-blue" alt="Apache-2.0">
</p>

[框架仓库](https://github.com/kylin930/ShizukuBot) · [插件开发](https://github.com/kylin930/ShizukuBot/blob/main/docs/plugin-development.md) · [插件 SDK](https://github.com/kylin930/ShizukuBot/blob/main/docs/plugin-sdk.md) · [移植对照检查报告](porting-audit.md)

</div>

## 简介

本仓库是 [ShizukuBot](https://github.com/kylin930/ShizukuBot) 的插件合集。插件来自 ZeroBot-Plugin、NanoBot-Plugin、Koishi（赏雪合集）、AstrBot、Yunzai 等生态，去重后逐个移植为框架原生的 Go 插件包，统一使用 `shizukubot/sdk`，可以随框架一起编译、测试和部署。

- `plugins/`：已移植、可直接编译的 Go 插件（93 个）
- `sources/`：上游原始源码，仅本地保留作移植参考，不参与编译，不纳入仓库
- `internal/`：插件测试与指令注册辅助包

插件没有在线安装或热重载。它和框架一起在编译期注册，通过 WebUI 启用、配置与重启。

## 仓库结构

```text
ShizukuBot-plugins/
├── plugins/
│   ├── admin/          # 群管 / 审核 / 撤回 / 自检（已移植 5）
│   ├── ai/             # 大模型对话 / 群聊总结（已移植 1）
│   ├── audio/          # 语音合成 / 点歌（已移植 5）
│   ├── chat/           # 人格与词典回复（已移植 3）
│   ├── entertainment/  # 整活 / 占卜 / 投胎 / 老婆库（已移植 28）
│   ├── game/           # 棋牌 / 猜谜 / 群经济 / 终末地（已移植 8）
│   ├── image/          # 图片生成 / 处理（已移植 2）
│   ├── query/          # 查询 / 订阅 / 解析（已移植 24）
│   └── tool/           # 加解密 / 翻译 / 图床 / 图像特效（已移植 17）
├── internal/
│   ├── plugintest/     # 插件测试辅助
│   └── triggercmd/     # 触发指令注册辅助
├── sources/            # 上游源码（本地参考，已被 .gitignore 排除）
├── go.mod              # replace shizukubot => ../shizukubot
└── porting-audit.md
```

## 快速使用

需要 Go **1.26+**，并把框架仓库与本仓库放在同级目录：

```text
你的工作目录/
├── shizukubot/          # 框架仓库（目录名需为 shizukubot）
└── ShizukuBot-plugins/  # 本仓库
```

1. 选择插件目录，复制到框架的 `plugins/` 下，例如：

   ```text
   plugins/entertainment/wife  →  ../shizukubot/plugins/wife
   ```

   也可以保留分类层级（`../shizukubot/plugins/entertainment/wife`），注册表会递归扫描。

2. 确认包名与导入路径合法：目录名会成为导入路径的一部分，包名必须是合法 Go 标识符。本仓库统一使用去掉连字符的小写名，例如 `verse-match` → `package versematch`。

3. 确认插件 `Meta.ID` 在框架内唯一。

4. 生成注册表、测试并构建：

   ```sh
   cd ../shizukubot
   go generate ./pluginregistry
   go test ./sdk ./plugins/... ./pluginregistry
   go build -o shizukubot .
   ```

5. 重启框架，在 WebUI“插件”页选择 Bot 并启用。配置、启停与重启不需要重新编译；修改插件源码后需要重新生成注册表并构建。

本仓库的 `go.mod` 通过 `replace shizukubot => ../shizukubot` 指向同级框架，因此也可以直接在本仓库运行测试：

```powershell
cd d:\ShizukuBot-plugins
go test ./plugins/...
```

## 插件总览

| 分类 | 数量 | 说明 |
| --- | ---: | --- |
| `admin` | 5 | 违禁词、复读打断、睡眠管理、系统自检、群管与撤回 |
| `ai` | 1 | 大模型聊天与群聊总结 |
| `audio` | 5 | 语音合成、点歌、MIDI 与到账语音 |
| `chat` | 3 | 人格回复、词典式聊天、群空调 |
| `entertainment` | 28 | 抽签、运势、塔罗、投胎、老婆库、签到钱包等 |
| `game` | 8 | 终末地、王者营地、猜歌、猜成语、猜词、钓鱼、牛牛、扑克 |
| `image` | 2 | 头像 GIF 制图与 Real-CUGAN 超分 |
| `query` | 24 | 新闻、B 站、GitHub、Steam、RSS、天气、游戏与番剧查询 |
| `tool` | 17 | 加解密、图床、二维码、翻译、字体渲染、图片保存等 |
| **合计** | **93** | |

## 完整清单

| 插件 | 触发 | 功能 |
|---|---|---|
| `ai/llm` | `/gpt <内容>`、`群聊总结 [条数]` | `/gpt` 保留最近 50 条对话上下文；群聊总结从 SQLite 读取最近一天、最多 1000 条群消息生成总结，每群 30 秒限一次，仅 `adminUsers` 可用 |
| `audio/alipayvoice` | `支付宝到账 <金额>` | 播放对应金额的到账语音（限数字，至多两位小数） |
| `audio/ahsai` | `使<音源>说<文本>`（音源可留空，按编号选择） | AH Soft 日语语音合成，18 种音源；音源为空或非法时回复编号表并保留 10 秒选择状态 |
| `audio/huozi` | `活字乱刷 [-y] [-n] [-r] [-s 速度] [-p 音调] 文本`（也支持 `活字印刷`、`huozi-generator`） | 调用活字乱刷 API 生成语音，支持倒放、变速、变调与等待提示 |
| `audio/midicreate` | `midi制作 <音符串>`、`设置音色 <0~127>`、`个人听音练习`、`团队听音练习` | 音符串（C~B、`#`/`b`、`R` 休止、`<n` 时值）合成 44.1kHz WAV 语音；音色按群保存（私聊按用户）；群内听音练习 |
| `audio/music` | `点歌 <关键词>`、`网易点歌 <关键词>` | 网易云搜索并发送语音；其他平台提示暂不支持 |
| `tool/ping` | `/ping`、`/ping <host>` | 回复 pong；带主机时做 TCP 连通延迟检测（无需 ICMP 权限），支持 IPv4/IPv6 与 `[::1]:端口` |
| `tool/bmi` | `/bmi <身高cm> <体重斤>`、`/bmi-calculator <身高cm> <体重斤>` | 计算 BMI 并给出健康建议；六档建议语可在配置中修改，边界与上游一致 |
| `tool/chrev` | `翻转 <英文>` | 倒序并替换为上下翻转字符（∀qɔ 等） |
| `tool/chouxianghua` | `抽象翻译 <中文>` | 内嵌上游完整拼音表（26140 字）与表情表（395 条）；优先匹配双字拼音组合，其次单字拼音，未命中保留原文 |
| `chat/chat` | @机器人 空消息、`空调开`、`空调关`、`群温度`、`设置温度N` | 被叫应答与群空调小功能 |
| `chat/atri` | `萝卜子`、早安/午安/晚安、@机器人 + 喜欢/夸赞/吐槽等关键词 | 亚托莉人格文本回复，按当前时段切换语料；1:00–5:59 全部静默 |
| `chat/thesaurus` | @机器人 + 任意文本 | 词典式聊天：按关键词匹配内置回复，未命中时给出通用回应 |
| `admin/antiabuse` | 自动检测、`添加违禁词`、`删除违禁词`、`查看违禁词` | 违禁词检测与分群词表，内置三条默认违禁词（可删除）；命中后撤回并禁言 2 分钟，权限不足时降级为提醒；增删词需在 `admin_users` 中 |
| `admin/breakrepeat` | 自动 | 连续复读超过 3 条时打乱原文后回复 |
| `admin/sleepmanage` | `晚安`、`早安` | 记录睡眠时间并输出群内排名 |
| `admin/status` | `检查身体`、`自检`、`启动自检`、`系统状态` | 查询机器人与计算机状态（CPU、内存、磁盘、网络等），纯文本输出 |
| `admin/manager` | `我的ID`、`撤回`、`我要自闭 10分钟`、`解除禁言我`、`禁言 @成员 10分钟`、`踢出 @成员 [并拉黑]` | 自我禁言、撤回本条；配置 `admin_users` 后执行成员禁言/解禁/踢出 |
| `entertainment/bookreview` | `随机书评`、`书评 <关键词>` | 内置 40 条书评语料，随机推荐或按关键词检索，结果以书评卡片图输出（失败回退文本） |
| `entertainment/choose` | `选择 A 还是 B` | 选择困难症帮手 |
| `entertainment/cpstory` | `组cp 攻名 受名`、`磕cp 攻名 受名` | 生成两人的 CP 小故事（也支持逗号、顿号或「和」分隔） |
| `entertainment/curse` | `骂我`、`大力骂我`、@机器人 + 关键词 | 内置两档回击语录 |
| `entertainment/diana` | `小作文`、`发大病`、`教你一篇小作文 <内容>` | 小作文与发病文学；管理员可扩充词库（state 持久化） |
| `entertainment/dish` | `怎么做 <菜名>`、`烹饪 <菜名>`、`随机菜谱` | 程序员做饭指南：查做法或随机推荐 |
| `entertainment/drawlots` | `抽签列表`、`刷新抽签列表`、`抽XX签`、`看XX签`、`加XX签`、`删XX签` | 七类内置签池（每日/事业/学业/缘分/财运/健康/欧气）抽签与查看，纯文本输出；加/删签入口保留，但内置文本签不支持自定义签文 |
| `entertainment/driftbottle` | `扔漂流瓶 <内容>`、`捞漂流瓶` | 漂流瓶投递与随机捞取 |
| `entertainment/emojimix` | 直接发送两个 emoji | 合成 Emoji Kitchen 表情图；无对应组合时静默 |
| `entertainment/emozi` | `抽象转写 <文本>`、`抽象还原 <文本>`、`抽象登录 <用户名> <密码>` | 中文与抽象颜文字互转（公开 API）；可在配置中填账号密码或私聊「抽象登录」，token 按小时续登，失败回退匿名 |
| `entertainment/fortune` | `运势`、`今日运势`、`抽签` | 按「用户 + 日期」生成当日运势（等级/评分/宜忌/幸运物），同人同日结果固定 |
| `entertainment/funny` | `讲个笑话`、`夸夸`（前缀匹配，可带 @成员/QQ/人名） | 冷笑话与夸奖语料，自动替换发送者昵称 |
| `entertainment/idiom` | `成语查询 <成语>`、`近义词查询 <词语>`、`反义词查询 <词语>` | 成语释义、近义词与反义词查询，支持模糊匹配；按配置输出图片或文本 |
| `entertainment/jandan` | `来份屌图`、`来份弔图`、`来份吊图` | 随机煎蛋网无聊图 |
| `entertainment/kfccrazythursday` | `疯狂星期四` | 随机疯狂星期四文案 |
| `entertainment/moyu` | `摸鱼提醒` | 当日摸鱼提醒、周末倒计时，以及元旦/春节/清明/劳动/端午/中秋/国庆七个节日倒计时 |
| `entertainment/omikuji` | `求签`、`占卜`、`解签` | 浅草寺签图与签文（按用户与日期） |
| `entertainment/pig` | `随机猪猪`、`来份猪猪`、`抽个猪猪`、`搜索猪猪 <关键词>`、`猪猪id <编号>` | 随机/搜索/按编号发送猪猪表情包（pig_data 缓存 24 小时） |
| `entertainment/score` | `签到`、`签到 <0~3>`、`设置签到预设 <0~3>`、`获得签到背景`、`查看等级排名`、`查看我的钱包`/`查看钱包余额 [@成员或QQ号]`、`查看钱包排名`、`设置硬币名称`、`管理钱包余额`、`钱包转账` | 签到（数字为本次签到图设定）、经验等级与 ATRI 币钱包、签到背景与预设、等级与钱包排行榜（当日缓存，出图失败回退文本）；钱包排名只统计已签到用户 |
| `entertainment/shadiao` | `哄我`、`渣我`、`来碗绿茶`、`发个朋友圈`、`来碗毒鸡汤`、`讲个段子`、`马丁路德骂我` | 沙雕 app 文案 |
| `entertainment/shindan` | `异世界转生`、`今天是什么少女`、`卖萌`、`今日老婆`、`黄油角色` | 测定结果转纯文本；未指定名字时优先使用发送者昵称 |
| `entertainment/tarot` | `抽塔罗牌 [问题]`、`抽大阿卡纳`、`抽小阿卡纳`、`抽N张塔罗牌`、`解塔罗牌 <牌名>`、`塔罗牌阵 <牌阵名>` | 78 张塔罗牌正逆位抽取与 7 种牌阵解读，保留提问；配置 `ai_base_url`/`ai_api_key`/`ai_model` 后额外生成 AI 解读，失败时静默回退牌面 |
| `entertainment/tiangou` | `舔狗日记`、`舔狗语录` | 随机舔狗日记与短句 |
| `entertainment/toutai` | `toutai`、`toutai.投胎中国`、`toutai.投胎世界`、`toutai.中国投胎记录.*`、`toutai.世界投胎记录.*`、`toutai.中国投胎排行榜.*`、`toutai.世界投胎排行榜.*` | 投胎模拟器：按真实出生人口加权抽取中国省份/世界国家，记录降生与夭折，出地图与记录/排行榜卡片，数据来自上游内嵌统计 |
| `entertainment/wife` | `抽老婆`、`猜老婆`（回答「xxx酱」或「跳过」） | 二次元角色老婆库：按用户+日期稳定抽取、远程图片懒加载缓存；猜老婆三档模糊、三次机会与超时揭晓 |
| `entertainment/verse-match` | `对对联 <上联>` | 返回匹配下联（公开 API） |
| `entertainment/wtf` | `鬼东西列表`、`查询鬼东西N [@成员]` | 随机鬼东西测试；优先使用被 @ 的成员，其次发送者昵称 |
| `entertainment/xhstext` | `捧场`、`有梗` | 小红书式捧场话术与接梗点评 |
| `image/gif` | `搓`、`摸`、`亲`、`垃圾桶`、`灰度`、`旋转` 等 100+ 制图命令（可 @成员、附带图片或写 QQ 号） | 基于头像的 GIF/PNG 表情制图，素材与字体首次使用时自动下载并缓存 |
| `image/realcugan` | `清晰术` + `双重吟唱/三重吟唱/四重吟唱` + `强力术式/中等术式/弱术式/不变式/原式`（附图片） | 调用 Real-CUGAN 放大并去噪，倍数与去噪等级按像素数自动调整；先回「少女祈祷中...」 |
| `game/endfield` | `:帮助`、`:绑定 <Cred>`、`:授权登陆`、`:扫码绑定`、`:手机绑定 [手机号]`、`:绑定列表`、`:切换绑定 <序号>`、`:删除绑定 <序号>`、`:便签`、`:角色`、`:影拓丰碑`、`:危机合约`、`:同步面板`、`:练度统计`、`:<干员名>面板`、`:帝江号建设`、`:地区建设`、`:理智`、`:订阅理智`、`:签到`、`:日历`、`:公告`、`:抽卡分析`、`:十连`/`:百连`/`:单抽`、`:wiki 干员 <名称>`、`:<名称>攻略`、`:maa ...`、`:蓝图` 等 | 终末地（森空岛）查询：多账号绑定、便签/角色、危机合约、建设、理智、公告、日历、抽卡分析与模拟抽卡、Wiki、攻略与 MaaEnd 远程控制；前缀支持 `:`/`：`/`#zmd`/`#终末地`/`/zmd`/`/终末地` |
| `game/gloryofkings` | `#绑定营地 <ID>`、`#切换营地<序号>`、`#删除营地<序号>`、`#营地ID`、`#营地wx全局登录`、`#营地QQ全局登录`、`#王者添加tk <ID> <token>`、`#查询战绩`、`#排位战绩`、`#巅峰战绩`、`#查询N战绩`、`#查询战绩<序号>`、`#排位战绩<序号>`、`#查<英雄>战绩`、`#查战绩 <英雄>`、`#我的英雄[N]`、`#常用英雄`、`#英雄战力榜`、`#皮肤墙[N]`、`#全部皮肤`、`#缺皮肤[英雄名]`、`#查皮肤 <英雄名>`、`#赛季表现[sN]`、`#排位表现[sN]`、`#巅峰表现[sN]`、`#全部排位表现`、`#全部巅峰表现`、`#巅峰趋势`、`#段位趋势`、`#排位排名`、`#巅峰排名`、`#排位bot排名`、`#巅峰bot排名`、`#排位总排名`、`#巅峰总排名`、`#王者签到`、`#王者签到状态`、`#营地币`、`#营地余额`、`#营地兑换[N] [确认]`、`#隐私帮助`、`#隐私状态`、`#隐私隐藏 N [档位]`、`#隐私公开 N [档位]`、`#隐藏我的战绩`、`#公开我的战绩`、`#王者在线`、`#在线状态` | 王者荣耀营地账号管理、扫码登录/手工 token 导入、战绩列表与单场详情、按英雄筛战绩、英雄/皮肤/赛季表现/趋势图出图、排位/巅峰的 bot 与总排名（群内排名需群成员列表，暂不可用）、营地签到与营地币兑换、营地隐私开关（私聊）、在线/对局状态快查；登录态按用户持久化 |
| `game/guessmusic` | `猜歌`、`个人猜歌`、`团队猜歌`、`猜歌 <答案>`、`提示`、`揭晓`、`结束猜歌`、`取消`、`猜歌排行` | 网易云热歌榜随机抽歌并发送语音，群内猜歌名/歌手；前两次「提示」重发当前音频（对齐上游再听一段），第三次提示已用完；按群记录题目、按用户记分，取消/结束仅发起者可用 |
| `game/handou` | `个人猜成语`、`团队猜成语`、`猜成语热门汉字`、`猜成语热门成语` | 四字成语猜谜，每人七次机会；首次开局惰性下载上游完整 `idiom.json`（失败回退内置词库），热门榜按累计猜中次数输出 Top10 |
| `game/mcfish` | `钓鱼`、`进行N次钓鱼`、`钓鱼背包`、`修复鱼竿`、`钓鱼商店`、`购买 <物品> [数量]`、`出售 <物品> [数量]`、`装备 <鱼竿>`、`附魔 <属性>`、`合成 <物品>`、`出售所有垃圾`、`当前装备概率明细`、`查看钓鱼规则`、`消除诅咒 [N]` | Minecraft 风格钓鱼小游戏：每日次数与耐久、三叉戟/美西螈加成、诅咒、鱼竿修复强化、日价格波动商店与库存税、附魔与合成，状态按用户持久化 |
| `game/niuniu` | `注册牛牛`、`打胶`、`jj@用户`、`排行榜`、`商店`、`拍卖行`、`赎牛牛`、`注销牛牛` | 群内牛牛养成（state 持久化）；商店/拍卖行在 120 秒会话内按序号浏览；赎回需二次确认，注销按 24 小时内第 N 次扣 N*50 金币，余额不足拒绝 |
| `game/poker` | `抽扑克`、`poker` | 从 54 张牌（含 Lynette/Lyney 特殊牌）随机抽一张并返回牌面图片（内嵌压缩牌图） |
| `game/wordle` | `个人猜单词`、`团队猜单词` | 五/六/七阶英文猜词；内嵌 CET-4 词表与对应词典，猜测词必须在词典中 |
| `query/dailynews` | `今日早报` | 今日早报图片 |
| `query/github` | `>github 关键词`；群里直接发 `github.com/owner/repo` 链接 | GitHub 仓库搜索与链接自动解析 |
| `query/hitokoto` | `一言 [分类/关键词]`、`系列一言` | 随机一言与出处；按分类/关键词筛选时最多返回 10 条并逐条发送；系列一言发送分类序号并在 120 秒内选号 |
| `query/hs` | `搜卡 卡名`、卡组代码 `[AAE…]` | 炉石传说卡牌图片（最多 5 张）；识别卡组代码并生成卡组图 |
| `query/hyaku` | `百人一首`、`百人一首之N` | 随机或按编号发送百人一首和歌与上下句配图，含上下句平假名读音 |
| `query/moyucalendar` | `摸鱼人日历` | 摸鱼人日历图片 |
| `query/nbnhhsh` | `?? <缩写>`、`？？<缩写>` | 拼音首字母缩写释义 |
| `query/warframeapi` | `地球时间`、`金星时间`、`火卫二时间`、`wf警报`、`wf仲裁`、`wf每日特惠`、`wf时间同步`、`.wm <物品>` | Warframe 世界状态；`wf时间同步` 刷新并复用 5 分钟本地时间模拟；`.wm` 在 warframe.market 模糊搜索物品、编号选择后输出满级卖单与 `/w` 文案 |
| `query/ygocdb` | `/ydp 卡名`、`/yds 卡名`、`/ydb 卡名`（多结果时数字选择、`下一页`、`取消`） | 游戏王卡图与效果；多条结果列出 10 条并保留 20 秒选择状态 |
| `query/yujn` | `随机骚话`、`土味情话`、`随机污句子`、`随机美句`、`御姐撒娇`、`绿茶语音`、`怼人语音`、`让lulu说<文本>` | 遇见 API 随机文本；三条语音指令直接发送语音，`让lulu说` 走 TTS 接口合成语音 |
| `query/bilibilipush` | `b站用户 <UID>`、`b站动态 <UID>`、`b站直播 <UID>`、`b站订阅列表`、`添加b站订阅 <UID>`、`取消b站订阅 <UID>`、`取消b站动态订阅 <UID>`、`取消b站直播订阅 <UID>`、`开启艾特全体`、`关闭艾特全体`、`b站推送` | B 站 UP 主资料/动态/直播查询；管理员可订阅并手动拉取更新（动态走 WBI 签名）；可单独关闭动态/直播推送，直播推送支持群级 @全体（QQ 官方接口不支持真实 @全体，以文本前缀实现） |
| `query/bilibiliparse` | 群里直接发 B 站视频/专栏/直播间链接或 `b23.tv` 短链 | 自动解析链接并回复标题、UP/作者、数据与封面图（同群 10 秒冷却） |
| `query/rsshub` | `rss /路由`、`rss订阅列表`/`查看rsshub订阅列表`、`添加rss订阅 /路由`/`添加rsshub订阅-/路由`、`删除rss订阅 /路由`/`删除rsshub订阅-/路由`、`rss同步`/`rsshub同步` | RSSHub 订阅源抓取（多实例回退，兼容 RSS 2.0 / Atom / RDF）；推送含标题、时间、摘要与首图 |
| `query/steam` | `steam <SteamID/自定义ID>`、`steam订阅列表`、`steam添加订阅 <ID>`、`steam删除订阅 <ID>`、`steam推送`、`steam绑定apikey <Key>`、`查看apikey` | Steam 玩家状态查询与订阅；订阅增删群成员即可，私聊管理员可绑定 Key 并脱敏查看 |
| `query/wantquotes` | `据意查句`、`据意查句 <关键词>`、`据意查句 <分类序号> <关键词>`、`设置据意查句 unionid\|secret` | 据意查句（wantquotes）语义搜句，18 个分类；不带序号时先发分类列表并在 10 秒内选号；密钥由管理员配置后持久化 |
| `query/animetrace` | `动漫识图`、`动漫识图 2`、`动漫识图 <模型名>`、`Gal识图`、`Gal识图 <模型名>`（可附图片或写图片 URL） | AnimeTrace 动画/Galgame 以图搜番；先回识别进度，再按接口 box 裁剪并发送角色图，附相似度、作品、角色与时间点 |
| `query/tracemoe` | `以图搜番`、`搜番`、`搜索番剧`（可附图片或写图片 URL） | trace.moe 以图搜番，逐张处理全部图片，返回番剧标题、集数、时间点、相似度、是否成人向，并发送预览图 |
| `query/ymgal` | `gal <关键词>`、`gal查询 <关键词>`、`随机gal`、`galCG`、`随机galCG`、`gal表情包`、`随机gal表情包`、`galCG <关键词>`、`gal表情包 <关键词>`、`更新gal`（管理员） | 月幕 Galgame 作品搜索、随机推荐与 CG/表情包图包；图包在运行时抓取官网页面（每包最多 50 张）；需在配置中填 `client_id`/`client_secret` |
| `query/bilibili` | `查成分 <UID>`、`查弹幕 <UID>`、`>user info <UID>`、`>vup info <UID>`、`更新vup`（管理员）、`设置b站cookie <Cookie>` | B 站 UP 主成分/直播弹幕/资料查询，成分与弹幕以图片卡片输出（失败回退文本）；VUP 全量字段缓存 10 分钟；昵称搜索复用 bilibilipush 的 WBI 签名，Cookie 可选 |
| `query/movies` | `今日电影`、`预售电影`、`电影搜索 <片名>`、`电影详情 <片名>`、`电影帮助` | 猫眼电影热映/预售榜单卡片图（海报、导演、演员、标签、上映时间、评分）与搜索详情，出图失败回退文本 |
| `query/ygotrade` | `查卡价 <卡名> [-r 稀有度]`、`查卡店 <卡名> [-r 稀有度]` | 集换社游戏王卡价与在售卡店查询，按价格升序展示并补发卡图 |
| `query/minecraftobserver` | `mc服务器状态 <地址>`、`mc服务器添加订阅 <地址>`、`mc服务器取消订阅 <地址>`、`mc服务器订阅列表`、`mc服务器订阅拉取`、`mc服务器全局订阅列表`（管理员） | Java 版 MC 服务器状态：在线、版本、MOTD、玩家数、延迟、模组与服务器图标；订阅状态变化由「订阅拉取」检查推送 |
| `query/genshin` | `原神 <UID>`、`原神角色 <UID>`、`原神十连`、`切换原神卡池` | 通过 Enka Network 查询玩家资料、角色橱窗（等级/命座/好感/武器）；原神十连抽卡含四星/五星保底与卡池切换，首次使用自动下载上游立绘素材 |
| `query/weather` | `weather-未来天气 <城市>`、`weather-当日天气 <城市>` | 内嵌 cityid/adcode 城市表，查询当日与多日天气（1~15 天） |
| `tool/base16384` | `加密 <文本>`、`解密 <密文>`、`用<密钥>加密 <文本>`、`用<密钥>解密 <密文>` | base16384 编解码；带密钥时叠加与上游逐字节一致的 TEA 加密，不限制输入长度 |
| `tool/base64gua` | `六十四卦加密 <文本>`、`六十四卦解密 <密文>`、`六十四卦用<密钥>加密 <文本>`、`六十四卦用<密钥>解密 <密文>` | 六十四卦字符（U+4DC0 起）编码，6 bit/字符；带密钥时叠加 TEA |
| `tool/baseamasiro` | `天城文加密 <文本>`、`天城文解密 <密文>`、`天城文用<密钥>加密 <文本>`、`天城文用<密钥>解密 <密文>` | 天城文字符（U+0900 起）编码，7 bit/字符；带密钥时叠加 TEA |
| `tool/cnbimagebed` | `图床上传`（附图片）、`图床状态 [N天]`、`图床失败记录 [N天]`、`图床失败原因 [N天]` | 上传图片到 CNB 图床，md5 缓存复用、上传统计、失败原因汇总与过期缓存清理；需配置 API 地址、token 与仓库 |
| `tool/crypter` | `齁语加密 <文本>`、`齁语解密 <文本>`、`h加密`/`h解密`、`fumo加密 <文本>`、`fumo解密 <文本>` | 兽吼（齁语）与芙语（fumo）两种文字加密；配置 `voiceEndpoint` 后齁语加密优先发语音，失败回退文本；解密只支持指令内密文 |
| `tool/font` | `渲染文字 <内容>`、`用<字体>渲染文字 <内容>`、`渲染抖动文字 <内容>` | 用本机字体把文字渲染成图片；抖动模式输出 10 帧 GIF |
| `tool/furrycode` | `兽语加密 <内容>`、`兽语解密 <内容>` | 兽语编解码，支持 legacy、msbt 与 4 字符自定义码本 |
| `tool/imagesave` | `保存图片 <名称> [文件夹]`、`获取链接`（可调试开关） | 保存消息/回复中的图片到本地目录，支持 5 种交互模式、命名变量、重名检查与重复图片监听 |
| `tool/patina` | `幻影`、`幻影解图`、`像素化`、`相机镜框`、`光棱`、`光棱取图`、`原图坦克`、`原图坦克2.合成`、`原图坦克2.提取` | 基于本地浏览器渲染的幻影/光棱坦克、像素化、相机镜框与 GIF/APNG 合成；六组功能默认关闭，需在配置中开启 |
| `tool/qqmarkdown` | `按钮菜单`（指令名与模板可配置） | 按配置的模板目录与步骤依次发送 QQ Markdown/JSON 消息与按钮 |
| `tool/qrcode` | `/qrcode [-m 边界] [-s 缩放] [-w 大小] [-d 暗色] [-l 亮色] <文本>` | 生成二维码图片并发送；`enablecommand` 默认关闭，开启后才响应指令 |
| `tool/universaltranslation` | `/universal-translation [-t 目标语言] <文本>`、`/universal-translation` | 通用翻译，默认目标语言可配置；不带文本时返回语言代码对照表 |
| `tool/wordcount` | `字数 <文本>`、`字数统计 <文本>`、`词频 <文本> [N]`、`热词 [N]` | 统计本条消息的字符/汉字/单词/标点等数量，或输出中文二字词与英文单词词频 Top N；`热词` 入口保留，因框架无历史消息接口暂不可统计 |

## 使用说明

### 指令注册

所有插件在保留原有聊天触发词的同时，都以 `/` 前缀注册了命令，可用 `/help` 查看。宿主只接受 ASCII 命令名（`sdk.ValidID`：字母、数字、`_`、`-`），因此中文触发词通过插件 ID 别名注册：

- 有 ASCII 原生触发词的插件直接沿用原名：`/steam`、`/rss`、`/rsshub`、`/poker`、`/ping`、`/qrcode`、`/bmi`、`/bmi-calculator`、`/gal`、`/ydp`、`/yds`、`/ydb` 等。
- 纯中文或符号触发的插件使用插件 ID 作别名：`/hitokoto`（等价「一言」）、`/drawlots`（等价「抽签列表」）、`/bilibilipush`（等价「b站用户」）、`/endfield`（等价「:帮助」）、`/gloryofkings`（等价「#王者帮助」）。
- `/插件ID 参数` 会把参数拼回原触发词后再交给插件，例如 `/steam 7656119...`、`/tracemoe 图片`。
- 纯被动插件（违禁词检测、复读检测、链接解析、emoji 合成、图片监听等）只注册说明型命令，`/插件ID` 返回用法，避免与自动监听重复回复。
- 原有中文触发、`#`/`>`/`.`/`:` 前缀触发行为完全不变，注册的 `/指令` 只是额外入口。

### 推送类插件

`query/bilibilipush`、`query/rsshub`、`query/steam` 不使用常驻轮询或定时群发：QQ 官方机器人没有常驻推送通道，定时群发容易触发频控和打扰。它们统一采用「群内按需查询 + 管理员手动拉取」：

- `bilibilipush`、`rsshub` 的订阅增删与主动拉取（`b站推送`、`rss同步`）需要发送者在插件配置的 `admin_users` 中（与 `admin/manager` 相同口径）；`steam` 的订阅增删群成员即可，主动拉取仍需管理员。
- 主动拉取有 60 秒频控，每个群最多 5 个订阅。
- 首次添加订阅只记录基线，之后只推送新增条目，避免刷屏；动态/RSS 推送带摘要与首图，图文分条发送。
- Steam 需要在私聊由管理员执行 `steam绑定apikey <Key>` 保存 Web API Key（`steamcommunity.com` 在本机不可达），`查看apikey` 只回脱敏结果。
- 直播开播的「@全体」使用文本前缀：QQ 官方群消息接口不支持真实 @全体。

### 按钮适配

以下插件在成功回复时附带 QQ 官方机器人按钮（指令型，点击后发送对应文本；需要应用开通自定义键盘权限，未开通时自动降级为纯文本）：

| 插件 | 按钮 |
|---|---|
| `query/warframeapi` | 地球时间 / 金星时间 / 火卫二时间 / wf警报 / wf仲裁 / wf每日特惠 |
| `query/hitokoto` | 再来一条 |
| `query/yujn` | 换一句 |
| `entertainment/kfccrazythursday` | 再来一条 |
| `entertainment/shadiao` | 再哄一次 / 来碗毒鸡汤 / 换个段子 |
| `entertainment/curse` | 再骂一句 / 大力骂 |
| `entertainment/driftbottle` | 再捞一个 / 我也捞一个 |
| `entertainment/omikuji` | 解签 |
| `entertainment/dish` | 随机菜谱 |
| `entertainment/funny` | 再来一个 / 再夸一次 |
| `entertainment/tiangou` | 再来一篇 / 再来一句 |
| `entertainment/cpstory` | 再磕一次 |
| `entertainment/xhstext` | 再捧一次 / 再接一个 |
| `entertainment/diana` | 再来一篇 |
| `query/hyaku` | 再来一首 |
| `chat/chat` | 开空调 / 关空调 / 查看温度 / 26℃ |
| `game/wordle` | 再来一局 / 结束 |
| `game/handou` | 再来一局 / 结束 |
| `game/poker` | 再抽一张 |
| `entertainment/jandan` | 换一张 |
| `game/niuniu` | 打胶 / 商店 / 背包，商店与拍卖行按钮带商品序号 |
| `game/gloryofkings` | 查询战绩 / 营地签到 / 营地兑换，兑换确认按钮带商品序号 |

几个约定：

- 图片消息无法携带键盘，`poker`、`jandan`、`gloryofkings` 会先发一条带按钮的 Markdown，再发图片。
- `niuniu` 商店/拍卖行、`gloryofkings` 兑换确认的按钮把商品 ID/序号拼进指令，商品售出或清单刷新后点击会走原有的「没有该序号」提示。
- `wordle`、`handou` 的个人模式对局绑定发起者，别人点击「再来一局」「结束」不会有响应。

### 链接自动解析

无需指令，群里发送链接即自动解析并回复（图片消息不能带链接，统一先发 Markdown 文本再发图）：

| 插件 | 识别的链接 | 返回 |
|---|---|---|
| `query/bilibiliparse` | `b23.tv` / `acg.tv` / `bili2233.cn` 短链，`bilibili.com/video/av..`、`/video/BV..` | 视频标题、UP、时长、播放/弹幕/点赞、封面图 |
| `query/bilibiliparse` | `bilibili.com/read/cv..` | 专栏标题、作者、阅读/点赞、摘要、头图 |
| `query/bilibiliparse` | `live.bilibili.com/<房间号>` | 直播间标题、主播、开播状态、在线人气、封面 |
| `query/github` | `github.com/<owner>/<repo>`（含 `.git`、`/issues` 等后缀） | 仓库名、描述、Star/Fork/Issue、语言、许可证、预览图 |

几点说明：

- B 站解析对同一群有 10 秒冷却；短链还原最多跟随 3 跳，识别不出类型时静默忽略。
- 不支持视频下载与 AI 总结：框架只能发图片/语音，没有视频发送能力。
- `t.bilibili.com` 动态需要 cookie 与 WBI 签名，稳定性差，暂不解析。

### 链接适配

以下插件把上游可访问页面补成可点击的 Markdown 链接（平台不支持 Markdown 时退回纯文本）：

| 插件 | 链接 |
|---|---|
| `query/github` | 仓库名 → 仓库页 |
| `query/ygocdb` | 详情 → ygocdb 卡片页 |
| `query/hs` | 来源 → fbigame 卡牌查询页 |
| `entertainment/jandan` | 原帖 → jandan.net/pic |
| `entertainment/shindan` | 来源 → 对应测定页 |
| `entertainment/verse-match` | 来源 → 对联网 |
| `entertainment/emozi` | 来源 → emozi 站点 |
| `game/gloryofkings` | 营地币回复里的活动入口 → Banner 跳转地址 |

## 移植说明

<details>
<summary><b>纯逻辑 / 无密钥插件的降级</b></summary>

这一批插件在上游都依赖图片素材、字体文件、第三方分词/绘图库或扫码登录，移植时按框架能力做了等价降级：

| 插件 | 上游做法 | 本仓库做法 |
|---|---|---|
| `entertainment/drawlots`、`entertainment/fortune`、`entertainment/tarot` | 底图 + gg 绘制运势/签文/牌阵图 | 全部改为纯文本；`fortune` 以「用户 + 日期」做种，同人同日结果固定；`drawlots` 保留加/删签入口但内置文本签不支持自定义签文 |
| `tool/font` | 内置 12 套 TTF，用 gg 逐字绘制 | `sdk.RenderHTML` + CSS 字体栈，效果取决于本机已装字体；抖动模式用标准库 `image/gif` 合成 10 帧，失败或超过 5 MiB 退回单帧 |
| `tool/wordcount` | 拉取群历史消息、jieba 分词、渲染柱状图 | 框架没有历史消息接口，只统计本条消息；中文按连续汉字切二元词，纯文本排名；`热词` 入口保留但暂不可统计 |
| `tool/crypter` | 支持 QQ 小黄脸（face 消息段）加密 | 事件结构不暴露 face 段，只保留齁语与 fumo；解密只接受指令内的密文 |
| `tool/base16384`、`tool/base64gua`、`tool/baseamasiro` | 上游用 floatbox 的 TEA 实现做带密钥加密 | `base16384` 已按 floatbox `GetTEA`（key 截断/循环补齐 16 字节）与 gofastTEA（自定义填充 + 双 IV 链式）逐字节对齐，并有上游密文测试向量；`base64gua`/`baseamasiro` 仍为「md5(key) + PKCS#7」推定实现 |
| `query/wantquotes` | 微信扫码登录换取 `unionid`/`secret` | 不做扫码，改为管理员执行 `设置据意查句 unionid\|secret` 保存密钥（`admin_users` 口径） |

另需注意：`tool/base16384` 的无密钥指令是通用的 `加密` / `解密`，与其它插件的同类指令可能冲突，部署时按需保留。

</details>

<details>
<summary><b>公开 API 查询插件的功能对齐</b></summary>

这一批全部走公开 HTTP 接口，不需要私有密钥（个别平台需要免费开发者凭证）。出图统一使用 `sdk.RenderHTML` 渲染 HTML 卡片，失败时回退文本；远程图片下载后转 data URI 内嵌，避免 QQ 侧外链限制：

| 插件 | 上游做法 | 本仓库做法 |
|---|---|---|
| `query/animetrace` | 调 AnimeTrace 接口并回传标注图 | 先回「少女祈祷中……共识别到 N 个角色」，再按接口 box 归一化坐标裁剪原图并逐张发送（JPEG/PNG/GIF，最多 20 条）；box 缺失或解码失败时回退原图 |
| `query/tracemoe` | 调 trace.moe 并回传预览图 | 逐张处理全部图片，先回识别进度，再输出番剧/集数/时间点/相似度/成人向并发送预览图；单张失败不影响其余 |
| `query/ymgal` | 下载 gal 图包，支持随机 CG / 表情包 | 用标准库 regexp 抓取 ymgal 官网列表/详情页，恢复 `galCG`、`gal表情包`、关键词图包与 `更新gal`；随机页翻页 + 随机 id，单包最多 50 张；凭证通过 `client_id`/`client_secret` 配置 |
| `query/bilibili` | 本地 sqlite 维护 VUP 列表、出成分图 | VUP 全量字段缓存到 state（10 分钟 TTL，`更新vup` 强制刷新）；成分/弹幕用 HTML 卡片出图（960 宽失败或超 5 MiB 降 760 重试）；昵称搜索复用 bilibilipush 的 WBI 签名 |
| `query/movies` | 猫眼榜单出图 | 榜单用 HTML 卡片出图（海报、导演、演员、标签、语言、上映时间、评分，最多 8 条），失败回退文本；详情失败时回退搜索结果，简介截断 |
| `query/ygotrade` | Markdown 链接结果 | 文本汇总后逐条补发卡图并补编号、卖家 ID；链接发送失败时自动降级为「文本 (URL)」 |
| `query/minecraftobserver` | 轮询服务器并主动推送状态变化 | 支持 `添加订阅/取消订阅/订阅列表/全局订阅列表/订阅拉取`；上游由 job 插件定时触发拉取，本框架改由「mc服务器订阅拉取」触发，其他会话的变化会在该会话下次发消息时补发；服务器图标改内嵌发送 |
| `query/genshin` | 原神抽卡模拟（依赖图片素材与本地绘图） | 增加 `原神十连`/`切换原神卡池`：每 10 抽保底五星、四星保底、普通池/五星池概率与上游一致。首次抽卡从 jsDelivr 下载上游 `Genshin.zip`（约 14.5 MB）并缓存到用户缓存目录，内存解析后按上游布局合成 1920×1080 立绘卡图；下载或解码失败时回退名称卡片。角色/武器名走 Enka 静态数据并内存缓存，拉取失败时显示 ID |
| `game/mcfish` | 完整钓鱼玩法（图片、排行榜、天气、饵料） | 完整移植玩法（每日次数、耐久、三叉戟/美西螈、诅咒、修复强化、商店日价格与库存税、附魔、合成、概率明细、规则），纯文本 + JSON state，并迁移旧版字段；上游 `articlesInfo.json` 未随源码提供，物品/概率按源码默认值重建 |
| `game/guessmusic` | 本地歌库 + ffmpeg 切歌 | 网易云热歌榜随机取歌，按外链发 mp3 语音；`取消`/`结束猜歌` 仅发起者可执行（对齐上游「你无权限取消」）。本地歌库与 ffmpeg 切歌为硬限制，改为「提示」重发当前音频（最多两次）；发送失败时改发歌曲链接并保留题目 |
| `entertainment/bookreview` | 内嵌书评语料 | 上游 sqlite 语料未随源码提供，内置 40 条同风格语料；随机推荐或按关键词检索，并以书评卡片图输出 |

所有插件的外部地址都是包级变量，单元测试统一用 `httptest` 替换，测试过程不联网。

尚未移植的原始插件仍保留在 `sources/` 对应目录中，每个目录即一个插件。

</details>

<details>
<summary><b>图片 / 媒体处理插件</b></summary>

| 插件 | 上游做法 | 本仓库做法 |
|---|---|---|
| `audio/ahsai` | `fumiama/ahsai` 合成 OGG | 同样使用 ahsai，去掉提示音后转 WAV 发送；音源编号按名称排序（与上游一致），音源为空/非法时列出编号并保留 10 秒选择状态，超时随机选择 |
| `audio/midicreate` | 群文件上传 `.mid`/`.txt` 互转 + timidity 合成 | 内置加法合成器输出 44.1kHz 单声道 WAV，音符语法（C~B、`#`/`b`、`R`、`<n`）与上游一致；听音练习 45s 作答/60s 超时。框架没有「群文件上传」事件，上游依赖该事件的 `midi制作*.txt` 与 `.mid→txt` 两个文件互转入口无法触发 |
| `entertainment/emojimix` | Emoji Kitchen 静态图片 | 先 HEAD 两个 emoji 组合的图片，成功后发送；无对应组合时静默 |
| `entertainment/pig` | `pig_data.json` + assets | 从 jsDelivr 拉取并在内存缓存 24 小时，随机/搜索/按编号发送 |
| `entertainment/score` | sqlite 存数据 + 绘图库出签到图/排行榜 | state 持久化 + `sdk.RenderHTML` 卡片；数值公式对齐上游（经验上限 1200、奖励 `1+rand(10)+等级*5`、4 种签到设定）；「获得签到背景」发送当日主题背景图，出图失败回退文本 |
| `image/gif` | 73 条制图命令（同一函数多命令） | 102 个命令全量移植，复用 `FloatTech/gg/factory`；素材与字体首次使用时从 jsDelivr 下载并缓存到用户缓存目录 |
| `image/realcugan` | 调 HuggingFace Space 超分 | 同样调用 `run/predict`；缩放倍数与去噪等级按像素数自动选择，并附带上游的「N重唱X分支大清晰术!」说明 |

</details>

<details>
<summary><b>Koishi 上游移植插件</b></summary>

这一批来自 `koishi-shangxue-apps`，均按上游功能完整移植：

| 插件 | 上游做法 | 本仓库做法 |
|---|---|---|
| `tool/qrcode` | `qrcode` 库生成二维码 | 同样使用 `skip2/go-qrcode`，边界/缩放/尺寸/配色参数与上游一致 |
| `tool/furrycode` | legacy / msbt / 自定义码本 | 纯算法移植，三种方式与自定义 4 字符码本均可用 |
| `tool/universaltranslation` | 语言对照表 + 翻译接口 | 内嵌 `languageMap.json`；腾讯与 Cloudflare 两个翻译端点自动回退，目标语言可配置 |
| `query/weather` | 和风天气 + 城市表 | 内嵌 cityid/adcode 小表，主备两个公开天气接口回退，支持当日与 1~15 天预报 |
| `entertainment/idiom` | 本地成语库 + 图片模板 | 抓取公开成语页面查询释义/近义词/反义词；图片模式使用内嵌 HTML 模板渲染，失败回退纯文本 |
| `audio/huozi` | 活字乱刷 API | 同一 API，支持等待提示、倒放、变速、变调与「原声大碟」映射 |
| `admin/status` | 系统状态查询 | 用 `gopsutil` 采集 CPU/内存/磁盘/网络等信息，纯文本输出 |
| `tool/imagesave` | 保存图片到本地目录 | 5 种交互模式、命名变量与重复图片监听均保留；需要把 `savePaths` 映射到宿主机可写目录 |
| `tool/qqmarkdown` | 模板 + 按钮组合发送 | 模板目录相对机器人工作目录解析；按钮回调依赖宿主交互事件路由，未开通时只发送消息本身 |
| `tool/cnbimagebed` | 覆盖 `Bot.imageToUrl` 自动上传 | 宿主没有该钩子，改为「图床上传」指令复用同一上传流程；需配置 API 地址、token 与仓库 |
| `tool/patina` | Puppeteer + 文件选择器 + ffmpeg | `sdk.OpenBrowser` 打开临时 `file://` 页面，用 `DataTransfer` 注入图片；原图坦克用标准库 `image/gif` 合成两帧 GIF；APNG 合成/提取在页面内完成 |

patina 的额外限制：

- 依赖本机 Chrome/Edge（`sdk.OpenBrowser`），并需要一段时间的等待交互（默认 30 秒内发送后续图片）。
- 「原图坦克」上游用 `sendAsFile` 发文件；宿主没有文件消息类型，统一按 `image/gif` 发送。
- `sdk.ReplyImageBytes` 单条内联上限 5 MiB，超大 GIF/大图可能发送失败，可调小输出尺寸或像素化参数规避。

</details>

<details>
<summary><b>独立上游移植插件</b></summary>

| 插件 | 上游做法 | 本仓库做法 |
|---|---|---|
| `entertainment/toutai` | Koishi + SQLite + Puppeteer + ECharts 出地图与卡片 | 数据表内嵌（世界 217 国、中国 34 省分城乡/胎次/性别出生人数）；概率算法逐行移植（按年出生人数加权、新生儿死亡率、冷却、并列名次）；卡片沿用上游 M3 样式并预生成 CSS；地图改为 Go 直接生成 SVG（等距投影 + 旧迹色阶 + 落点标记），不依赖 ECharts/CDN；记录与索引写入 State |
| `entertainment/wife` | ZeroBot 老婆库 + `GetLazyData` 远程图片 | `wife.json` 内嵌，图片按需从 jsDelivr/GitHub/gitea 下载并缓存到用户缓存目录；每日抽取复刻 CRC64(日期+用户) 种子；猜老婆用 `imaging` 高斯模糊，三档难度、三次机会、超时揭晓 |
| `ai/llm` | ZeroBot `GetGroupMessageHistory` + 大模型 | 改为插件自行记录群消息到 SQLite（`modernc.org/sqlite`，纯 Go），保留一天、每次清理过期数据；`/gpt` 按会话保留最近 50 条上下文；`群聊总结` 默认 200 条、上限 1000 条、每群 30 秒限频 |
| `game/endfield` | Yunzai + Redis + Puppeteer + 120 MB 模板/图片资源 + 统一后端 API | 全部命令与后端端点移植；绑定记录写入 State；每次出图用 `sdk.RenderHTML` 渲染统一卡片（资源已全量打包进插件）；抽卡模拟规则、MaaEnd 设备/任务流程、Wiki/攻略内容解析均按上游实现 |

toutai 的额外限制：

- 上游 `imageType` 支持 `png/jpeg/webp`；本仓库渲染输出 JPEG，`png` 会转码为 PNG，`webp` 因标准库无编码器按 PNG 发送。
- `retractDelay`（自动撤回上一条消息）需要发送后返回 messageId，宿主 Reply 不返回 ID，该配置不生效。
- 世界/中国地图使用 SVG 近似投影，视觉与 ECharts 不完全一致，落点、着色、标签信息一致。

endfield 的额外限制：

- 必须配置 `apiKey`（终末地协议终端）与后端地址；未配置时公开/查询类命令都会提示先配置。
- 宿主没有定时任务 API：公告推送、理智订阅推送、自动签到、网页授权状态轮询无法在后台执行；订阅/开关会保存，但需要宿主提供调度能力后才能推送。
- `更新` / `强制更新` 对编译期插件无意义，会回复提示并保留命令入口。
- MaaEnd 需要用户自行运行 MaaEnd Client；截图/任务轮询按上游端点实现，超时与状态以客户端返回为准。
- 上游 HTML 模板使用 art-template 语法，本仓库改为 Go 侧等价卡片渲染；信息字段完整，排版非逐像素复刻。
- 资源目录约 120 MB（含 8 MB 字体、攻略图与抽卡立绘）已随插件 `go:embed` 打包，会显著增大二进制与构建时间。

wife / llm 的额外说明：

- `llm` 使用 `modernc.org/sqlite`（纯 Go，无 CGO）；数据库默认在用户缓存目录 `ShizukuBot/llm/llm.db`，可用 `databasePath` 指定。
- 群聊总结只统计插件启动后收到的群消息；不包含图片、语音等非文本内容。
- `llm` 的群聊总结仅 `adminUsers` 中的用户可用；默认 30 秒一次、最多 1000 条、只保留一天。
- `wife` 的图片按需下载并缓存；首次抽取或猜图时若网络不可用会提示下载失败，已缓存过的图片可离线使用。

</details>

## 移植约定

1. **优先只用标准库 + `shizukubot/sdk`**；确有必要时允许引入纯 Go 依赖（目前为 `ai/llm` 的 `modernc.org/sqlite`、`entertainment/wife` 的 `disintegration/imaging` 等）。
2. **斜杠指令**用 `sdk.CommandPlugin(meta, "cmd", Handle)`，参数在 `c.Args`。
3. **中文/关键词触发**用 `sdk.EventPlugin(meta, sdk.MessageEvents, Handle)`，在 `Handle` 里用 `sdk.Text(c.Event)` + `sdk.MatchPrefix` / `MatchFull` / `MatchSuffix` / `MatchRegexp` 匹配。需要「仅 @ 机器人」时用 `sdk.Mentioned(c.Event)`。
4. **能力边界**：回复支持文本、图片（URL / `data:` / 本地文件）、Markdown、按钮和 @用户；事件带发送者昵称（QQ 群消息的 `author.username`）；管理能力支持撤回、禁言/解禁、踢人（群聊踢人受 QQ 白名单限制）。仍没有戳一戳和视频发送（语音可用 `sdk.ReplyVoice` / `sdk.ReplyVoiceBytes`）；QQ 群也没有成员撤回事件，因此 `autowithdraw` 无法移植。群聊 @ 用户仅 Markdown 消息支持，`sdk.ReplyAt` 会自动切换。
5. **持久状态**用 `sdk.WithPermissions("state.read", "state.write")`，读写成对使用 `sdk.LoadJSON` / `sdk.SaveJSON`。状态按插件隔离，跨插件共享数据要合并进同一个插件。
6. **HTTP** 使用 10 秒超时、`NewRequestWithContext`、`io.LimitReader` 限流响应体、校验状态码；上游 URL 用包级变量，便于用 `httptest` 测试。
7. **不搬大资源**：字体（11 MB+）、成语库（11 MB）和 sqlite 数据包不复制，改用公开 API、内嵌小词表或离线转换的小 JSON；只内嵌了压缩后约 1.3 MB 的扑克牌图、78 KB 的签文数据，以及王者营地约 1.6 MB 的卡片背景/图标。
8. 每个插件都要有 `plugin_test.go`，测试不得联网。

## SDK 补充

移植过程中发现框架 SDK 缺少通用的触发匹配与状态读写辅助，已在框架 `sdk/` 补齐（不影响既有插件）：

- `sdk.MessageEvents`：群消息、群 @ 消息、私聊的统一订阅集合
- `sdk.Text` / `sdk.Mentioned`：去 @ 后的文本、是否被 @
- `sdk.MatchPrefix` / `MatchFull` / `MatchSuffix` / `MatchRegexp`
- `sdk.LoadJSON` / `sdk.SaveJSON`：State 的 JSON 读写，缺键不报错
- `sdk.ReplyAt` / `sdk.MentionUserIDs`：回复 @ 用户、提取消息中被 @ 的用户 ID
- `sdk.ReplyImageBytes` / `sdk.ReplyImageFile`：发送内存图片或本地图片（≤5 MiB）
- `sdk.ReplyVoice` / `sdk.ReplyVoiceBytes`：发送语音（silk/wav/mp3/flac，≤5 MiB；频道不支持）
- `sdk.ReplyKeyboard` / `sdk.ReplyKeyboardJSON`，配合 `sdk.NewKeyboard` / `NewRow` / `CommandButton` / `CallbackButton` / `LinkButton`：发送 Markdown + 按钮键盘，发送前校验 QQ 的行列、文字与 data 长度限制
- `sdk.Recall` / `sdk.RecallMessage` / `sdk.Mute` / `sdk.MuteUser` / `sdk.Unmute` / `sdk.Kick`：群管动作，分别需要 `message.recall`、`group.manage` 权限
- `sdk.RenderHTML`：共享无头浏览器渲染 HTML 卡片并返回 JPEG
- `sdk.OpenBrowser`：独立浏览器会话，用于扫码登录、拦截 redirect、元素截图等交互

## 去重记录

<details>
<summary><b>留强删弱，共删 26 个</b></summary>

| 删除 | 保留对象 | 原因 |
|---|---|---|
| NanoBot 17 个子插件（base64gua/dish/fortune/genshin/wife/wordle 等） | ZeroBot 同名原版 | NanoBot 版是 ZeroBot 的移植子集 |
| NanoBot qunyou | ZeroBot llm / aichat | 依赖硬编码本地 llama2 二进制 |
| ZeroBot reborn | koishi toutai | toutai 有中国/世界两套 + 排行榜 + 记录 |
| ZeroBot translation | universal-translation | 后者多引擎多语种 |
| koishi translator-youdaoavailable | universal-translation | 仅中英有道，是其子集 |
| koishi bilibili-videolink-analysis | ZeroBot bilibili / bilibiliparse / bilibilipush | 后者三件套更全 |
| koishi image-upscale-bigjpg | ZeroBot realcugan | 本地 AI 放大，免 API 额度 |
| koishi jrys-prpr | ZeroBot fortune | 后者底图主题更多 |
| koishi steam-friend-status-fork | ZeroBot steam | 后者带订阅管理（本仓库改为手动拉取） |
| koishi transform-before-send | — | Koishi 平台基建，无功能移植价值 |

</details>

## 开发与测试

```sh
cd d:\ShizukuBot-plugins

# 本仓库只测试插件（需要同级框架仓库）
go test ./plugins/...
go vet ./plugins/...

# 复制插件到框架后，完整验证
cd ../shizukubot
go generate ./pluginregistry
go test ./sdk ./plugins/... ./pluginregistry
go build -o shizukubot .
```

测试约定：

- 每个插件都有 `plugin_test.go`，不访问网络；外部地址是包级变量，用 `httptest` 替换。
- 新增或修改插件后先跑本仓库测试，再在框架中生成注册表并构建。
- 新增插件请遵循上面的移植约定与命名规则。

## 相关仓库与上游

| 项目 | 说明 |
| --- | --- |
| [ShizukuBot](https://github.com/kylin930/ShizukuBot) | 插件运行的框架 |
| [ZeroBot-Plugin](https://github.com/FloatTech/ZeroBot-Plugin) | 主要上游，大部分插件与其对齐 |
| [NanoBot-Plugin](https://github.com/FloatTech/NanoBot-Plugin) | 官方 API 机器人插件合集 |
| [koishi-shangxue-apps](https://github.com/koishi-shangxue-plugins/koishi-shangxue-apps) | Koishi 赏雪插件合集 |
| [AstrBot](https://github.com/AstrBotDevs/AstrBot) | 参考的机器人框架 |
| [Yunzai-Bot](https://github.com/yoimiya-kokomi/Miao-Yunzai) | 终末地插件的上游 |

## 许可证

[Apache License 2.0](LICENSE)
