# ShizukuBot-plugins

ShizukuBot 的插件合集仓库。原始插件来自 ZeroBot-Plugin、NanoBot-Plugin、Koishi、AstrBot、Yunzai 等生态，去重后逐个移植为框架原生的 Go 插件包。

- `plugins/`：已移植、可直接编译的 Go 插件（33 个）
- `sources/`：原始源码，仅本地保留作移植参考，不参与编译、不纳入仓库（125 个）

## 快速使用

框架的插件是编译期注册的原生 Go 包，没有在线安装/热重载。使用步骤：

1. 复制插件目录到框架：`plugins/<分类>/<插件名>` → `d:\shizukubot\plugins\<插件名>`
2. 重新生成注册表并编译：

```powershell
cd d:\shizukubot
go generate ./pluginregistry
go build ./...
```

目录名会成为导入路径的一部分，包名必须是合法 Go 标识符（本仓库统一用去掉连字符的小写名，如 `verse-match` → `package versematch`）。插件 `Meta.ID` 在框架内必须唯一。

## 目录结构

```
plugins/
├── ai/            # 大模型 / AI 画图 / AI 声聊
├── audio/         # 语音合成 / 点歌
├── chat/          # 人格回复（已移植 1）
├── game/          # 棋牌 / 猜谜 / 群经济（已移植 4）
├── entertainment/ # 整活 / 占卜 / 投胎（已移植 12）
├── image/         # 图片生成 / 处理 / 搜图
├── query/         # 查询订阅（已移植 9）
├── tool/          # 加解密 / 翻译 / 图床（已移植 3）
└── admin/         # 群管 / 审核 / 撤回（已移植 4）

sources/（本地参考目录，已被 .gitignore 排除）
├── zerobot-plugin/      # FloatTech/ZeroBot-Plugin 骨架（kanban 等共享库）
├── nanobot-plugin/      # FloatTech/NanoBot-Plugin 骨架
├── koishi-shangxue-apps/# 赏雪 Koishi 合集骨架
└── standalone/          # 单仓库插件（AstrBot / Yunzai / Koishi）
```

## 已移植插件

| 插件 | 触发 | 功能 |
|---|---|---|
| `tool/ping` | `/ping`、`/ping <host>` | 回复 pong；带主机时做 TCP 连通延迟检测（无需 ICMP 权限） |
| `tool/bmi` | `/bmi <身高cm> <体重斤>` | 计算 BMI 并给出随机健康建议 |
| `tool/chrev` | `翻转 <英文>` | 倒序并替换为上下翻转字符（∀qɔ 等） |
| `chat/chat` | @机器人 空消息、`空调开`、`空调关`、`群温度`、`设置温度N` | 被叫应答与群空调小功能 |
| `admin/antiabuse` | 自动检测、`添加违禁词`、`删除违禁词`、`查看违禁词` | 违禁词检测与分群词表；命中后撤回并禁言 2 分钟，权限不足时降级为提醒 |
| `admin/breakrepeat` | 自动 | 连续复读超过 3 条时打乱原文后回复 |
| `admin/sleepmanage` | `晚安`、`早安` | 记录睡眠时间并输出群内排名 |
| `admin/manager` | `我的ID`、`撤回`、`我要自闭 10分钟`、`解除禁言我`、`禁言 @成员 10分钟`、`踢出 @成员 [并拉黑]` | 自我禁言、撤回本条；配置 `admin_users` 后执行成员禁言/解禁/踢出 |
| `entertainment/choose` | `选择 A 还是 B` | 选择困难症帮手 |
| `entertainment/curse` | `骂我`、`大力骂我`、@机器人 + 关键词 | 内置两档回击语录 |
| `entertainment/driftbottle` | `扔漂流瓶 <内容>`、`捞漂流瓶` | 漂流瓶投递与随机捞取 |
| `entertainment/emozi` | `抽象转写 <文本>`、`抽象还原 <文本>` | 中文与抽象颜文字互转（公开 API） |
| `entertainment/jandan` | `来份屌图`、`来份弔图`、`来份吊图` | 随机煎蛋网无聊图 |
| `entertainment/kfccrazythursday` | `疯狂星期四` | 随机疯狂星期四文案 |
| `entertainment/moyu` | `摸鱼提醒` | 当日摸鱼提醒与周末倒计时 |
| `entertainment/omikuji` | `求签`、`占卜` | 浅草寺签文（按用户与日期） |
| `entertainment/shadiao` | `哄我`、`渣我`、`来碗绿茶`、`发个朋友圈`、`来碗毒鸡汤`、`讲个段子`、`马丁路德骂我` | 沙雕 app 文案 |
| `entertainment/shindan` | `异世界转生`、`今天是什么少女`、`卖萌`、`今日老婆`、`黄油角色` | 测定结果转纯文本 |
| `entertainment/verse-match` | `对对联 <上联>` | 返回匹配下联（公开 API） |
| `entertainment/wtf` | `鬼东西列表`、`查询鬼东西N [@成员]` | 随机鬼东西测试；优先使用被 @ 的成员作为输入 |
| `game/handou` | `个人猜成语`、`团队猜成语` | 四字成语猜谜，每人七次机会 |
| `game/niuniu` | `注册牛牛`、`打胶`、`jj@用户`、`排行榜`、`商店`、`拍卖行` | 群内牛牛养成（state 持久化） |
| `game/poker` | `抽扑克`、`poker` | 随机抽一张牌（文本牌面） |
| `game/wordle` | `个人猜单词`、`团队猜单词` | 五/六/七阶英文猜词 |
| `query/dailynews` | `今日早报` | 今日早报图片 |
| `query/github` | `>github 关键词` | GitHub 仓库/用户查询 |
| `query/hitokoto` | `一言` | 随机一言与出处 |
| `query/hs` | `搜卡 卡名` | 炉石传说卡牌图片（最多 5 张） |
| `query/moyucalendar` | `摸鱼人日历` | 摸鱼人日历图片 |
| `query/nbnhhsh` | `?? <缩写>`、`？？<缩写>` | 拼音首字母缩写释义 |
| `query/warframeapi` | `地球时间`、`金星时间`、`火卫二时间`、`wf警报`、`wf仲裁`、`wf每日特惠` | Warframe 世界状态 |
| `query/ygocdb` | `/ydp 卡名`、`/yds 卡名`、`/ydb 卡名` | 游戏王卡图与效果 |
| `query/yujn` | `随机骚话`、`土味情话`、`随机污句子`、`随机美句` | 遇见 API 随机文本 |

尚未移植的原始插件仍保留在 `sources/` 对应目录中，每个目录即一个插件。

## 移植约定

1. **只用标准库 + `shizukubot/sdk`**，不引第三方依赖。
2. **斜杠指令**用 `sdk.CommandPlugin(meta, "cmd", Handle)`，参数在 `c.Args`。
3. **中文/关键词触发**用 `sdk.EventPlugin(meta, sdk.MessageEvents, Handle)`，在 `Handle` 里用 `sdk.Text(c.Event)` + `sdk.MatchPrefix` / `MatchFull` / `MatchSuffix` / `MatchRegexp` 匹配。需要「仅 @ 机器人」时用 `sdk.Mentioned(c.Event)`。
4. **能力边界**：回复支持文本、图片（URL / `data:` / 本地文件）、Markdown、按钮和 @用户；管理能力支持撤回、禁言/解禁、踢人（群聊踢人受 QQ 白名单限制）。仍没有昵称、戳一戳、视频/语音发送；QQ 群也没有成员撤回事件，因此 `autowithdraw` 无法移植。群聊 @ 用户仅 Markdown 消息支持，`sdk.ReplyAt` 会自动切换。
5. **持久状态**用 `sdk.WithPermissions("state.read", "state.write")`，读写成对使用 `sdk.LoadJSON` / `sdk.SaveJSON`。状态按插件隔离，跨插件共享数据要合并进同一个插件。
6. **HTTP** 使用 10 秒超时、`NewRequestWithContext`、`io.LimitReader` 限流响应体、校验状态码；上游 URL 用包级变量，便于用 `httptest` 测试。
7. **不搬大资源**：图片包、字体、sqlite 数据包一律不复制，改用公开 API 或内嵌小词表。
8. 每个插件都要有 `plugin_test.go`，测试不得联网。

本地编译校验：仓库根 `go.mod` 通过 `replace shizukubot => ../shizukubot` 指向同级框架仓库，因此在 `d:\ShizukuBot-plugins` 下可直接运行：

```powershell
go test ./plugins/...
```

## SDK 补充

移植过程中发现框架 SDK 缺少通用的触发匹配与状态读写辅助，已在 `d:\shizukubot\sdk\match.go` 补齐（不影响既有插件）：

- `sdk.MessageEvents`：群消息、群 @ 消息、私聊的统一订阅集合
- `sdk.Text` / `sdk.Mentioned`：去 @ 后的文本、是否被 @
- `sdk.MatchPrefix` / `MatchFull` / `MatchSuffix` / `MatchRegexp`
- `sdk.LoadJSON` / `sdk.SaveJSON`：State 的 JSON 读写，缺键不报错
- `sdk.ReplyAt` / `sdk.MentionUserIDs`：回复 @ 用户、提取消息中被 @ 的用户 ID
- `sdk.ReplyImageBytes` / `sdk.ReplyImageFile`：发送内存图片或本地图片（≤5 MiB）
- `sdk.Recall` / `sdk.RecallMessage` / `sdk.Mute` / `sdk.MuteUser` / `sdk.Unmute` / `sdk.Kick` 等：群管动作，分别需要 `message.recall`、`group.manage` 权限

## 去重记录（留强删弱，共删 26 个）

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
| koishi steam-friend-status-fork | ZeroBot steam | 后者带订阅播报 |
| koishi transform-before-send | — | Koishi 平台基建，无功能移植价值 |
