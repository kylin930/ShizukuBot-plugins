# 移植对照检查报告

## 检查范围

- 本仓库 `plugins/` 下 93 个 Go 插件，对照 `sources/` 中的 ZeroBot-Plugin、NanoBot-Plugin、Koishi（赏雪合集）、AstrBot、Yunzai / standalone 上游实现。
- 本报告合并 5 份审计结果（娱乐上半、娱乐下半、查询、工具+llm、游戏/语音/聊天/管理），去重后逐条复核；不修改任何代码。
- 93 个插件中，`entertainment/toutai` 不在五份审计范围（见 README「独立上游移植插件」），`game/endfield` 由主代理另行核对且无明细条目；其余 91 个在五份明细中逐条比对。

## 方法与口径

按五个维度对照：

1. **指令覆盖**：上游注册的触发词/正则、参数形式、权限限定是否在本仓库存在。
2. **回复文案**：关键文案、@、引用、消息条数、图文顺序。
3. **功能/数据/概率**：算法、概率、词库/图库规模、分页/选择交互、种子与缓存。
4. **配置**：默认开关、可配置项、凭证保存方式。
5. **权限**：超级用户、机器人管理员、群管理员、普通群成员四档是否一致。

级别定义：

- **P0**：缺失指令或核心功能，用户按上游文档无法完成主要用法。
- **P1**：行为/数据/概率/权限不一致，或关键回复语义错误；功能存在但结果与上游不等价。
- **P2**：文案、参数、上限、交互方式明显不同，但主流程可用。
- **P3**：仅措辞、顺序、标点或体验增强。

证据格式为 `文件:行`；`已核实` 表示主代理已直接阅读上下游源码确认。README 已明确声明的平台限制不重复计为移植缺陷，单独列入「已文档化限制」。

## 总览

| 簇 | 插件数 | P0 | P1 | P2 | P3 | 合计 |
|---|---:|---:|---:|---:|---:|---:|
| 娱乐上半（bookreview…jandan） | 14 | 0 | 3 | 26 | 15 | 44 |
| 娱乐下半（kfccrazythursday…wife） | 13 | 0 | 2 | 12 | 11 | 25 |
| 查询（query） | 24 | 7 | 9 | 15 | 11 | 42 |
| 工具 + llm | 18 | 1 | 6 | 20 | 5 | 32 |
| 游戏/语音/聊天/管理 | 23 | 8 | 10 | 19 | 2 | 39 |
| **全量合计** | **92** | **16** | **30** | **92** | **44** | **182** |

统计修正说明：

- 五份原始文件逐条计数为 **P0 16、P1 30、P2 92、P3 44，共 182 条**；任务给的「P2 93、P3 43」与逐条统计相差 1 条（总数同为 182），本表以原始文件逐条标签为准。
- 簇表插件数合计 92（含无明细条目的 `game/endfield`）；再加未列入任何簇的 `entertainment/toutai`，共 93 个；五份明细实际逐条覆盖 91 个。

按性质拆分：

| 级别 | 移植缺陷 | 宿主能力限制 | 已文档化限制 | 待确认 | 合计 |
|---|---:|---:|---:|---:|---:|
| P0 | 10 | 5 | 0 | 1 | 16 |
| P1 | 24 | 5 | 1 | 0 | 30 |

## P0 清单

| 插件 | 结论 | 上游证据 | 本仓库证据 | 性质 | 建议 |
|---|---|---|---|---|---|
| `query/bilibilipush` | 缺 `取消b站动态订阅`、`取消b站直播订阅`，只能整体取消订阅（已核实） | `sources/zerobot-plugin/plugin/bilibilipush/bilibilipush.go:114-146` 两条正则，分别回「已取消X的动态/直播订阅」 | `plugins/query/bilibilipush/plugin.go:125-129` 只有 `取消b站订阅`；`Handle` 105-145 无动态/直播分支 | 移植缺陷 | 补两条只清对应开关的指令与文案 |
| `query/bilibilipush` | 缺 `开启艾特全体` / `关闭艾特全体`，直播推送不 @全体（已核实） | `bilibilipush.go:61-77` 回「已开启艾特全体Oo」；`:402-404` 推送时 `message.AtAll()` | `plugins/query/bilibilipush/plugin.go:106-146` 无触发；`pull` 346-423 无 @全体逻辑 | 移植缺陷 | 增加群级开关，拉取直播更新时按开关 @全体 |
| `query/steam` | 缺 `查看apikey`（私聊 + 超级用户）（已核实） | `sources/zerobot-plugin/plugin/steam/listenter.go:42-46` 回「apikey为: xxx」 | `plugins/query/steam/plugin.go:80-110` 无该分支 | 移植缺陷 | 补指令，仅私聊管理员，输出掩码 |
| `query/warframeapi` | 缺 `.wm [物品]` 市场查价（模糊搜索、编号选择、满级报价、生成 `/w` 文案）（已核实） | `sources/zerobot-plugin/plugin/warframeapi/main.go:236-381` | `plugins/query/warframeapi/plugin.go:74-88` 只分发 `wf警报/仲裁/每日特惠/平原时间` | 移植缺陷 | 接入 warframe.market 物品表与订单接口 |
| `query/hs` | 缺卡组代码 `[AAE…]` 解析与卡组图（已核实） | `sources/zerobot-plugin/plugin/hs/run.go:88-94` 正则匹配后调用 `kz()` | `plugins/query/hs/plugin.go:59-87` 只处理 `搜卡` | 移植缺陷 | 补正则并调用 `mod=general_deck_image` |
| `query/yujn` | 缺 `御姐撒娇`、`绿茶语音`、`怼人语音` 与 `让lulu说…`（已核实）；21 个视频指令另属宿主无视频发送 | `sources/zerobot-plugin/plugin/yujn/yujn.go:102-106` 三条 `message.Record`；`:128-142` 生成语音；`:97` 21 个视频 | `plugins/query/yujn/plugin.go:17-22` 仅 4 个文本端点；`Handle` 42-56 无语音分支 | 移植缺陷 | 补 3 条语音与 TTS 调用；视频部分列入宿主限制 |
| `query/hitokoto` | 缺 `系列一言`（分类序号图、120 秒选号、按分类随机 10 条）（已核实） | `sources/zerobot-plugin/plugin/hitokoto/hitokoto.go:88-173` | `plugins/query/hitokoto/plugin.go:39-55` 仅 `一言` 随机一条 | 移植缺陷 | 改为文本序号列表 + 限时选择态 |
| `tool/wordcount` | 缺 `热词 [N]` 入口与统计（已核实） | `sources/zerobot-plugin/plugin/wordcount/main.go:41` Help；`:55` `^热词\s?(\d*)$`，`:143` 出 top20 柱状图 | `plugins/tool/wordcount/plugin.go:19-55` 只有 `字数/字数统计/词频` | 移植缺陷 | 先补 `热词` 入口与提示；宿主提供历史消息接口后实现统计 |
| `game/gloryofkings` | 缺 `#排位排名`、`#巅峰排名`、`#排位总排名`、`#巅峰总排名`、`#排位bot排名` 等全部排名指令 | `sources/standalone/glory-of-kings/apps/rankList.js:60-62` | `plugins/game/gloryofkings/plugin.go:93-117` 无排名分支；全目录无「排名」实现 | 移植缺陷 | 先补 bot/总排名；群排名依赖群成员列表（见宿主限制） |
| `game/gloryofkings` | 缺 `#群日报/群周报/群月报`、开启/关闭推送、`#群报状态`、`#战绩推送状态` | `sources/standalone/glory-of-kings/apps/groupReport.js:74-81`；`gameRecordPush.js:188-192` | `plugins/game/gloryofkings/plugin.go:93-117` 无分支；`signin.go:4` 注释「不做定时任务/主动推送」 | 宿主能力限制 | 补可被动查询的状态指令；定时推送在 README 标注 |
| `game/gloryofkings` | 缺 `#营地共享库`、`#营地消息`、`#营地回复`、`#营地好友`、`#营地私聊` 等多 bot 共享/私聊扩展 | `sources/standalone/glory-of-kings/apps/shareBind.js:50-56`；`campIm.js:387-395`；`campFriend.js:60-61` | `plugins/game/gloryofkings/plugin.go` 无对应指令，`helpText` 648-672 也未列出 | 待确认 | 先确认营地协议与多 bot 共享是否仍可用，再决定移植或 README 单列 |
| `admin/manager` | 缺 `升为管理/取消管理/开启全员禁言/解除全员禁言/修改名片/修改头衔/申请头衔/退出群聊`（已核实） | `sources/zerobot-plugin/plugin/manager/manager.go:101-261` | `plugins/admin/manager/plugin.go:58-90` 仅 `我的ID/撤回/解除禁言我/我要自闭/禅定/禁言/解除禁言/踢出` | 宿主能力限制 | 逐项对照 SDK；可做的补，需平台管理员/群名片/退群 API 的列 README |
| `admin/manager` | 缺 `在…时提醒大家…`、cron 提醒、`取消在…的提醒`、`列出所有提醒`（已核实） | `manager.go:296-361` | `plugins/admin/manager/plugin.go` 全文无提醒/定时器实现 | 宿主能力限制 | 保存规则，待宿主调度；README 标注 |
| `admin/manager` | 缺入群欢迎、退群提醒、设置/测试欢迎语与告别辞、入群验证、gist 自动审批（已核实） | `manager.go:448-618` | `plugins/admin/manager/plugin.go` 无 notice 订阅与欢迎语状态 | 宿主能力限制 | README 注明；宿主提供入群/退群/审批事件后再补 |
| `admin/manager` | 缺群聊/私聊转发、翻牌、赞我、群签到、回应表情、设置/取消精华、精华列表（已核实） | `manager.go:270-295`、`362-447`、`646-711` | `plugins/admin/manager/plugin.go:50-91` 无对应分支 | 宿主能力限制 | 可做的群签到先补；依赖原始 OneBot API 的（转发/赞/回应表情/精华）统一说明 |
| `game/handou` | 缺 `猜成语热门 汉字/成语`，无法查看热门 Top10（已核实） | `sources/zerobot-plugin/plugin/handou/game.go:119-127` | `plugins/game/handou/plugin.go:22` 只有 `个人/团队猜成语` 两条 | 移植缺陷 | 用现有猜词统计补 Top10 |

## P1 清单

| 插件 | 结论 | 上游证据 | 本仓库证据 | 性质 | 建议 |
|---|---|---|---|---|---|
| `entertainment/drawlots` | 上游支持 `加XX签` / `删XX签`（超管 + 图包），本仓库明确拒绝增删（已核实） | `sources/zerobot-plugin/plugin/drawlots/main.go:121,150`，成功回「成功！」`:148/168` | `plugins/entertainment/drawlots/plugin.go:122-128` 回「图包抽签已改为内置文本签…」「内置签文不能删除哦~」 | 移植缺陷 | 保留入口并给出同义失败原因，或在 README 明确不支持 |
| `entertainment/emozi` | 缺 `抽象登录`（私聊密码、持久化凭证、每小时续登） | `sources/zerobot-plugin/plugin/emozi/main.go:100,109`；`main.go:29-43` 每小时续登 | `plugins/entertainment/emozi/plugin.go:17,51-58` 固定 `apiBase` 匿名调用 | 移植缺陷 | 补登录与凭证持久化，失败回退匿名 |
| `entertainment/funny` | `讲个笑话/夸夸` 上游为前缀匹配（可带 @/人名），本仓库要求整句完全相等 | `sources/zerobot-plugin/plugin/funny/laugh.go:34` `OnPrefixGroup`；Help `:30` 含 `[@xxx/qq号/人名]` | `plugins/entertainment/funny/plugin.go:50-53` 使用 `MatchFull` | 移植缺陷 | 改为前缀匹配，忽略后续参数 |
| `entertainment/moyu` | 缺元旦/春节/清明/劳动/端午/中秋/国庆 7 个节日倒计时（已核实） | `sources/zerobot-plugin/plugin/moyu/run.go:41-53`；格式见 `nowork.go:44-53` | `plugins/entertainment/moyu/plugin.go:25-33` 只输出日期、摸鱼文案与周末倒计时 | 移植缺陷 | 内置当年节假日表或改为可配置数据 |
| `entertainment/tarot` | 丢弃「询问的事情」，无大模型解读；牌阵问题同样被忽略（已核实） | `sources/zerobot-plugin/plugin/tarot/tarot.go:129-214`、`291-410` | `plugins/entertainment/tarot/plugin.go:203-205` 只取 `match[1]/match[2]`；`:277-312` 忽略问题 | 移植缺陷 | 补追问解读（可配置 AI），或在帮助/README 明确不支持 |
| `query/rsshub` | 上游指令名为 `添加rsshub订阅-`/`删除rsshub订阅-`/`查看rsshub订阅列表`/`rsshub同步`，本仓库全部改为 `rss…`（已核实） | `sources/zerobot-plugin/plugin/rsshub/main.go:61-112` | `plugins/query/rsshub/plugin.go:83-93` 使用 `rss订阅列表/添加rss订阅/删除rss订阅/rss同步` | 移植缺陷 | 增加 `rsshub*` 原名别名 |
| `query/rsshub` | 推送仅「源名 + 条目标题 + 链接」，每源最多 3 条，无正文/图片；上游发完整条目转发节点 | `sources/zerobot-plugin/plugin/rsshub/main.go:146-149` | `plugins/query/rsshub/plugin.go:266-277` | 移植缺陷 | 至少补条目摘要与首图 |
| `query/bilibilipush` | 动态更新只回「名称 更新了动态：标题 + 链接」，上游发送完整动态卡片（正文/图片） | `sources/zerobot-plugin/plugin/bilibilipush/bilibilipush.go:332-350` | `plugins/query/bilibilipush/plugin.go:399` | 移植缺陷 | 补正文首段与图片，或调用动态详情接口 |
| `query/steam` | 订阅增删上游群成员即可，本仓库要求 `admin_users` | `sources/zerobot-plugin/plugin/steam/steam.go:42,84` 仅 `zero.OnlyGroup` | `plugins/query/steam/plugin.go:172,217` 要求 `isAdmin` | 移植缺陷 | 恢复群成员可用，或至少接受群管理员 |
| `query/warframeapi` | 缺 `wf时间同步` 与本地时间模拟 | `sources/zerobot-plugin/plugin/warframeapi/main.go:225-234` | `plugins/query/warframeapi/plugin.go:74-88` 无该触发 | 移植缺陷 | 补同步入口并返回同步结果/时间 |
| `query/hitokoto` | `一言 关键词/分类` 参数被丢弃，上游按关键词/分类筛选本地库（已核实） | `sources/zerobot-plugin/plugin/hitokoto/hitokoto.go:42-46` | `plugins/query/hitokoto/plugin.go:40-43` 命中前缀后直接请求随机接口 | 移植缺陷 | 支持分类/关键词过滤 |
| `query/ygocdb` | 多结果直接取第一条，无列表、数字选择、`下一页`、`取消` | `sources/zerobot-plugin/plugin/ygocdb/ygocdb.go:85-162` | `plugins/query/ygocdb/plugin.go:80-92` 直接 `result.Result[0]` | 移植缺陷 | 补 10 条列表 + 20 秒限时选择/翻页 |
| `query/wantquotes` | 无 17 分类选择交互，未带序号时默认「现」；上游先发分类图再限时选号 | `sources/zerobot-plugin/plugin/wantquotes/wantquotes.go:167-203` | `plugins/query/wantquotes/plugin.go:147-171` | 移植缺陷 | 用按钮或限时状态恢复分类选择 |
| `query/hyaku` | 缺上下句平假名读音两行 | `sources/zerobot-plugin/plugin/hyaku/main.go:24-49` 每条 CSV 6 字段全部输出 | `plugins/query/hyaku/poems.json:2-7` 仅 4 字段；`plugin.go:82-89` 输出 4 行 | 移植缺陷 | 从上游 CSV 离线转一份内嵌数据 |
| `tool/base16384` | 带密钥加密与上游不兼容：上游用原始 key 补齐/截断 16 字节 + gofastTEA 自定义填充/链式加密，本仓库用 md5(key)+PKCS#7（已核实） | `sources/zerobot-plugin/plugin/base16384/main.go:43`；`floatbox/crypto/tea.go:10-21`；`gofastTEA/tea_1.17.go:11-31` | `plugins/tool/base16384/plugin.go:262-320` | 移植缺陷 | 复刻 floatbox/gofastTEA，并补上游密文测试向量 |
| `tool/chouxianghua` | 词表由上游整库降为 96 条，且无「拼音/双字拼音」匹配逻辑，多数中文原样返回 | `sources/zerobot-plugin/plugin/chouxianghua/model.go:16-29`；`chouxianghua.go:27-29,59` | `plugins/tool/chouxianghua/dict.json:1-96`；`plugin.go:84-106` 仅字面最长匹配 | 移植缺陷 | 导入上游 pinyin/emoji 表，按拼音与双字拼音匹配 |
| `tool/imagesave` | 不支持保存「被回复消息」中的图片；上游优先读 `session.quote.content` | `sources/koishi-shangxue-apps/plugins/image-save-path/lib/index.js:417-448` | `plugins/tool/imagesave/plugin.go:277-285` 只取当前消息；`:300-308` 无引用分支 | 宿主能力限制 | 从 `Event.Raw` 解析被引用消息，或由宿主补 quote 字段 |
| `tool/ping` | 用 TCP 443/80 建连代替系统 `ping -n 4`，无 4 包统计、丢包率、RTT 明细 | `sources/standalone/ping/src/utils/PingUtils.ts:8-25` | `plugins/tool/ping/plugin.go:43-67` 仅 TCP 建连耗时 | 已文档化限制 | README 已说明 TCP 探测；如要对齐则改为执行系统 ping |
| `tool/crypter` | 缺 `qq加密` / `qq解密` 两条 face 段指令 | `sources/zerobot-plugin/plugin/crypter/main.go:21-23,39-40` | `plugins/tool/crypter/plugin.go:4-5` 注释未移植 face；`:20-24` 仅齁语/fumo 四前缀 | 宿主能力限制 | 宿主暴露 Raw face 段后补 |
| `ai/llm` | `/gpt` 不支持读取被回复消息内容，只取 `/gpt` 后文本（已核实） | `sources/zerobot-plugin/plugin/llm/main.go:145-181` | `plugins/ai/llm/plugin.go:222-225`；`:272-307` 无引用分支 | 宿主能力限制 | 从 `Event.Raw` 解析引用正文，或宿主补 quote |
| `game/gloryofkings` | 缺 `#谁在打游戏/#王者在线列表/#在线列表` 与 `#王者用户统计/#清理失效营地账号/#隐藏主页名单/#清除隐藏主页` | `sources/standalone/glory-of-kings/apps/whoIsPlaying.js:121`；`accountManager.js:126-147` | `plugins/game/gloryofkings/watch.go:84-92` 仅本人在线状态；`plugin.go:648-672` 帮助无 | 宿主能力限制 | 保留单人查询并在帮助说明群名单不可用 |
| `game/guessmusic` | `提示` 改为文字提示（歌名 N 字、首字），每题一次；上游是再听下一段音频（最多 2 次） | `sources/zerobot-plugin/plugin/guessmusic/guessmusic.go:293-298` | `plugins/game/guessmusic/plugin.go:309-341` | 移植缺陷 | 接回音频分段逻辑，或改 README 描述 |
| `game/handou` | 词库仅 107 条，合法四字成语会被判「不存在」；上游下载完整 `idiom.json` 并有 API 补词（已核实） | `sources/zerobot-plugin/plugin/handou/game.go:77-82`、`288-305` | `plugins/game/handou/plugin.go:307-415`；`:90-93` 回「你确定存在这样的成语吗？」 | 移植缺陷 | 扩大词库或接公开成语接口 |
| `game/niuniu` | 赎牛牛无上游的二次确认，错误文案也不同 | `sources/zerobot-plugin/plugin/niuniu/main.go:217-242` | `plugins/game/niuniu/plugin.go:366-407` 直接赎回 | 移植缺陷 | 补「是/否」确认会话并对齐文案 |
| `game/niuniu` | 注销免费；上游按 24h 内次数扣 `count*50` 钱包币，余额不足拒绝 | `sources/zerobot-plugin/plugin/niuniu/main.go:400-425` | `plugins/game/niuniu/plugin.go:586-600` 直接删除用户 | 移植缺陷 | 补注销费用与递增规则 |
| `game/wordle` | 题库仅每阶约百词；上游来自 CET-4 词库并有词典校验（已核实） | `sources/zerobot-plugin/plugin/wordle/wordle.go:82-121,193,221-222` | `plugins/game/wordle/plugin.go:267-299` | 移植缺陷 | 扩充到 CET-4 规模并在 README 注明 |
| `audio/ahsai` | 编号使用排序后的列表；上游编号按原始 `namelist` 顺序，同一序号指向不同音源（已核实） | `sources/zerobot-plugin/plugin/ahsai/ahsai.go:21-27,68-93` | `plugins/audio/ahsai/plugin.go:22-31,172-178` 先 `sort.Strings` 再编号 | 移植缺陷 | 改回原始顺序，并修正 README「与上游一致」的错误表述 |
| `chat/atri` | 缺 1:00–5:59 全部触发静默 | `sources/zerobot-plugin/plugin/atri/atri.go:42-48,68` | `plugins/chat/atri/plugin.go:41-83` 无时段门禁 | 移植缺陷 | 补凌晨静默 |
| `admin/antiabuse` | 添加/删除违禁词无管理员校验，任何群成员可改词表 | `sources/zerobot-plugin/plugin/antiabuse/anti.go:105,115` 使用 `AdminPermission` | `plugins/admin/antiabuse/plugin.go:49-57` 无 `admin_users` 校验 | 移植缺陷 | 增删加管理员校验 |
| `admin/antiabuse` | 命中仅撤回 + 群禁言 2 分钟，无 bot 级封禁与自动解封 | `sources/zerobot-plugin/plugin/antiabuse/anti.go:79-96` `Manager.DoBlock` + `onDel` 解封 | `plugins/admin/antiabuse/plugin.go:66-79` | 宿主能力限制 | README 说明仅做群禁言 |

## P2 / P3 摘要

路径缩写：`Z/` = `sources/zerobot-plugin/plugin/`，`K/` = `sources/koishi-shangxue-apps/plugins/`，`S/` = `sources/standalone/`，`P/` = `plugins/`。

### 娱乐上半（14）

- **bookreview**（P2×2）：未命中上游仍渲染空书评图，本仓库回「没有找到…」（`Z/bookreview/book_review.go:51-62` ↔ `P/entertainment/bookreview/plugin.go:135-142`）；上游只在评语字段检索，本仓库检索书名/作者/评语（`Z/bookreview/model.go:13-16` ↔ `P/entertainment/bookreview/plugin.go:156-166`）。P3：卡片额外渲染书名/作者/品牌（`Z/.../book_review.go:54` ↔ `P/.../plugin.go:80-103`）。
- **choose**（P2×1）：缺首行 `> 昵称`（`Z/choose/choose.go:32-36` ↔ `P/entertainment/choose/plugin.go:37`）。P3：新增「选项太少」提示、丢弃空选项（`Z/.../choose.go:25-30,26-29` ↔ `P/.../plugin.go:30-32,40-48`）。
- **cpstory**（P2×2）：上游从 @ 消息提取两个 QQ 号，本仓库只解析文本；磕cp 不足两人文案不同（`Z/cpstory/cpstory.go:50-53,63-64` ↔ `P/entertainment/cpstory/plugin.go:40-60,54-55`）。P3：缺 `OnlyGroup`；占位符由 `<攻>/<受>` 改为 `{攻}/{受}`（`Z/.../cpstory.go:50,54-57,71` ↔ `P/.../plugin.go:36,89-91`）。
- **curse**（P2×3）：上游默认禁用、有限频与 1–2 秒延迟、远程两级语料；本仓库默认启用、无限频无延迟、内置 15+15 条（`Z/curse/curse.go:26,58-66` ↔ `P/entertainment/curse/plugin.go:59-66,69-81,14-48`）。P3：不引用原消息（`Z/.../curse.go:61,67,73` ↔ `P/.../plugin.go:73-75`）。
- **diana**（P2×4）：权限由 `AdminPermission` 改为 `admin_users`；新增 8–500 字限制；成功文案「记住啦!」→「学会啦…」；语料由远程 DB 改为内置 10+1 篇（`Z/diana/bing.go:47-54,53,35-45` ↔ `P/entertainment/diana/plugin.go:87-89,91-94,101,22-26,66-69`）。
- **dish**（P2×2）：上游远程全量菜谱，本仓库 20 道内置；缺少「客官」「辣」标志文案（`Z/dish/dish.go:39-52,84-88` ↔ `P/entertainment/dish/plugin.go:14-34,79-85`）。P3：空参数提示、随机失败文案（`Z/.../dish.go:67-76,80,100` ↔ `P/.../plugin.go:54-56,70`）。
- **driftbottle**（P2×2）：只存 `user_id`，回复无昵称/QQ；捞瓶无限频（`Z/driftbottle/main.go:58,49` ↔ `P/entertainment/driftbottle/plugin.go:30-36,101-102,54`）。P3：半角括号文案（`Z/.../main.go:68` ↔ `P/.../plugin.go:69`）。
- **drawlots**（P2×3）：签池固定 7 类文本；「看XX签」无权限；每次重新随机而非按用户+日期（`Z/drawlots/main.go:36-54,107,268-275` ↔ `P/entertainment/drawlots/plugin.go:28-83,114-120,153-155`）。
- **emojimix**（P2×1）：不处理 QQ `face` 段，仅文本 emoji（`Z/emojimix/mix.go:54-65` ↔ `P/entertainment/emojimix/plugin.go:42-59`）。P3：先去空白再判 2 个 rune，`😀 😀` 也触发（`Z/.../mix.go:67-79` ↔ `P/.../plugin.go:43-50`）。
- **emozi**（P2×1）：多音字无逐字选择，统一取首读音（`Z/emozi/main.go:67-87` ↔ `P/entertainment/emozi/plugin.go:71-77`）。
- **fortune**（P2×2）：缺 `设置底图` 与 20 种底图；签文由上游 `text.json` 改为自造评分模型（`Z/fortune/fortune.go:41,66,144-145` ↔ `P/entertainment/fortune/plugin.go:54-62,19-52,97-110`）。P3：新增「同人同日固定」说明（`Z/.../fortune.go:82,88` ↔ `P/.../plugin.go:113-129`）。
- **funny**（P2×1）：笑话与夸夸由共用 `jokes` 表改为 `jokes.json`+`praises.json`（`Z/funny/laugh.go:34-67` ↔ `P/entertainment/funny/plugin.go:14-33,52-53`）。P3：昵称来源 `NickName` → `Event.Nickname`（`Z/.../laugh.go:60` ↔ `P/.../plugin.go:59-64`）。
- **idiom**（P3×2）：空参数上游实际无输出；异常分支上游 `.try_faild` 拼写错误，本仓库统一「查询失败」（`K/idiom-dictionary/lib/index.js:123-125,76-83,149-151` ↔ `P/entertainment/idiom/plugin.go:108-112,118-120`）。
- **jandan**（P2×2）：缺 `更新[屌弔吊]图`；图源从全站历史库缩为首页列表（`Z/jandan/jandan.go:56-68,115`、`data.go:17-25` ↔ `P/entertainment/jandan/plugin.go:44-59,69-91`）。P3：正则由未锚定改为整句匹配（`Z/.../jandan.go:56` ↔ `P/.../plugin.go:29,47`）。

### 娱乐下半（13）

- **moyu**（P3×1）：上游默认禁用、Help 提供启用/禁用；本仓库注册即启用（`Z/moyu/run.go:21-29` ↔ `P/entertainment/moyu/plugin.go:16-23`）。
- **omikuji**（P2×2）：求签未 @ 用户且图片分条发送；解签改为纯文本（`Z/omikuji/sensou.go:44-48,78-88` ↔ `P/entertainment/omikuji/plugin.go:61-66,52-56`）。P3：签号种子由 `RandSenderPerDayN` 改为 FNV（`Z/.../sensou.go:33,80` ↔ `P/.../plugin.go:75-79`）。
- **score**（P2×4）：`设置签到预设` 上游超管，本仓库 `admin_users` 为空时放行所有人（`Z/score/sign_in.go:271` ↔ `P/entertainment/score/plugin.go:264-286`，已核实）；钱包排名只统计签到用户（`Z/wallet/wallet.go:70-76` ↔ `P/.../plugin.go:339`）；排名无当日缓存（`Z/score/sign_in.go:199-205`、`wallet.go:63-68` ↔ `P/.../plugin.go:316-331,335-351`）；缺 `设置硬币名称/管理钱包余额/钱包转账`（`Z/wallet/wallet.go:142,154,211` ↔ `P/.../plugin.go:110-124`）。P3：排名标题不同（`Z/.../sign_in.go:247`、`wallet.go:118` ↔ `P/.../plugin.go:331,350`）。
- **shadiao**（P3×2）：错误前缀 `ERROR:` → 「获取失败」；7 个接口均无限流（`Z/shadiao/shadiao.go:39,43`、`sweetnothings.go:14,17` ↔ `P/entertainment/shadiao/plugin.go:75,67-80`）。
- **shindan**（P3×1）：上游忽略触发词后文本，本仓库把它当作被测定名字（`Z/shindan/shindan.go:26-37,47-49` ↔ `P/entertainment/shindan/plugin.go:65-72`）。
- **tarot**（P2×1）：牌义为手写 78 条且无 `imgUrl`，上游来自 `tarots.json`（`Z/tarot/tarot.go:95-111` ↔ `P/entertainment/tarot/plugin.go:51-139`）。P3：小阿卡纳本仓库含 77 号权杖国王（`Z/.../tarot.go:155-158` ↔ `P/.../plugin.go:178-186`）。
- **verse-match**（P2×3）：缺 `verse-match` 主命令；无「正在对联中...」；未匹配与请求失败统一报错（`K/verse-match/lib/index.js:9-10,17,25-30` ↔ `P/entertainment/verse-match/plugin.go:38,37-52,49-51,77-80`）。
- **wife**（P3×2）：图片与文案分两条；猜老婆无用户级限流（`Z/wife/main.go:78-87`、`wifegame.go:93-97,31` ↔ `P/entertainment/wife/plugin.go:157-166,87-99`）。
- **wtf**（P2×1）：@ 成员时用 QQ 号而非群昵称（`Z/wtf/main.go:45-49` ↔ `P/entertainment/wtf/plugin.go:154-156`）。P3：纯文本不解析 CQ 码（`Z/.../main.go:61` ↔ `P/.../plugin.go:164`）。
- **xhstext**（P2×1）：语料缩为 12+12 条（`Z/xhstext/xhstext.go:65,76` ↔ `P/entertainment/xhstext/plugin.go:13-24`）。P3：无限流（`Z/.../xhstext.go:63,74` ↔ `P/.../plugin.go:46-54`）。
- **kfccrazythursday / pig / tiangou**：见「无差异」清单。

### 查询（24）

- **bilibilipush**（P2×1）：缺 `b站推送列表`/`拉取b站推送` 别名，列表为纯文本且不显示动态/直播开关（`Z/bilibilipush/bilibilipush.go:149,155-179,183` ↔ `P/query/bilibilipush/plugin.go:114,117,334-341`）。
- **steam**（P2×2）：缺 `steam查询订阅`/`拉取steam订阅`/`steam绑定 api key` 别名；列表纯文本，且强制 32 位 hex key（`Z/steam/steam.go:121`、`listenter.go:30,33,47` ↔ `P/query/steam/plugin.go:84,86,89,262-266,128`）。
- **warframeapi**（P2×1）：无 5 分钟本地时间模拟，每次重新请求（`Z/warframeapi/main.go:32-67` ↔ `P/query/warframeapi/plugin.go:106-143`）。
- **hitokoto**（P2×1）：上游最多 10 条逐条发送，本仓库只发 1 条（`Z/hitokoto/hitokoto.go:55-83` ↔ `P/query/hitokoto/plugin.go:51-54`）。
- **ygotrade**（P2×2）：上游遍历全部条目，本仓库按价格排序后硬截断 5 条；卡店字段把稀有度当「品相」并省略卖家名（`Z/ygotrade/ygotrade.go:64-73,99-106` ↔ `P/query/ygotrade/plugin.go:113-132,30,215-241,252-258`）。
- **ymgal**（P2×1）：`更新gal` 只统计可抓取数量，不落库缓存（`Z/ymgal/ymgal.go:61-70`、`model.go:169-223` ↔ `P/query/ymgal/plugin.go:181-190`、`picset.go:157-176`）。P3：关键词长度限制放宽（`Z/.../ymgal.go:48` ↔ `P/.../plugin.go:50`）。
- **bilibili**（P2×3）：`设置b站cookie` 不再落盘；VUP 上限 30（上游 50）；每场弹幕仅 8 条（`Z/bilibili/bilibili.go:541-550,177-180,426-518` ↔ `P/query/bilibili/plugin.go:561-566,27,332-335,29,472-479`）。P3：更新完成文案（`Z/.../bilibili.go:560` ↔ `P/.../plugin.go:557`）。
- **tracemoe**（P2×1）：上游关键词出现在句中即可，本仓库要求前缀开头（`Z/tracemoe/moe.go:23` ↔ `P/query/tracemoe/plugin.go:65-75`）。P3：额外输出中文名/罗马音/英文名/相似度等（`Z/.../moe.go:43-48` ↔ `P/.../plugin.go:170-211`）。
- **wantquotes**（P2×1）：上游返回全部句子，本仓库最多 8 条并合并成一条（`Z/wantquotes/wantquotes.go:97-108` ↔ `P/query/wantquotes/plugin.go:26,118-120`）。
- **nbnhhsh**（P2×1）：空结果上游输出「xx: 」，本仓库回「没有找到释义」（`Z/nbnhhsh/nbnhhsh.go:25` ↔ `P/query/nbnhhsh/plugin.go:52-54`）。P3：错误文案（`Z/.../nbnhhsh.go:25,50-51` ↔ `P/.../plugin.go:49-50`）。
- **animetrace**（P3×1）：上游仅群聊，本仓库私聊也响应（`Z/animetrace/main.go:32,44` ↔ `P/query/animetrace/plugin.go:73-83`）。
- **minecraftobserver**（P2×1）：快照无图标哈希，状态变化检测缺失（`Z/minecraftobserver/minecraftobserver.go:272-289` ↔ `P/query/minecraftobserver/subscribe.go:34-40,322-347`）。P3：全局订阅列表仅校验 `isAdmin`，群聊也可执行（`Z/.../minecraftobserver.go:134` ↔ `P/.../subscribe.go:194-196`）。
- **genshin**（P3×2）：全角冒号；文本与图片分两条发送（`Z/genshin/ys.go:110,108-114` ↔ `P/query/genshin/gacha.go:207,223,206-213`）。
- **github**（P3×1）：`>github` 后不要求空白，`>githubfoo` 会误触发（`Z/github/repo_searcher.go:24` ↔ `P/query/github/plugin.go:83-97`）。
- **yujn**（P3×1）：额外附带「换一句」按钮（`Z/yujn/yujn.go:115,126` ↔ `P/query/yujn/plugin.go:52-55`）。
- **hyaku**（P3×1）：发送顺序由「图→文本→图」改为「文本→图」（`Z/hyaku/main.go:103-107,129-133` ↔ `P/query/hyaku/plugin.go:71-78`）。

### 工具 + llm（18）

- **base16384**（P2×1）：新增 1024 字节输入上限（`Z/base16384/main.go:20-61` ↔ `P/tool/base16384/plugin.go:22,65-67,87-89,98-100`）。
- **base64gua**（P2×1）：新增 1024 字节限制（`Z/base64gua/main.go:20-61` ↔ `P/tool/base64gua/plugin.go:23,66-69,88-90,100-102`）。
- **baseamasiro**（P2×1）：新增原文 1024 / 密文 4096 字节限制（`Z/baseamasiro/main.go:20-61` ↔ `P/tool/baseamasiro/plugin.go:22-27,69-72,80-83`）。
- **imagesave**（P2×1）：无图/参数无效也进入 30 秒等待，下一条任意图片可能被保存（`K/image-save-path/lib/index.js:425-426,432-435` ↔ `P/tool/imagesave/plugin.go:279-283,305`）。
- **ping**（P2×1）：仅支持 IPv4 风格字符，IPv6 被判非法（`S/ping/src/utils/PingUtils.ts:27-32` ↔ `P/tool/ping/plugin.go:14`）。P3：无参回 `pong`（`S/ping/src/index.ts:12` ↔ `P/tool/ping/plugin.go:29-31`）。
- **crypter**（P2×1）：齁语加密不优先发语音，始终回文本（`Z/crypter/handlers.go:101-106` ↔ `P/tool/crypter/plugin.go:48-53`）。P3：空参数新增用法提示（`Z/.../main.go:31-32` ↔ `P/.../plugin.go:27-31,48-52`）。
- **bmi**（P2×3）：命令名 `bmi` 而非 `bmi-calculator`；六档建议写死不可配置；补齐了上游 24.9/29.9/34.9 的区间空洞（`K/bmi-calculator/lib/index.js:87,10-54,70-84` ↔ `P/tool/bmi/plugin.go:61-67,15-58,99-117`）。P3：提示文案不同（`K/.../index.js:91,112,121` ↔ `P/.../plugin.go:73,84,88`）。
- **cnbimagebed**（P2×1）：上传失败不回退本地 URL（`S/cnb-image-bed/cnb图床.js:321-335` ↔ `P/tool/cnbimagebed/plugin.go:240-254`）。
- **font**（P2×2）：终末体/粗苹方体/苹方体三者映射到同一 CSS 栈；抖动改为整块位移而非逐字符偏移（`Z/font/main.go:28,33-59,91-107` ↔ `P/tool/font/plugin.go:39-52,120-138`）。
- **patina**（P2×2）：六组功能默认全开（上游默认关）；纯数字 QQ 号不再转 `qlogo` 头像（`K/patina/src/index.ts:156-183,211-242`、`command1.ts:26-34` ↔ `P/tool/patina/plugin.go:78-119,513-527`、`media.go:24-46`）。
- **qqmarkdown**（P2×2）：模板 ID/样式丢失降级纯文本；无 `msg_seq` 与 40054005 去重重试（`K/qq-markdown-button/src/template.ts:119-135`、`sender.ts:21-31,95-108` ↔ `P/tool/qqmarkdown/plugin.go:284-288,306-333,151-171`）。
- **qrcode**（P2×1）：缺 `enablecommand` 开关（`K/qrcode-service-null/src/index.ts:10-12,56-66` ↔ `P/tool/qrcode/plugin.go:31-38`）。P3：颜色名支持较少（`K/.../index.ts:30-33` ↔ `P/.../plugin.go:218-233,312-327`）。
- **universaltranslation**（P2×1）：双接口失败时上游静默，本仓库回「翻译失败」（`K/universal-translation/src/service.ts:18-28` ↔ `P/tool/universaltranslation/plugin.go:91-94`）。
- **chrev**（P3×1）：空输入新增用法提示（`Z/chrev/init.go:21-32` ↔ `P/tool/chrev/plugin.go:32-34`）。
- **llm**（P2×2）：默认总结 Prompt/标题不同；切分长度 1800 rune 且普通文本逐条发送，上游 1000 字符合并转发（`Z/llm/main.go:85-95,111-119,124-142,205-225` ↔ `P/ai/llm/plugin.go:49,363,301-305,363-367`）。

### 游戏/语音/聊天/管理（23）

- **guessmusic**（P2×2）：无 105s「还有15s」提醒；判定用双向 `Contains`，猜得比答案长也算对（`Z/guessmusic/guessmusic.go:126-139,299-304` ↔ `P/game/guessmusic/plugin.go:44,79-81,489-493`）。
- **handou**（P2×2）：无 105s 提醒；答案缺词源/近义词/褒贬（`Z/handou/game.go:158,162,654-673` ↔ `P/game/handou/plugin.go:27-29,84-86,119-121,188`）。
- **mcfish**（P2×3）：缺 `维修鱼竿` 别名；次数未限制 1–59；美西螈/三叉戟买卖也累积诅咒（`Z/mcfish/pole.go:156`、`fish.go:18,34`、`store.go:306-307,648-649` ↔ `P/game/mcfish/plugin.go:53,25,94-109`、`store.go:150,215`）。
- **niuniu**（P2×1）：商店/拍卖行由 120 秒会话内回复改为独立指令（`Z/niuniu/main.go:169-195,51-98` ↔ `P/game/niuniu/plugin.go:125-130,440-456`）。P3：冷却文案由随机 4 条改为固定（`Z/.../main.go:290-295,327-332` ↔ `P/.../plugin.go:256-258,311-313`）。
- **poker**（P2×1）：只按 13×4 生成牌面，内嵌的 Lynette/Lyney 特殊牌永不选中（`Z/poker/poker.go:44-46` ↔ `P/game/poker/plugin.go:64-77`）。
- **wordle**（P2×2）：无词典校验，任意 5–7 字母都计一次；无 105s 提醒，答案无翻译（`Z/wordle/wordle.go:190-194,146-151,170,180` ↔ `P/game/wordle/plugin.go:94-101,26,79-81,111-113,168-169`）。
- **realcugan**（P2×1）：先发图再发说明，上游先说明后图（`Z/realcugan/realcugan.go:98-102` ↔ `P/image/realcugan/plugin.go:76-81`）。
- **ahsai**（P2×1）：超时只在用户下一条消息到来时判定，不会主动随机合成（`Z/ahsai/ahsai.go:80-85` ↔ `P/audio/ahsai/plugin.go:117-127`）。
- **atri**（P2×1）：「离谱」3/5 概率、「啊这」1/2 概率回复，本仓库必回（`Z/atri/atri.go:239-247,220-225` ↔ `P/chat/atri/plugin.go:76-81`）。
- **chat**（P2×2）：戳一戳未移植；回复固定「机器人」而非 Bot 昵称（`Z/chat/chat.go:41-56,29-37` ↔ `P/chat/chat/plugin.go:45,17-22`）。
- **thesaurus**（P2×1）：空文本上游不触发，本仓库回「在呢，你说。」（`Z/thesaurus/chat.go:41-47` ↔ `P/chat/thesaurus/plugin.go:57-60`）。
- **antiabuse**（P2×2）：词表由图片改为纯文本；内置三条违禁词始终生效且不可删除（`Z/antiabuse/anti.go:125-133,78` ↔ `P/admin/antiabuse/plugin.go:125-133,21-22`）。
- **breakrepeat**（P3×1）：用纯文本而非含 CQ 码的 `RawMessage` 比较复读（`Z/breakrepeat/breakrepeat.go:29-32` ↔ `P/admin/breakrepeat/plugin.go:45`）。

## 宿主能力限制（不要求按上游修复）

以下问题根因在宿主 SDK/平台能力，不按上游逐项修复；建议在 README 或插件帮助中明确说明。

- **群成员列表 / 群名片**：`score` 钱包排名无法遍历全群成员（`Z/wallet/wallet.go:70-76` ↔ `P/entertainment/score/plugin.go:339`）；`gloryofkings` 的 `#排位排名`、`#谁在打游戏/在线列表`（`S/glory-of-kings/apps/rankList.js:60-62`、`whoIsPlaying.js:121`）；`manager` 升为管理/修改名片/头衔（`Z/manager/manager.go:101,216,229`）；`wtf` 被 @ 成员群昵称（`Z/wtf/main.go:45-49`）。
- **平台管理员权限 / 全员禁言 / 退群 / 入群审批**：`manager` 的 `开启|解除全员禁言`、`退出群聊`、入群欢迎与验证（`manager.go:148-157,140,448-618`）；`antiabuse` 的 bot 级封禁（`Z/antiabuse/anti.go:79-96`）。
- **消息引用 / 被回复消息内容**：`imagesave` 保存回复图片（`K/image-save-path/lib/index.js:417-448`）；`llm` `/gpt` 读取被回复内容（`Z/llm/main.go:145-181`）；`manager` 撤回/精华/回应表情依赖 `CQ:reply`（`manager.go:265,417,647`）。
- **定时任务 / 主动推送**：`manager` 定时提醒（`manager.go:296-361`）；`gloryofkings` 群日报/战绩推送（`groupReport.js:74-81`、`gameRecordPush.js:188-192`）；`endfield` 公告/理智订阅/自动签到；`minecraftobserver` 改手动拉取。
- **原始 OneBot API**：`manager` 群聊/私聊转发、翻牌、赞我、回应表情、精华（`manager.go:270-295,362-447,646-711`）；`crypter` face 段（`Z/crypter/main.go:39-40`）；`antiabuse` bot 级封禁。
- **视频发送**：`yujn` 21 个视频指令（`Z/yujn/yujn.go:97`）；`bilibiliparse` 视频下载/总结（README 已注明）。
- **历史群消息**：`wordcount` `热词` 的完整统计（README「纯逻辑 / 无密钥插件的降级」）；`llm` 群聊总结只统计启动后消息。
- **戳一戳 / 群文件事件 / 发送返回 messageId**：`chat` 戳一戳（`Z/chat/chat.go:41-56`）；`midicreate` 群文件互转；`toutai` 自动撤回。
- **图片消息不能带 @/键盘**：`wife`、`omikuji` 等拆成两条消息；`poker/jandan/gloryofkings` 先发 Markdown 按钮再发图。

## 已文档化限制

以下为 README 已声明、**不重复计入移植缺陷**（但部分问题在 P0/P1 中另有独立缺陷，已在相应行注明）：

- `bilibilipush`、`rsshub`、`steam`、`minecraftobserver`：不常驻轮询，改「按需查询 + 管理员手动拉取」，60 秒频控、每群最多 5 个订阅（`README.md:142-149,246`）。
- `patina`：依赖本机浏览器、30 秒交互、5 MiB 内联上限、文件改图片发送（`README.md:286-290`）。
- `cnbimagebed`：需配置 API 地址/token/仓库，宿主无 `imageToUrl` 钩子（`README.md:283`）。
- `imagesave`：`savePaths` 需映射到宿主机可写目录（`README.md:281`）；但「回复图片保存」缺失另计 P1 宿主限制。
- `qqmarkdown`：模板目录相对工作目录，按钮回调依赖宿主交互事件（`README.md:282`）。
- `llm`：群聊总结只统计插件启动后消息、仅 `adminUsers`、默认 200/上限 1000、30 秒限频（`README.md:298,319-320`）；但 `/gpt` 读引用消息缺失另计 P1。
- `toutai`：`webp` 以 PNG 发送；`retractDelay` 因 Reply 不返回 messageId 不生效（`README.md:301-305`）。
- `endfield`：无定时任务 API，公告/理智订阅/自动签到/授权轮询不能后台执行；`更新/强制更新` 仅保留入口；MaaEnd 依赖客户端（`README.md:307-314`）。
- `bilibiliparse`：无视频下载、AI 总结、动态解析（`README.md:200-202`）。
- `ping`：README 插件表已声明为「TCP 连通延迟检测（无需 ICMP 权限）」（`README.md:54`），故 P1 标为已文档化限制。
- `mcfish`：上游 `articlesInfo.json` 未随源码提供，按源码默认值重建（`README.md:248`）。
- `wordcount`：只统计本条消息、不读群历史（`README.md:227`）；但 `热词` 触发词完全缺失仍计 P0。
- `crypter`：事件不暴露 face 段（`README.md:228`），对应 P1 已标宿主限制。
- `base16384`：README 仅注明「密文与上游未必互通」（`README.md:229`），这是不确定的推定实现，仍计 P1 移植缺陷而非可接受限制。
- `ahsai`：README 称「音源编号按名称排序（与上游一致）」（`README.md:260`），与实际上游原始 `namelist` 顺序不符，需修正文档；P1 已记录编号差异。

## 无差异 / 仅措辞差异插件

**逐项核对无实质差异**：

- `entertainment/idiom`（仅 2 条 P3）、`entertainment/kfccrazythursday`、`entertainment/pig`、`entertainment/tiangou`、`entertainment/toutai`（独立上游移植插件）。
- `query/dailynews`、`query/moyucalendar`、`query/weather`、`query/bilibiliparse`（限制已文档化）、`query/movies`（README 已注明最多 8 条）。
- `tool/furrycode`、`image/gif`、`audio/alipayvoice`（仅新增金额校验）、`audio/huozi`、`audio/midicreate`（群文件事件已文档化）、`audio/music`（仅网易云已文档化）、`admin/sleepmanage`、`admin/status`（纯文本已文档化）。
- `game/endfield`（主代理另行核对，见 README「独立上游移植插件」）。

**仅 P3 措辞/参数差异**：

- `entertainment/shadiao`、`entertainment/shindan`、`entertainment/wife`、`query/animetrace`、`query/genshin`、`query/github`、`tool/chrev`、`admin/breakrepeat`。

## 建议修复顺序

1. **P0 移植缺陷（10 条）**：按用户影响排序
   1. `bilibilipush` 补 `取消b站动态订阅`、`取消b站直播订阅`（对应开关独立清除）。
   2. `bilibilipush` 补 `开启/关闭艾特全体`，直播更新按开关 @全体。
   3. `steam` 补 `查看apikey`（私聊管理员、掩码）。
   4. `hs` 补 `[AAE…]` 卡组代码解析与卡组图。
   5. `hitokoto` 补 `系列一言`（文本序号 + 120 秒选择）。
   6. `handou` 补 `猜成语热门汉字/成语`。
   7. `warframeapi` 补 `.wm` 市场查价。
   8. `yujn` 补 3 条语音与 `让lulu说…`。
   9. `wordcount` 补 `热词` 入口；宿主提供历史消息接口后补统计。
   10. `gloryofkings` 补 bot/总排名（群排名依赖群成员列表）。
2. **P1 移植缺陷（24 条）**：优先修数据/权限/兼容类
   - 兼容与数据：`base16384` TEA、`ahsai` 音源编号、`handou` 词库、`wordle` 词库、`hyaku` 平假名、`chouxianghua` 词表。
   - 权限/经济：`steam` 订阅权限、`niuniu` 注销费用与二次确认、`antiabuse` 增删权限。
   - 指令/交互：`rsshub` 指令别名与推送摘要、`bilibilipush` 动态卡片、`drawlots` 增删签、`emozi` 抽象登录、`funny` 前缀匹配、`moyu` 节日、`tarot` 问题解读、`ygocdb` 多结果、`wantquotes` 分类、`guessmusic` 提示、`atri` 凌晨静默、`warframeapi` 时间同步、`hitokoto` 关键词。
3. **P2**：按簇批量修文案/上限/交互（`score` 预设空名单放行、VUP 50 条、弹幕上限、钱包排名缓存、ygotrade 截断、bmi 配置化、font 字体/抖动、patina 默认开关、qqmarkdown `msg_seq`、mcfish 次数与诅咒等）。
4. **P3**：随对应插件改动顺手对齐，不单独排期。

**宿主限制单列，不排入修复**：群成员列表/群名片、平台管理员与全员禁言/退群/入群审批、消息引用内容、定时任务、原始 OneBot API、视频发送、历史群消息、戳一戳/群文件/撤回 messageId。统一在 README 与插件帮助中标注，待宿主能力补齐后再评估。
