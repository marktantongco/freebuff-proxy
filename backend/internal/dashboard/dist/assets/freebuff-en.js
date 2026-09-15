/*
 * freebuff-en.js — English front-end patch for Freebuff Desktop
 *
 * Runtime DOM localization layer restoring the English UI from a
 * Simplified-Chinese-localized Freebuff Desktop (e.g. after applying
 * Kfowever/freebuff-zh-patch). Same DOM-walk architecture as the zh patch,
 * with the translation direction inverted (zh → en).
 *
 * Derived from Kfowever/freebuff-zh-patch d34b01f (MIT) — translation tables
 * inverted by tools/gen-en-patch.mjs. No network access, no user-data access.
 *
 * Performance notes (vs the zh patch engine):
 *   - CJK fast-reject: every translation surface is CJK-gated, so strings
 *     without CJK codepoints return immediately (audited: all exact keys,
 *     patterns, and label tables require CJK).
 *   - The MutationObserver coalesces records into sets flushed in one
 *     microtask instead of spawning a TreeWalker per record.
 *   - Subtrees inside ignorable regions (code, prose, terminal…) are skipped
 *     wholesale instead of per-node.
 */
(() => {
  'use strict'

  const PATCH_ID = 'freebuff-en'
  const PATCH_VERSION = '0.6.2'
  if (globalThis.__FREEBUFF_EN_PATCH__?.id === PATCH_ID) return

  const CJK_RE = /[\u3000-\u303f\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff\uff01-\uff5e]/

  // zh → en exact strings (inverted from the zh patch exact map).
  const exact = new Map(Object.entries({
  '暂时无法获取 Freebucks 余额。': 'Freebucks balance temporarily unavailable.',
  'Freebucks 余额': 'Freebucks balance',
  '智能且快速': 'Smart & Fast',
  '智能且快速 · 可能将数据用于 AI 训练': 'Smart & Fast · May use data for AI training',
  '需要排队 · 可能将数据用于 AI 训练': 'Queue · May use data for AI training',
  '你的 API 服务商': 'Your API providers',
  '使用你的密钥 · 由你的服务商计费': 'Your keys · Your provider’s billing',
  '你的 API 密钥': 'Your API key',
  '连接服务商…': 'Connect a provider…',
  '连接服务商': 'Connect a provider',
  '管理服务商…': 'Manage providers…',
  '管理服务商': 'Manage providers',
  '添加或管理你的 API 密钥': 'Add or manage your API keys',
  '连接服务商以使用你自己的 API 密钥。此电脑上的各项目均可使用。': 'Connect a provider to use your own API key. Available across projects on this computer.',
  '你可以将此任务切换到自己的 API 服务商。切换后，如需再次使用 Freebuff 模型，请新建任务。': 'You can switch this task to your API provider. After switching, start a new task to use Freebuff models again.',
  '此任务使用你所选的服务商。如需使用 Freebuff 模型或切换服务商，请新建任务。': 'This task uses your selected provider. Start a new task to use Freebuff models or switch providers.',
  '在新建或现有 Freebuff 任务的模型菜单中选择服务商。任务使用你的 API 密钥后，如需返回 Freebuff 模型，请新建任务。': 'Select a provider in the model picker of a new or existing Freebuff task. Once a task uses your API key, start a new task to return to Freebuff models.',
  '此电脑': 'THIS COMPUTER',
  '使用你的模型和服务商账户。': 'Your models. Your provider account.',
  '模型请求直接发送到你的服务商，并由其计费。使用自有 API 密钥的任务不含广告。': 'Model requests go directly to your provider and use its billing. BYOK tasks are ad-free.',
  '连接你的第一个服务商': 'Connect your first provider',
  '使用 OpenRouter 密钥，或连接其他兼容 OpenAI 接口的服务。': 'Bring an OpenRouter key, or connect another OpenAI-compatible service.',
  '添加服务商': 'Add provider',
  '正在加载服务商…': 'Loading providers…',
  '已连接的服务商': 'CONNECTED PROVIDERS',
  '关闭 API 服务商管理': 'Close API providers',
  '隐私与支持的功能': 'About privacy and supported features',
  '已启用的同步与诊断功能保持现有行为。手机镜像可同步任务记录。使用自有 API 密钥时，不支持自动运行和托管研究工具。': 'Enabled sync and diagnostics keep their existing behavior. Mobile Mirror can sync task transcripts. Auto-run and hosted research tools are unavailable for BYOK.',
  '更改会立即保存。': 'Changes are saved immediately.',
  '此电脑上的桌面应用和 CLI 共享此配置。': 'Shared by Desktop and CLI on this computer.',
  '搜索服务商…': 'Search providers…',
  '未找到服务商。请尝试“自定义”。': 'No providers found. Try “Custom”.',
  '自定义端点': 'Custom endpoint',
  '兼容 OpenAI 接口': 'OpenAI-compatible',
  '请求发送至': 'Requests go to',
  '服务商配置指南 ↗': 'Provider setup ↗',
  '这只是预设配置，模型的编程支持仍需测试。': 'Preset only. Your model’s coding support still needs testing.',
  'API 基础地址': 'Base URL',
  '服务商 API 基础地址': 'Provider base URL',
  '请使用 HTTPS；本机服务器也可使用 HTTP。': 'Use HTTPS, or HTTP for a server on this computer.',
  'API 密钥': 'API key',
  '服务商 API 密钥': 'Provider API key',
  '粘贴你的 API 密钥': 'Paste your API key',
  '隐藏 API 密钥': 'Hide API key',
  '显示 API 密钥': 'Show API key',
  '保存在操作系统凭据存储中，不会写入项目文件。': 'Saved in your OS credential store. Never in project files.',
  '模型 ID': 'Model ID',
  '服务商模型 ID': 'Provider model ID',
  '请从服务商处复制准确的模型 ID。编程支持尚未测试。': 'Copy the exact ID from your provider. Coding support is untested.',
  '高级设置': 'Advanced settings',
  '名称与模型限制': 'Name and model limits',
  '连接名称': 'Connection name',
  '例如：个人账户': 'For example, Personal',
  '服务商连接名称': 'Provider connection name',
  '上下文窗口令牌数': 'Context window tokens',
  '配置的上下文窗口': 'Configured context window',
  '最大输出令牌数': 'Maximum output tokens',
  '配置的最大输出令牌数': 'Configured maximum output tokens',
  '请填写模型支持的限制值。这些是用量上限，并非自动检测到的模型能力。': 'Use limits supported by your model. These are budgets, not detected capabilities.',
  '编程能力尚未测试': 'Coding untested',
  '检查连接': 'Check connection',
  '正在检查…': 'Checking…',
  '更换密钥': 'Replace key',
  '新 API 密钥': 'New API key',
  '现有任务继续使用旧版连接。请在新任务中使用更换后的密钥。': 'Existing tasks keep their old connection revision. Use the replacement in a new task.',
  '保存新密钥': 'Save replacement key',
  '取消更换': 'Cancel replacement',
  '移除服务商': 'Remove provider',
  '保留服务商': 'Keep provider',
  '正在移除…': 'Removing…',
  '？使用它的任务将在下一次模型请求前停止。': '? Tasks using it will stop before their next model request.',
  '保存服务商': 'Save provider',
  '服务商已添加。请在新建或现有 Freebuff 任务的模型菜单中选择它。': 'Provider added. Select it in the model picker of a new or existing Freebuff task.',
  '凭据已验证。编程支持仍未测试。': 'Credential verified. Coding support is still untested.',
  '端点可访问。身份验证和编程支持仍需通过推理测试确认。': 'Endpoint reachable. Authentication and coding support still need an inference test.',
  '密钥已更换。请在新任务中选择此更新后的连接。': 'Key replaced. Select this updated connection in a new task.',
  '打开的标签页': 'Open tabs',
  'Freebuff 菜单': 'Freebuff menu',
  '新建空间': 'New space',
  '在此项目中新建空间：': 'New space in',
  '关闭此空间': 'Close this space',
  '关闭空间': 'Close space',
  '使用文件夹名称': 'Use folder name',
  '重命名…': 'Rename…',
  '重置名称': 'Reset name',
  '新建任务': 'Start a new thread',
  '应用工具': 'App tools',
  '搜索连接器': 'Search connectors',
  '全部连接器': 'All connectors',
  '移除连接器': 'Remove connector',
  '附加图片': 'Attach images',
  '输入消息 — / 选择技能，@ 引用任务或文件': 'Type a message — / for skills, @ for threads or files',
  '项目中的任务和文件': 'Project threads and files',
  '共享工作区': 'Shared workspace',
  '任务引用提示': 'Thread mentions tip',
  '任务引用': 'Thread mentions',
  '你的任务之间可以互相引用。': 'Your threads can talk to each other.',
  '将先前对话的上下文带入当前任务。': 'Bring context from an earlier conversation into this one.',
  '提示示例': 'Example prompt',
  '参考此任务中的方法：': 'Use the approach from',
  '，在这里添加登录功能。': 'to add sign-in here.',
  '试一试': 'Try it',
  '输入 @ 以选择任务': 'Type @ to choose a thread',
  '已保存的任务快照 · 只读': 'Saved thread snapshot · read-only',
  '无法加载历史记录': 'Could not load history',
  '此页历史记录已发生变化。请返回最新消息后重试。': 'This history page changed. Return to latest and try again.',
  '对话位于可视区域外 — 聚焦以阅读': 'Conversation outside the viewport — focus to read',
  '对话分页': 'Conversation pages',
  '更早的消息': 'Older messages',
  '较新的消息': 'Newer messages',
  '返回最新消息': 'Return to latest',
  '显示完整提示': 'Show the full prompt',
  '展开更多': 'Show more',
  '收起': 'Show less',
  '正在编辑较早的消息 — 发送后将替换该消息、移除其后的所有消息，并撤销智能体随后对文件的更改。': 'Editing an earlier message — sending will replace it, remove all later messages and rewind the agent’s subsequent file changes.',
  'Freebuff 已完成回复': 'Freebuff finished responding',
  '回复已停止': 'Response stopped',
  '回复失败': 'Response failed',
  '目标已暂停。发送消息以继续。': 'Mission paused. Send a message to continue.',
  '托管会话名额已满': 'Hosted session slots are in use',
  '冲刺 — 专注完成任务': 'Sprint — focused and complete',
  '无法刷新此提案。Freebuff 重新连接后将恢复操作控件。': 'Could not refresh this proposal. Its controls will return when Freebuff reconnects.',
  '正在重试结束会话': 'Retrying session end',
  '退款处理中': 'Refund processing',
  '退款尚未确认': 'Refund unconfirmed',
  '会话已结算 · 无退款': 'Session settled · no refund',
  '会话已结束 · 退款尚未确认': 'Session ended · refund unconfirmed',
  '你的对话已保存。桌面应用将在连接后自动重试。': 'Your conversation is saved. Desktop will retry automatically when connected.',
  '会话已结束。正在等待最终用量结算；余额将自动刷新。': 'Session ended. Waiting for final usage charges; your balance will refresh automatically.',
  '服务器已确认最终退款。': 'Final refund confirmed by the server.',
  '服务器未返回退款金额，暂不计入退款余额。': 'The server did not report a refund amount. No credit is assumed.',
  '会话退款': 'Session refunds',
  '最近的会话退款': 'Recent session refunds',
  '上一会话': 'Previous session',
  '无法获取余额。启动会话可能消耗每日额度或钱包中的 Freebucks。是否继续？': 'Your balance is unavailable. Starting a session may spend Freebucks from your daily allowance or wallet. Continue?',
  '将结束当前会话。无法获取余额，启动会话可能消耗每日额度或钱包中的 Freebucks。是否继续？': 'Ends your current session. Your balance is unavailable. Starting a session may spend Freebucks from your daily allowance or wallet. Continue?',
  '邀请好友以解锁': 'Unlock by referring friends',
  'Novita 通道 — 仅供评估': 'Novita route — evaluation only',
  '匿名服务商会保留提示内容': 'Anonymous provider retains prompts',
  '所有用户共享且有速率限制：繁忙时排队，随后由 DeepSeek V4.1 Flash 回答。': 'Rate limited and shared by all users: queues when busy, then answers on DeepSeek V4.1 Flash.',
  'Freebuff 无法加载': 'Freebuff couldn’t load',
  '部分界面未能启动。请重新加载一次；若再次出现此页面，请重新安装最新版本。你的项目和对话不会丢失。': 'Part of the interface did not start. Reload once; if this screen returns, reinstall the latest version. Your projects and conversations are safe.',
  '重新加载 Freebuff': 'Reload Freebuff',
  '获取最新版安装程序': 'Get latest installer',
  '需要批准': 'Needs approval',
  '需要重新连接': 'Reconnect required',
  '连接失败': 'Connection failed',
  '选择工具': 'Choose tools',
  '选择工具：': 'Choose tools for',
  '随时可用': 'Ready when needed',
  '社区配置': 'Community setup',
  '配置指南': 'Setup guide',
  '配置': 'Set up',
  '运行并显示工具': 'Run it and show me its tools',
  '连接器已添加。请刷新连接器以继续配置。': 'Connector was added. Refresh your connectors to continue setup.',
  '连接并选择工具': 'Connect and choose tools',
  '允许': 'What may',
  '执行哪些操作？': 'do?',
  '此服务提供了': 'It reported',
  '个工具。选择前不会调用任何工具。工具标签由服务器自行声明，仅供参考，并非保证。': 'tools. Nothing can be called until you choose. Servers label their own tools, so treat these hints as a claim, not a guarantee.',
  '仅选择标记为安全的工具': 'Safe only',
  '社区配置指南': 'Community setup guide',
  '配置文档': 'Setup documentation',
  '配置名称：': 'Configuration name:',
  '。此电脑上的所有智能体均可使用。': '. Available to every agent on this computer.',
  '从此电脑移除？这也会移除其保存的访问授权和共享 CLI 配置。': 'from this computer? This also removes its saved access and shared CLI configuration.',
  '粘贴服务器说明中的': 'Paste the',
  '审查连接': 'Review connection',
  '重试连接': 'Retry connection',
  '请参阅': 'Follow the',
  '配置指南，确认支持的客户端和账户要求，并获取 MCP 配置。然后在此添加配置、审查访问权限并选择工具。': 'setup guide to check supported clients, account requirements, and get your MCP configuration. Then add it here to review access and choose tools.',
  '添加 MCP 配置': 'Add MCP configuration',
  '连接以审查访问权限，并选择 Freebuff 可以使用的工具。': 'Connect to review access and choose which tools Freebuff may use.',
  '此连接器已移除。请返回“你的连接器”以继续。': 'This connector was removed. Return to your connectors to continue.',
  '添加自定义 MCP': 'Add custom MCP',
  'MCP 配置 JSON': 'MCP configuration JSON',
  '配置块。配置将写入': 'block from the server’s instructions. It is written to',
  '。审查并批准后才会运行。': '. Nothing runs until you review and approve it.',
  '连接器名称': 'Connector name',
  '连接器视图': 'Connector views',
  '你的连接器': 'Your connectors',
  '无法刷新你的连接器。': 'Couldn’t refresh your connectors.',
  '正在显示上次已知的连接状态。': 'Showing the last known connection states.',
  '无法获取连接状态。': 'Connection states are unavailable.',
  '搜索结果': 'Search results',
  '热门连接器': 'Top connectors',
  '显示全部': 'Show all',
  '你的自定义连接器': 'Your custom connectors',
  '没有活动连接': 'No active connections',
  '在这里添加你的工具': 'Your tools belong here',
  '请尝试其他名称或描述。': 'Try another name or description.',
  '就绪、已禁用和待批准的连接器列在“你的连接器”中。': 'Ready, disabled, and unapproved connectors are listed under Your connectors.',
  '查找连接器或添加自己的 MCP 服务器以开始使用。': 'Discover a connector or add your own MCP server to get started.',
  '探索连接器': 'Explore connectors',
  '你的智能体之间共享连接。': 'Connections are shared across your agents.',
  '在你的电脑上运行的自定义 MCP 服务器。': 'A custom MCP server that runs on your computer.',
  '通过独立地址连接的自定义 MCP 服务器。': 'A custom MCP server connected through its own address.',
  '查找、创建和更新问题、项目及评论。': 'Find, create, and update issues, projects, and comments.',
  '搜索工作区，创建或更新已连接的页面。': 'Search your workspace and create or update connected pages.',
  '排查错误、性能问题，查看发行版本及项目。': 'Investigate errors, performance issues, releases, and projects.',
  '通过智能体使用 Stripe API 和开发者文档。': 'Use Stripe’s API and developer documentation from your agent.',
  '查看项目、数据库、日志和开发资源。': 'Inspect projects, databases, logs, and development resources.',
  '管理 Cloudflare 服务并查看账户配置。': 'Manage Cloudflare services and inspect account configuration.',
  '处理仓库、议题、拉取请求及 GitHub 项目。': 'Work with repositories, issues, pull requests, and GitHub projects.',
  '访问项目、议题、合并请求及 GitLab 工作流。': 'Access projects, issues, merge requests, and GitLab workflows.',
  '查找和管理工作区、项目、任务及报告。': 'Find and manage workspaces, projects, tasks, and reports.',
  '搜索和更新 Jira、Confluence、Bitbucket 及其他 Atlassian 工作内容。': 'Search and update Jira, Confluence, Bitbucket, and other Atlassian work.',
  '在你的权限范围内查询数据库、创建或更新记录。': 'Query bases and create or update records within your permissions.',
  '搜索和管理任务、文档、工作区成员及聊天。': 'Search and manage tasks, Docs, workspace members, and Chat.',
  '使用 CRM 数据生成报告、执行工作流及基于账户信息的自动化。': 'Use CRM data for reports, workflows, and account-aware automation.',
  '搜索客服对话与联系人，管理帮助中心内容。': 'Search support conversations and contacts, and manage Help Center content.',
  '选择已连接应用中获准的操作，并自动执行工作流。': 'Choose approved actions from connected apps and automate workflows.',
  '让智能体访问实时商品目录和电商功能。': 'Connect agents to real-time product catalog and commerce capabilities.',
  '搜索文档，管理项目、部署及日志。': 'Search documentation and manage projects, deployments, and logs.',
  '为智能体提供当前 Netlify 上下文及部署功能。': 'Give an agent current Netlify context and deployment capabilities.',
  '管理项目与分支，查看数据库并运行 SQL。': 'Manage projects and branches, inspect databases, and run SQL.',
  '探索数据、查询集合并管理数据库部署。': 'Explore data, query collections, and manage database deployments.',
  '查看日志、指标、追踪、仪表盘、监控及事件。': 'Investigate logs, metrics, traces, dashboards, monitors, and incidents.',
  '管理 App Platform、Droplets、Kubernetes 集群及云资源。': 'Manage App Platform, Droplets, Kubernetes clusters, and cloud resources.',
  '搜索、探索和分析账户可访问的索引。': 'Search, explore, and analyze the indices available to your account.',
  '让工具访问本地 Convex 项目的代码、数据、日志及函数。': 'Expose a local Convex project to tools for code, data, logs, and functions.',
  '通过 Snyk CLI 在本地检查代码和依赖的安全性。': 'Run local code and dependency security checks through the Snyk CLI.',
  '管理服务、部署、数据库、日志及指标。': 'Manage services, deploys, databases, logs, and metrics.',
  '通过 Appwrite 管理项目、API 及文档。': 'Manage projects, APIs, and documentation through Appwrite.',
  '处理 Auth0 租户配置及 Management API 资源。': 'Work with Auth0 tenant configuration and Management API resources.',
  '使用当前 Clerk SDK 的用法及身份验证实现指南。': 'Use current Clerk SDK patterns and authentication implementation guidance.',
  '上传、整理、转换和分析媒体资源。': 'Upload, organize, transform, and analyze media assets.',
  '查看数据集，并在管控范围内执行 BigQuery 查询。': 'Inspect datasets and run governed queries against BigQuery.',
  '让智能体访问受管控的 Unity Catalog 数据和 SQL 工具。': 'Connect agents to governed Unity Catalog data and SQL tools.',
  '连接 Snowflake 管理的 MCP 服务器，在管控范围内访问数据。': 'Connect to Snowflake-managed MCP servers for governed data access.',
  '搜索和管理用于语义检索的向量集合。': 'Search and manage vector collections for semantic retrieval.',
  '管理 Redis、QStash、Workflow、Vector 和 Search 资源。': 'Manage Redis, QStash, Workflow, Vector, and Search resources.',
  '通过 Turso CLI 的 MCP 模式将智能体连接到本地 Turso 数据库。': 'Connect an agent to a local Turso database using the Turso CLI MCP mode.',
  '查看数据库、结构、分支及查询性能。': 'Inspect databases, schema, branches, and query performance.',
  '通过 MCP 配置使用 Directus 项目的 API 和数据模型。': 'Use a Directus project’s API and data model through an MCP setup.',
  '管理内容、结构、数据集及 GROQ 查询。': 'Manage content, schemas, datasets, and GROQ queries.',
  '管理 Contentful 中的结构化内容操作。': 'Manage structured content operations in Contentful.',
  '搜索、更新、发布和管理 Storyblok 内容。': 'Search, update, publish, and manage Storyblok content.',
  '配置 DatoCMS 内容管理 MCP 工作流。': 'Set up a DatoCMS content-management MCP workflow.',
  '配置 Prismic MCP 服务器以获取仓库及内容上下文。': 'Set up the Prismic MCP server for repository and content context.',
  '此供应商仓库已归档；请仅按照链接中 README 记载的方式配置。': 'This vendor repository is archived; follow the linked README only for its documented setup.',
  '通过兼容 MCP 的客户端查询和管理 SingleStore 数据。': 'Query and manage SingleStore data from an MCP-compatible client.',
  '使用 Timescale 的 Tiger CLI MCP 服务器获取数据库上下文。': 'Use Timescale’s Tiger CLI MCP server for database context.',
  '使用 Elastic MCP 工具搜索和分析 Elasticsearch 数据。': 'Search and analyze Elasticsearch data with Elastic MCP tools.',
  '读取、写入、查询和管理 Redis 数据库中的数据。': 'Read, write, query, and manage data in a Redis database.',
  '使用 Better Auth 的 MCP 指南实现身份验证集成。': 'Use Better Auth’s MCP guidance for authentication integrations.',
  '使用文档中提供的 MCP 服务器管理自托管 Coolify 资源。': 'Manage self-hosted Coolify resources with its documented MCP server.',
  '通过 ngrok 网关公开并保护自托管 MCP 端点。': 'Expose and secure a self-hosted MCP endpoint through an ngrok gateway.',
  '搜索 Hub 模型、数据集、Spaces 及文档。': 'Search Hub models, datasets, Spaces, and documentation.',
  '通过 Perplexity 官方 MCP 服务器搜索网页。': 'Search the web through Perplexity’s official MCP server.',
  '通过其托管的 MCP 管理语音智能体并生成音频。': 'Manage voice agents and generate audio through its hosted MCP.',
  '为 AI 工作流搜索、抓取和提取网页内容。': 'Search, crawl, and extract web content for AI workflows.',
  '搜索 LiveKit 文档、公开代码、示例及更新日志。': 'Search LiveKit docs, public code, examples, and changelogs.',
  '查询实验、生产日志及评估结果。': 'Query experiments, production logs, and evaluation results.',
  '通过 MCP 服务器查看 Weights & Biases 实验数据。': 'Inspect Weights & Biases experiment data through its MCP server.',
  '分析产品事件、洞察、功能开关及错误。': 'Analyze product events, insights, feature flags, and errors.',
  '通过 Mixpanel MCP 服务器查询产品分析数据。': 'Query product analytics through Mixpanel’s MCP server.',
  '使用社区 MCP 服务器查看注重隐私的分析数据。': 'Use a community MCP server to inspect privacy-friendly analytics.',
  '通过托管的 MCP 服务器使用 New Relic 可观测性功能。': 'Use New Relic observability through its hosted MCP server.',
  '通过 Tavily MCP 服务器为 AI 智能体搜索网页。': 'Search the web for AI agents through Tavily’s MCP server.',
  '为 AI 智能体搜索网页并检索研究资料。': 'Search the web and retrieve research context for AI agents.',
  '按照配置指南排查事件并处理值班工作。': 'Investigate incidents and on-call work through a setup guide.',
  '通过 MCP 访问流水线、作业及工作流状态。': 'Access pipelines, jobs, and workflow status through MCP.',
  '通过 MCP 访问流水线、作业、日志及测试数据。': 'Access pipelines, jobs, logs, and test data through MCP.',
  '查看生产错误、部署及会话回放数据。': 'Inspect production errors, deploys, and session replay data.',
  '通过 Honeybadger MCP 服务器查看应用错误。': 'Inspect application errors through Honeybadger’s MCP server.',
  '通过 MCP 客户端使用浏览器自动化和检查功能。': 'Use browser automation and inspection from an MCP client.',
  '通过 MCP 搜索日志、告警、仪表盘及 Cloud SIEM。': 'Search logs, alerts, dashboards, and Cloud SIEM through MCP.',
  '通过 MCP 客户端搜索、比较和运行 Replicate 模型。': 'Search, compare, and run Replicate models from an MCP client.',
  '浏览实时模型数据、排名、价格和文档，并测试推理。': 'Browse live model data, rankings, pricing, docs, and test inference.',
  '将智能体连接到你的 Mistral Studio 工作区。': 'Connect an agent to your Mistral Studio workspace.',
  '为智能体提供 Google Maps 地点及地理空间上下文。': 'Ground agents with Google Maps places and geospatial context.',
  '通过 MCP 连接地图、地理空间服务或文档。': 'Connect maps, geospatial services, or documentation through MCP.',
  '搜索工作区内容、发送消息并管理画布。': 'Search workspace content, send messages, and manage canvases.',
  '需要 Slack 应用身份及管理员批准的 OAuth 权限范围。': 'Requires a Slack app identity and administrator-approved OAuth scopes.',
  '通过 Google Workspace 搜索和处理邮件。': 'Search mail and work with messages through Google Workspace.',
  'Google 服务器目前处于开发者预览阶段，需要 Google Cloud 项目。': 'Google’s server is in Developer Preview and needs a Google Cloud project.',
  '在 Google 权限范围内搜索、读取和创建云端硬盘文件。': 'Search, read, and create Drive files within Google permissions.',
  '通过本地服务器读取和管理 Google 日历活动。': 'Read and manage Google Calendar events with a local server.',
  '通过 MCP 服务器连接 Microsoft Graph 数据及工作工具。': 'Connect Microsoft Graph data and work tools through an MCP server.',
  '为智能体提供 Figma 设计上下文及受支持的画布操作。': 'Bring Figma design context and supported canvas actions to an agent.',
  '远程访问仅限 Figma MCP Catalog 客户端；配置前请确认是否支持桌面版。': 'Remote access is limited to Figma MCP Catalog clients; confirm Desktop support before setup.',
  '搜索、创建、编辑和导出 Canva 设计及资源。': 'Search, create, edit, and export Canva designs and assets.',
  '需要 Canva 账户，以及客户端注册或兼容的客户端元数据。': 'Requires a Canva account and a client registration or compatible client metadata.',
  '在兼容 MCP 的工作区中查找和使用 Dropbox 文件。': 'Find and use Dropbox files from an MCP-compatible workspace.',
  'Dropbox 将此远程服务器标记为公开测试版。': 'Dropbox describes this remote server as an open beta.',
  '处理看板、列表、卡片、检查清单及工作区数据。': 'Work with boards, lists, cards, checklists, and workspace data.',
  '通过社区服务器查询文档、表格、数据行及页面。': 'Query docs, tables, rows, and pages with a community server.',
  '通过社区服务器访问 Discord 服务器及消息。': 'Access Discord servers and messages through a community server.',
  '通过 MCP 服务器查找和管理客服工单。': 'Find and manage customer-support tickets with an MCP server.',
  '通过社区服务器处理客服邮箱及对话。': 'Work with support mailboxes and conversations using a community server.',
  '通过 Calendly MCP 服务器安排活动并管理可用时间。': 'Schedule events and manage availability through Calendly’s MCP server.',
  '通过 Typeform MCP 服务器创建表单并分析回复。': 'Create forms and analyze responses through Typeform’s MCP server.',
  '通过社区服务器处理受众、营销活动及营销数据。': 'Work with audiences, campaigns, and marketing data via a community server.',
  '通过社区服务器使用 Mailgun 发送功能及域名数据。': 'Use Mailgun sending and domain data through a community server.',
  '通过 Resend MCP 服务器发送和管理事务邮件。': 'Send and manage transactional email through Resend’s MCP server.',
  '通过社区 MCP 服务器管理营销活动及联系人。': 'Manage campaigns and contacts through a community MCP server.',
  '使用订单、付款、客户、商品目录及发票数据。': 'Use orders, payments, customers, catalog, and invoice data.',
  '通过本地或远程 MCP 服务器使用 PayPal 商家工具。': 'Use PayPal merchant tools through its local or remote MCP server.',
  '通过社区服务器访问付款及商家资源。': 'Access payment and merchant resources with a community server.',
  '处理产品、客户、付款及订阅。': 'Work with products, customers, payments, and subscriptions.',
  '通过社区 MCP 服务器使用账单及订阅数据。': 'Use billing and subscription data via a community MCP server.',
  '通过社区服务器查看商店、产品、订单及订阅数据。': 'Inspect store, product, order, and subscription data with a community server.',
  '打开的任务': 'Open threads',
  '新消息': 'New messages',
  '执行失败': 'Turn failed',
  '已自动停止': 'Auto stopped',
  '合并冲突': 'Merge conflict',
  '出现问题': 'Something went wrong',
  '重试': 'Try again',
  '高级模型': 'Premium model',
  '100% 免费智能体': '100% free agent',
  '任务工具': 'Thread tools',
  '队列为空。': 'Nothing queued.',
  '选择一个任务以显示工具。': 'Select a thread to show tools.',
  '折叠侧栏': 'Collapse explorer',
  '展开侧栏': 'Expand explorer',
  '调整侧栏宽度': 'Resize explorer panel',
  '项目': 'Projects',
  '打开项目': 'Open project',
  '打开项目…': 'Open project…',
  '开始连续使用': 'Start your streak',
  '你关闭的任务会显示在这里。': 'Threads you close land here.',
  '任务目录': 'Thread catalog',
  '需要关注': 'Needs attention',
  '没有进行中的任务': 'No active threads',
  '没有已归档的任务': 'No archived threads',
  '自动归档不活跃任务的时间': 'Archive inactive threads after',
  '置顶任务': 'Pin thread',
  '取消置顶任务': 'Unpin thread',
  '标记为已处理': 'Mark handled',
  '上移': 'Move up',
  '下移': 'Move down',
  '归档任务': 'Archive thread',
  '移回“最近”': 'Return to Recent',
  '显示 Freebuff 数据': 'Show Freebuff data',
  '从项目中移除': 'Remove from projects',
  '请先关闭它已打开的标签页': 'Close its open tabs first',
  '你的账户': 'Your account',
  '跟随系统': 'Match system',
  '用量面板': 'Usage dashboard',
  '退出登录': 'Sign out',
  '正在退出登录…': 'Signing out...',
  '已退出登录': 'Signed out',
  '正在等待登录…（重试）': 'Waiting for sign-in… (retry)',
  '登录失败 — 重试': 'Sign-in failed — retry',
  '登录 Freebuff': 'Sign in to Freebuff',
  '取消登录': 'Cancel sign-in',
  '无法开始登录 — Freebuff 本地服务无响应。请重启应用。': 'Could not start sign-in — Freebuff’s local service is not responding. Restart the app.',
  '无法开始登录。': 'Could not start sign-in.',
  '需要登录 Freebuff': 'Freebuff sign-in needed',
  '消息未发送': 'Message not sent',
  '消息未加入队列': 'Message not queued',
  '无法停止当前轮次': 'Could not stop the turn',
  '无法恢复队列': 'Could not resume the queue',
  '无法切换智能体': 'Could not switch agent',
  '无法更改推理投入程度': 'Could not change reasoning effort',
  '无法切换智能体模式': 'Could not switch agent mode',
  '无法更改工作区模式': 'Could not change workspace mode',
  '无法重命名此标签页': 'Could not rename this tab',
  '无法编辑消息': 'Could not edit message',
  '需要管理员审批': 'Administrator approval required',
  'Freebuff 需要你的批准才能运行管理员命令': 'Freebuff needs your approval to run an administrator command',
  '文件将在主窗口中以标签页打开': 'Files open as tabs in the main window',
  '无法打开标签页': 'Could not open tab',
  '无法重新打开该标签页': 'Could not reopen that tab',
  '无法加载该标签页': 'Could not load this tab',
  '编辑期间会话已发生变化 — 请重试': 'The conversation changed underneath the edit — try again',
  '输入消息 — / 选择技能，@ 引用文件': 'Type a message — / for skills, @ for files',
  '输入消息 — 发送后将恢复队列': 'Type a message — sending resumes the queue',
  '输入消息 — 将添加到队列': 'Type a message — added to the queue',
  '编辑消息 — 按 Enter 从此处重新发送': 'Edit your message — Enter resends from here',
  '附加文件、图片或文件夹': 'Attach files, photos, or a folder',
  '发送消息': 'Send message',
  '停止当前任务': 'Stop the running turn',
  '正在停止当前任务': 'Stopping the running turn',
  '正在停止…': 'Stopping…',
  '复制消息': 'Copy message',
  '消息已复制': 'Message copied',
  '复制代码': 'Copy code',
  '复制命令': 'Copy command',
  '还原此消息': 'Revert message',
  '从此处分支': 'Fork from here',
  '将聊天和当前工作区文件复制到新的隔离任务': 'Fork chat and current workspace files into a new isolated thread',
  '智能体模式': 'Agent mode',
  '计划模式 — 描述希望智能体设计的内容': 'Plan mode — describe what the agent should design',
  '提供反馈': 'Give feedback',
  '建议的后续步骤': 'Suggested next steps',
  '编辑技能': 'Edit skills',
  '添加新技能': 'Add new skills',
  '运行 Freebuff 技能': 'Run the Freebuff skill',
  '运行技能': 'Run skill',
  '项目文件': 'Project files',
  '⇥ 打开': '⇥ open',
  '↵ 引用 · ⇥ 打开文件夹': '↵ mention · ⇥ open folder',
  '待发送的代码评论': 'Pending code comments',
  '移除评论': 'Remove comment',
  '放弃所有待发送评论': 'Discard all pending comments',
  '全部清除': 'Clear all',
  '下一条提示的终端上下文': 'Terminal context for next prompt',
  '终端上下文 — 将随消息发送': 'Terminal context — sent with your message',
  '移除终端上下文': 'Remove terminal context',
  '队列因错误暂停。': 'Queue paused after an error.',
  '队列已暂停。': 'Queue paused.',
  '恢复队列，或发送消息以继续。': 'Resume it, or send a message to continue.',
  '恢复队列': 'Resume queue',
  '发送并恢复队列（Enter）': 'Send and resume queue (Enter)',
  '加入队列（Enter）': 'Add to queue (Enter)',
  '发送（Enter）': 'Send (Enter)',
  '发送消息并恢复队列': 'Send message and resume queue',
  '加入队列': 'Add to queue',
  '发送前编辑': 'Edit before sending',
  '拖放文件、照片或文件夹以添加附件': 'Drop files, photos, or folders to attach',
  '移除附件': 'Remove attachment',
  '关闭图片预览': 'Close image preview',
  '消息剩余空间不足，无法加入该建议': 'Not enough room left in the message for that suggestion',
  '消息剩余空间不足，无法引用该内容': 'Not enough room left in the message to quote that',
  '添加文件需要使用桌面应用': 'Attaching files needs the desktop app',
  '粘贴文件需要使用桌面应用': 'Pasting files needs the desktop app',
  '拖放操作需要使用桌面应用': 'Drag-and-drop needs the desktop app',
  '移至新窗口': 'Move to new window',
  '关闭窗口': 'Close window',
  '最小化窗口': 'Minimize window',
  '最大化窗口': 'Maximize window',
  '还原窗口': 'Restore window',
  '已关闭的标签页': 'Closed tabs',
  '搜索已关闭的标签页': 'Search closed tabs',
  '正在关闭此标签页…': 'Closing this tab…',
  '跳到新消息': 'Jump to new messages',
  '跳到最新消息': 'Jump to the latest',
  '滚动到新消息': 'Scroll to new messages',
  '滚动到最新消息': 'Scroll to latest',
  '智能体完成后的时间': 'Time since the agent finished',
  '距你上次提问的时间': 'Time since your latest prompt',
  '项目设置…': 'Project settings…',
  '项目设置': 'Project settings',
  '关闭项目设置': 'Close project settings',
  '启动脚本': 'Startup script',
  '在每个新的隔离工作区中运行一次此 bash 命令：发送第一条消息后、智能体启动前执行。本地任务不会运行它。': 'Run this bash command once in each new Isolated workspace, after its first message and before the agent starts. Local threads do not run it.',
  '请输入 bash 命令，或将此字段留空。': 'Enter a bash command or leave the field empty.',
  '正在加载项目设置…': 'Loading project settings…',
  '设置已保存': 'Settings saved',
  '包含 AGENTS.md': 'Include AGENTS.md',
  '将项目中的 AGENTS.md（或 CLAUDE.md）指令加入智能体上下文。从下一条消息开始，适用于该项目的所有任务。': 'Include your project’s AGENTS.md (or CLAUDE.md) instructions in the agent’s context. Applies to every thread from its next message.',
  '连接器…': 'Connectors…',
  '关闭连接器': 'Close connectors',
  '尚未配置服务器。请添加到': 'No servers configured yet. Add one to',
  '，然后重新加载。': ', then reload.',
  '添加连接器': 'Add connector',
  '添加连接器…': 'Add connector…',
  '清除搜索': 'Clear search',
  '从磁盘重新加载': 'Reload from disk',
  '返回技能列表': 'Back to skills',
  '搜索要添加的技能…': 'Search skills to add…',
  '打开一个项目以开始': 'Open a project to get started',
  '定位文件夹…': 'Locate folder…',
  '请在浏览器中完成连接器登录。': 'Finish signing in to your connector in the browser.',
  '尚未启动任何程序。添加此连接器会以与你相同的权限在电脑上运行程序；启动前无法读取工具列表，之后还会再次询问允许执行的操作。': 'Nothing has started yet. Adding this connector runs a program on your computer with the same permissions as you. Its tool list cannot be read without starting it, so you are asked again afterwards about what it may do.',
  '尚未连接任何地址。添加此连接器后 Freebuff 可以与该地址通信，并可能要求你登录；连接前无法读取工具列表，之后还会再次询问允许执行的操作。': 'Nothing has been contacted yet. Adding this connector lets Freebuff talk to this address, and it may ask you to sign in. Its tool list cannot be read without connecting, so you are asked again afterwards about what it may do.',
  'Freebuff 编排器正在启动…': 'Starting Freebuff orchestrator…',
  '起始分支': 'Started from',
  '将从此分支开始': 'Starts from',
  '打开终端': 'Open Terminal',
  '无法打开终端': 'Couldn’t open Terminal',
  '无法打开工作区': 'Couldn’t open your workspace',
  '正在打开工作区…': 'Opening your workspace…',
  '打开已保存的工作区耗时过长。': 'Opening your saved workspace took too long.',
  '不恢复已保存的标签页': 'Open without saved tabs',
  '你的会话和项目文件仍会保留。': 'Your conversations and project files stay saved.',
  '选择文件夹需要使用桌面应用。': 'Choosing a folder needs the desktop app.',
  '使用隔离工作区': 'Use an isolated workspace',
  '无法设置起始分支': 'Could not set the starting branch',
  'Freebuff 需要 Git Bash': 'Freebuff needs Git Bash',
  '智能体会使用 bash 编写 shell 命令，但 Windows 并未自带 bash。Git for Windows 包含它，安装大约需要一分钟。': 'Agents write shell commands in bash, which Windows doesn’t ship. Git for Windows includes it and takes about a minute to install.',
  '下载 Git for Windows': 'Download Git for Windows',
  '每周限额': 'Weekly limit',
  '每日限额': 'Daily limit',
  '仅限 1 个标签页': '1 tab only',
  '限时试用': 'Limited-time trial',
  '受限访问': 'Limited access',
  '额度降低': 'Lower limits',
  '认识 Freebucks': 'Meet Freebucks',
  '现在使用 Freebucks 购买会话——这是可用于任意模型的每日额度。': 'Sessions are now bought with Freebucks — a daily allowance you spend on any model.',
  '每天刷新额度': 'A fresh pool every day',
  '你的 Freebucks 每天太平洋时间午夜补充，无需赚取，也无需等待。': 'Your daily Freebucks refill at midnight Pacific. Nothing to earn, nothing to wait for.',
  '不再设置每周或每月会话上限': 'No more weekly or monthly session caps',
  '只按实际支出计算。任意模型的一小时会话均按一个价格计费，并在会话开始时一次扣除。': 'The only thing that counts is what you spend. An hour of any model is one price, charged once when the session starts.',
  '按需使用': 'Spend it how you like',
  '每个模型都会显示每小时价格。可以全天使用便宜模型，也可以积攒额度使用高价模型。': 'Every model shows its price per hour. Pick the cheap one all day, or save up for the expensive one.',
  '知道了': 'Got it',
  '免费会话': 'Free sessions',
  '高级会话': 'Premium sessions',
  '高级会话剩余时间': 'Premium session time remaining',
  '获取更多会话': 'Get more sessions',
  '获取更多': 'Get more',
  '使用钱包余额': 'Use wallet',
  '打开“赚取”页面': 'Open Earn',
  '查看方案': 'See plans',
  '邀请好友 → 赚取 Freebucks': 'Refer friends → earn Freebucks',
  '每位符合条件的受邀好友都会带来 Freebucks，可在“赚取”页面领取。': 'Each qualified referral pays Freebucks, claimed on the Earn page.',
  '参与帖子互动、提升等级并获得更多每日会话': 'Engage with a post, level up, and get more daily sessions',
  '邀请、悬赏和 Trust——所有可获得 Freebucks 的方式': 'Referrals, bounties and Trust — everything that pays Freebucks',
  '奖励会话已解锁': 'Reward session unlocked',
  '已赚取会话': 'Earned sessions',
  '你的方案会话': 'your plan sessions',
  '每日方案会话': 'daily plan sessions',
  '每周方案会话': 'weekly plan sessions',
  '每月方案会话': 'monthly plan sessions',
  '优先使用免费会话 · 今日额度重置倒计时': 'Free sessions are used first · today resets in',
  '结束前可不限量使用消息和工具调用。费用已支付——切换模型会结束当前会话并购买新会话。': 'Unlimited messages and tool calls until it ends. Already paid for — switching models ends it and buys a new one.',
  '前往标签页 →': 'Go to tab →',
  '深度推理': 'Deep reasoning',
  '能力最强，适合复杂且高要求的工作': 'Most capable model for complex, demanding work',
  '可靠的智能体主力模型，适合日常任务': 'Reliable agentic workhorse for everyday tasks',
  '均衡的智能体编程模型，适合日常工作': 'Balanced agentic coding model for everyday work',
  '快速且经济的智能体编程模型': 'Fast and affordable agentic coding model',
  '繁忙时排队，随后切换至 DeepSeek V4 Flash · 可能将数据用于 AI 训练': 'Queues, then falls back · May use data for AI training',
  '繁忙时排队，随后切换至 DeepSeek V4 Flash': 'Queues, then falls back',
  '可能将数据用于 AI 训练': 'May use data for AI training',
  '综合能力强': 'Strong all-around',
  '/小时': '/hr',
  '邀请好友，获得更多免费会话': 'Refer friends for more free sessions',
  '复制邀请链接': 'Copy invite link',
  '✓ 已复制！': '✓ Copied!',
  'GLM 5.2 面板 ↗': 'GLM 5.2 dashboard ↗',
  '领取奖励并查看邀请进度': 'Claim bounties and track referrals',
  '关联注册满 4 个月的 GitHub 账户后，邀请才会计入奖励': 'Referrals qualify once a GitHub account (4+ months old) is connected',
  '关联 GitHub 以满足条件 ↗': 'Connect GitHub to qualify ↗',
  'GLM 5.2 已解锁': 'GLM 5.2 unlocked',
  '完成一个悬赏任务即可解锁': 'Complete a bounty to unlock',
  '通过悬赏任务获得': 'earned from bounties',
  'GLM 5.2 — 今日会话已用尽': 'GLM 5.2 — today’s sessions used',
  '邀请好友以解锁 GLM 5.2': 'Refer friends to unlock GLM 5.2',
  '每位符合条件的受邀好友每天可获得一个 1 小时会话，用于最强大的开源模型': 'Each qualified referral earns a daily 1-hour session of the most powerful open-source model',
  '继续队列': 'Resume the queue',
  '完成后关闭标签页': 'Close tab when done',
  '队列完成后关闭标签页': 'Close tab when queue finishes',
  '已计划关闭标签页': 'Tab close scheduled',
  '没有正在运行或排队的任务': 'Nothing is running or queued',
  '自动运行正在决定下一步': 'Auto-run is deciding what is next',
  '正在决定下一步…': 'Deciding what is next…',
  '队列为空时自动继续工作；你加入队列的任务会优先执行。': 'Keep working automatically when the queue empties. Anything you queue takes over.',
  '队列为空时让此标签页自动继续工作': 'Let this tab keep working on its own when the queue empties',
  '删除排队的关闭操作即可取消': 'Delete the queued close action to cancel',
  '上方所有任务完成后，关闭此标签页并清理任务': 'After everything above finishes, close this tab and clean up the thread',
  '该轮执行失败，因此队列其余任务已暂停。': 'That turn failed, so the rest of the queue is paused.',
  '此标签页已停止。': 'This tab is stopped.',
  '立即发送': 'Send now',
  '正在发送…': 'Sending now…',
  '编辑提示': 'Edit prompt',
  '拖动此行以重新排序': 'Drag the row to reorder',
  '无法删除该项目': 'Could not delete that item',
  '队列完成后将关闭此标签页。点击即可取消。': 'This tab will close once the queue finishes. Click to cancel.',
  '队列完成后关闭此标签页。该操作会在队列末尾添加一行。': 'Close this tab when the queue finishes. Adds a row to the end of the queue.',
  '正在等待模型…': 'Waiting for the model…',
  '正在等待后台命令…': 'Waiting on a background command…',
  '正在等待 Freebuff 会话…': 'Waiting for a Freebuff session…',
  '会话已就绪 — 正在发送请求…': 'Session ready — sending request…',
  'Freebuff 当前繁忙 — 正在重试…': 'Freebuff is at capacity — retrying…',
  '网络已断开 — 重新连接后将继续': 'No internet — turns resume when you reconnect',
  '已在重启后恢复': 'Picked up after a restart',
  '标签页已关闭并重新打开': 'Tab closed and reopened',
  '你的免费会话已结束': 'Your free session ended',
  '已执行 ·': 'Worked ·',
  '已完成': 'done',
  '处理中…': 'working…',
  '思考中…': 'Thinking…',
  '等待此任务完成': 'Waiting for this thread to finish',
  '正在创建分支…': 'Forking…',
  '提示历史': 'Prompt history',
  '进行中': 'In progress',
  '智能体待办列表': 'Agent to-do list',
  '本轮更改的文件': 'Files changed in this turn',
  '差异过大': 'large diff',
  '读取目录树': 'Read tree',
  '查找文件': 'Find files',
  '管理员权限请求': 'Administrator request',
  '写入文档': 'Write doc',
  '搜索网页': 'Search web',
  '读取 URL': 'Read URL',
  '读取文档': 'Read docs',
  '编辑笔记本': 'Edit notebook',
  '计划审批': 'Plan approval',
  '智能体的问题': 'Questions from the agent',
  '计划已准备好，等待你审阅': 'The plan is ready for your review',
  'Freebuff 有一个问题': 'Freebuff has a question',
  '复制计划': 'Copy plan',
  '将计划复制为 Markdown': 'Copy plan as Markdown',
  '计划已复制': 'Plan copied',
  '下一次计划修订的反馈': 'Feedback for the next plan revision',
  '上一个问题': 'Previous question',
  '下一个问题': 'Next question',
  '无法发送回答': 'Could not send answers',
  '无法跳过问题': 'Could not skip the questions',
  '智能体工作期间发生更改': 'Changed during agent work',
  '编辑目标': 'Edit mission',
  '目标提示': 'Mission prompt',
  '最小 — 仅追求重大且具体的改进': 'Minimal — only a major concrete gain',
  '精简 — 追求明确改进': 'Lean — buy clear improvements',
  '平衡 — 在收益明确时继续改进': 'Balanced — refine while gains are clear',
  '详尽 — 追求较小但可信的改进': 'Thorough — pursue smaller credible gains',
  '穷尽 — 改进收益变小后停止': 'Exhaustive — stop when gains are marginal',
  '冲刺 — 大致完成优先于精雕细琢': 'Sprint — roughly complete beats polished',
  '专注 — 完整并经过检查': 'Focused — complete and checked',
  '精制 — 正确、简洁且经过验证': 'Crafted — correct, clean, and proven',
  '详尽 — 各项质量维度均达到高标准': 'Thorough — strong on every quality dimension',
  '穷尽 — 做到能够验证的最佳版本': 'Exhaustive — the best version you can prove',
  '合并 PR': 'Merge PR',
  '自行编写': 'Write your own',
  '调查并报告结果，不做任何更改': 'Investigate and report back, changing nothing',
  '完成工作并在本地提交，但绝不推送': 'Do the work and commit locally, but never push',
  '一直推进到拉取请求合并完成': 'Take it all the way to a merged pull request',
  '完成目标的标准，以及完成后的处理方式。': 'What finishing looks like, and what happens to the finished work.',
  '队列完成后自动继续推进此目标': 'Set a mission to keep working toward automatically once the queue finishes',
  '队列完成后将自动推进此目标。你加入队列的任务会优先执行。': 'Working toward this mission automatically once the queue finishes. Anything you queue takes over.',
  '目标正在决定下一步': 'Mission is deciding what is next',
  '无法更改目标': 'Could not change the mission',
  '无法更改目标投入程度': 'Could not change mission effort',
  '无法保存目标': 'Could not save the mission',
  '队列为空后自动继续执行。': 'Keeps going on its own once the queue is empty.',
  '已保存的目标': 'Saved missions',
  '保存此目标': 'Save this mission',
  '忘记此目标': 'Forget this mission',
  '将此目标保留给其他任务和项目': 'Keep this mission for other threads and projects',
  '暂停排队中的任务': 'Pause queued work',
  '等待当前任务完成后，再暂停排队中的任务': 'Let the current turn finish, then pause queued work',
  '暂停队列': 'Pause the queue',
  '无法暂停队列': 'Could not pause the queue',
  '欢迎使用 Freebuff': 'Welcome to Freebuff',
  '开始使用 Freebuff 构建': 'Start building with Freebuff',
  '选择一个项目并新建任务，或继续上次的工作。': 'Pick a project and start a thread, or pick up where you left off.',
  '登录后打开项目并开始第一个任务。': 'Sign in to open a project and start your first thread.',
  '登录页面将在浏览器中打开。': 'Sign-in opens in your browser.',
  '正在等待浏览器完成登录 — 此页面会自动继续。': 'Waiting for your browser — this screen continues on its own.',
  '正在加载任务…': 'Loading thread…',
  '无法加载此任务': 'Couldn’t load this thread',
  '正在连接…': 'Connecting…',
  '正在重新连接…': 'Reconnecting…',
  '报告问题或提出功能建议': 'Report issue or feature',
  '分享反馈': 'Share feedback',
  '帮助我们改进 Freebuff。': 'Help us make Freebuff better.',
  '告诉我们哪里出了问题，或你希望看到什么。': 'Tell us what went wrong or what you’d like to see.',
  '关闭反馈': 'Close feedback',
  '这是什么类型的反馈？': 'What kind of feedback is this?',
  '结果很好': 'Good result',
  '某项功能运行得特别好': 'Something worked especially well',
  '结果不理想': 'Bad result',
  '智能体遗漏或破坏了某些内容': 'The agent missed or broke something',
  '应用错误': 'App bug',
  '崩溃、错误或界面问题': 'A crash, error, or UI problem',
  '想法、问题或其他内容': 'An idea, question, or anything else',
  '描述你遇到的问题或希望看到的功能。': 'Describe the issue or feature you’d like to see.',
  '登录后发送反馈': 'Sign in to send feedback',
  '完成登录前，你的草稿会保留在这里。': 'Your draft will stay here while you finish signing in.',
  '再次打开登录': 'Open sign-in again',
  '登录': 'Sign in',
  '将包含你的应用版本和操作系统信息。': 'Includes your app version and operating system.',
  '发送反馈': 'Send feedback',
  '反馈已发送，谢谢！': 'Feedback sent. Thank you!',
  '取消（Esc）': 'Cancel (Esc)',
  '批准命令': 'Approve command',
  '请求管理员权限': 'Administrator access requested',
  '运行目录': 'Runs from',
  '正在等待…': 'Waiting…',
  '此命令将获得完整的管理员权限。只有在你于此处批准后，Freebuff 才会调用操作系统的原生身份验证提示。': 'This command will have full administrator access. Freebuff will invoke your operating system’s native authentication prompt only after you approve it here.',
  '另一个标签页正在使用托管模型': 'Another tab is using the hosted model',
  '这会结束另一个标签页的会话。': 'Ends the other tab’s session.',
  '在此处使用': 'Use it here',
  '此标签页已获得会话名额 — 请重新发送消息。': 'This tab has the slot — send your message again.',
  '无法将会话移至此标签页。': 'Could not move the session to this tab.',
  '仅适用于此模型。': 'Specific to this model.',
  '所有高级模型共享此额度。': 'Shared across all premium models.',
  '所有可用免费模型共享此额度。': 'Shared across all available free models.',
  '切换到 DeepSeek V4 Flash 以继续。': 'Switch to DeepSeek V4 Flash to keep going.',
  '用量不完整': 'Usage incomplete',
  '令牌用量': 'Token usage',
  '任务令牌用量': 'Thread token usage',
  '压缩于': 'Compacts at',
  '任务用量': 'Thread usage',
  '当前任务记录中由提供商报告的累计用量。': 'Cumulative provider-reported usage in the current thread transcript.',
  '部分任务用量缺失': 'Some thread usage is missing',
  '这些总计仅包含中断前已报告的请求。': 'These totals include only requests reported before an interruption.',
  '推理力度': 'Reasoning effort',
  '极高': 'Extra high',
  '最低限度思考': 'Bare minimum deliberation',
  '回复最快，思考最少': 'Fastest replies, least deliberation',
  '思考更多，速度更慢': 'More deliberation, slower',
  '最深入的思考，速度最慢': 'Deepest available, slowest',
  '最大程度思考': 'Maximum deliberation',
  '全部能力': 'Everything it has',
  '编码智能体和模型': 'Coding agent and model',
  '你的订阅': 'Your subscription',
  '已登录': 'Signed in',
  '已复制 — 请在终端运行': 'Copied — run it in your terminal',
  '下载供 Freebuff 使用': 'Download for Freebuff',
  '重试下载': 'Retry download',
  '正在安装…': 'Installing…',
  '正在开始下载…': 'Starting download…',
  '第 2/2 步 · 使用 Claude Pro、Max、Team 或 Enterprise 方案登录': 'Step 2 of 2 · Sign in with a Claude Pro, Max, Team, or Enterprise plan',
  '第 2/2 步 · 使用 ChatGPT 或 API 密钥登录 Codex': 'Step 2 of 2 · Sign in to Codex with ChatGPT or an API key',
  'Codex 已退出登录': 'Codex is signed out',
  'Claude Code 已退出登录': 'Claude Code is signed out',
  'Claude Code 已被策略阻止': 'Claude Code is blocked by policy',
  'Codex CLI 需要更新': 'Codex CLI needs updating',
  '更新 Codex': 'Update Codex',
  '无法执行更新。': 'Could not run the update.',
  'Codex 已更新 — 请重新发送消息。': 'Codex updated — send your message again.',
  '正在更新…': 'Updating…',
  '镜像到我的手机': 'Mirror to my phone',
  '此电脑：': 'This computer:',
  '登录后启用。': 'Sign in to enable.',
  '无法列出文件': 'Couldn’t list files',
  '正在加载文件': 'Loading files',
  '筛选文件': 'Filter files',
  '无匹配项': 'No matches',
  '无文件': 'No files',
  '没有文件路径包含该文本。': 'No file path contains that text.',
  '此工作区没有可显示的文件。': 'This workspace has no files to show.',
  '在文件资源管理器中打开工作区': 'Open workspace in File Explorer',
  '选择用于打开工作区的应用': 'Choose application for workspace',
  '没有可显示的更改。': 'No changes to show.',
  '更改范围': 'Changes scope',
  '工作目录更改': 'Working directory changes',
  '让智能体解决': 'Resolve with agent',
  '正在加载更改': 'Loading changes',
  '无法加载更改': 'Couldn’t load changes',
  '尚无工作树': 'No worktree yet',
  '没有更改': 'No changes',
  '工作树是干净的 — 所有更改均已提交。': 'The working tree is clean — everything is committed.',
  '评论此行 — 将随下一条消息发送': 'Comment on this line — sent with your next message',
  '点击一行以添加评论': 'Click a line to comment',
  '二进制文件。': 'Binary file.',
  '正在加载差异…': 'Loading diff…',
  '正在加载差异查看器…': 'Loading diff viewer…',
  '差异过大，无法显示。': 'Diff too large to display.',
  '无法加载差异。': 'Could not load diff.',
  '无法加载更改。': 'Could not load changes.',
  '自上次提交以来的工作树更改': 'Working-tree changes since the last commit',
  '无法应用更改。': 'Could not apply the changes.',
  '无法在文件浏览器中打开该更改。': 'Couldn’t open that change in the file browser.',
  '该文件已不存在，已改为打开最近的现有文件夹。': 'That file no longer exists. Opened its nearest existing folder instead.',
  '将更改应用到此文件夹': 'Apply changes to this folder',
  '正在读取任务工作区…': 'Reading the thread’s workspace…',
  '详情请查看任务记录。': 'Follow the details in the thread transcript.',
  '智能体会从任务工作区打开独立 HTML 页面或启动项目开发服务器，然后在此处显示。': 'An agent opens a standalone HTML page or starts this project’s dev server from the thread workspace, then shows it here.',
  '正在准备预览': 'Preparing the preview',
  '智能体正在准备预览…': 'Agent is preparing the preview…',
  '预览设置已排队…': 'Preview setup is queued…',
  '重新加载页面': 'Reload the page',
  '预览正在启动…': 'Preview is starting…',
  '预览已运行': 'Preview is live',
  '预览失败 — 打开以重新启动': 'Preview failed — open to relaunch',
  '启动预览': 'Launch preview',
  '开发服务器进程已退出。': 'The dev server process exited.',
  '开发服务器已退出。': 'The dev server died.',
  '在浏览器中打开': 'Open in your browser',
  '关闭 HTML 预览': 'Close the HTML preview',
  '停止开发服务器': 'Stop the dev server',
  '因错误结束': 'Ended with an error',
  '任务预览': 'thread preview',
  '任务终端': 'Thread shell',
  '终端输入': 'Terminal input',
  '无法打开此文件': 'Couldn’t open this file',
  '正在加载文件…': 'Loading file…',
  '无法读取此文件': 'Could not read this file',
  '无法保存此文件': 'Could not save this file',
  '无法打开该文件夹。': 'Could not open that folder.',
  '正在显示前 512 KB': 'Showing the first 512 KB',
  '正在以只读方式显示前 512 KB': 'Showing the first 512 KB read-only',
  '此文件已在磁盘上发生变化，请重新加载后再保存。': 'This file changed on disk. Reload it before you save again.',
  '记下这个项目的想法…': 'Jot an idea for this project…',
  '还没有笔记': 'Nothing jotted yet',
  '随时记录灵感': 'Capture ideas as they come',
  '保存笔记（Enter）': 'Save note (Enter)',
  '保存笔记': 'Save note',
  '复制笔记': 'Copy note',
  '编辑笔记': 'Edit note',
  '删除笔记': 'Delete note',
  '正在加载笔记…': 'Loading notes…',
  '无法保存笔记': 'Could not save your notes',
  '无法复制到剪贴板': 'Couldn’t copy to clipboard',
  '添加到聊天': 'Add to Chat',
  '无法重新排列队列': 'Could not reorder the queue',
  '无法更改自动运行范围': 'Could not change the auto-run scope',
  '无法更改自动运行': 'Could not change auto-run',
  '无法编辑该项目': 'Could not edit that item',
  '无法将该建议加入队列': 'Could not queue that suggestion',
  '关闭赞助展示': 'Close sponsor break',
  '赞助内容间歇': 'Sponsored break',
  '赞助任务提案': 'Sponsored proposal',
  '赞助任务提案选项': 'Sponsored proposal options',
  '正在启动赞助任务…': 'Starting sponsored thread…',
  '赞助任务正在运行': 'Sponsored thread running',
  '赞助任务已提交更改': 'Sponsored thread committed its work',
  '赞助任务已创建 PR': 'Sponsored thread landed a PR',
  '赞助任务失败': 'Sponsored thread failed',
  '赞助 PR 已合并': 'Sponsored PR merged',
  '启动赞助任务': 'Start sponsored thread',
  '创建拉取请求': 'Create pull request',
  '查看执行内容': 'View what it did',
  '查看任务运行情况': 'Watch this run',
  '审阅拉取请求': 'Review the pull request',
  '在 GitHub 上查看': 'view on GitHub',
  '举报此提案': 'Report this proposal',
  '为什么会显示此内容？': 'Why this?',
  '忽略赞助任务提案': 'Dismiss sponsored proposal',
  '关闭赞助消息': 'Dismiss sponsored message',
  '关闭赞助任务提案': 'Turn off sponsored proposals',
  '无法打开该赞助任务': 'Could not open that sponsored run',
  '赞助任务未能完成。你的项目未发生任何更改。': 'The sponsored thread could not finish. Nothing was changed in your project.',
  '根据你在此项目中构建的内容匹配。未经你允许，赞助任务提案绝不会读取代码。': 'Matched to what you are building in this project. Sponsored proposals never read your code without your go-ahead.',
  '赞助任务目前无法在 Windows 上运行：Freebuff 尚无法在此操作系统中将广告方命令限制在工作区内。': 'Sponsored tasks can’t run on Windows yet: Freebuff has no way to keep an advertiser’s commands inside the workspace on this operating system.',
  '赞助任务需要 bubblewrap（`bwrap`）才能限制在工作区内运行。请安装后重新打开此项目以接受任务。': 'Sponsored tasks need bubblewrap (`bwrap`) to stay inside the workspace. Install it and reopen this project to accept.',
  '赞助任务需要 Freebuff 桌面应用；任务运行前由应用请求你的批准。请在应用中打开此项目以接受任务。': 'Sponsored tasks need the Freebuff desktop app, which is what asks you to approve the task before it runs. Open this project in the app to accept.',
  '赞助任务无法在此操作系统上运行：Freebuff 无法在这里将广告方命令限制在工作区内。': 'Sponsored tasks can’t run on this operating system: Freebuff has no way to keep an advertiser’s commands inside the workspace here.',
  '赞助任务目前无法在此处运行。': 'Sponsored tasks can’t run here yet.',
  '该模型的标签页额度已用尽——此标签页已保留默认模型。': 'That model’s tab limit is reached — this tab kept the default.',
  '登录…': 'Sign in…',
  'Shell 会话': 'Shell Session',
  '了解更多': 'Learn more',
  '请详细说明': 'Tell us more',
  '发生了什么？你的预期是什么？': 'What happened, and what did you expect?',
  '加载完成前将暂停保存': 'Saving is paused until they load',
  '记下一个想法…': 'Jot an idea…',
  '显示 Freebuff 数据需要使用桌面应用。': 'Showing Freebuff data needs the desktop app.',
  '无法打开 Freebuff 数据文件夹。': 'Could not open Freebuff’s data folder.',
  '打开 PR': 'Open PR',
  '拖动以重新排序': 'Drag to reorder',
  '受保护，不会自动归档': 'Protected from automatic archive',
  '打开暂存': 'Open the stash',
  '已暂存消息': 'Stashed messages',
  '暂存此消息': 'Stash this message',
  '仅文件': 'Files only',
  '丢弃此暂存消息': 'Discard this stashed message',
  '搜索技能': 'Search skills',
  '关闭技能搜索': 'Close skill search',
  '从头创建': 'Create from scratch',
  '使用引导模板创建新的项目技能': 'Start a new project skill with a guided template',
  '热门技能': 'Popular skills',
  '按类别筛选热门技能': 'Filter popular skills by category',
  '正在插入技能…': 'Inserting skill…',
  '立即使用技能': 'Use skill now',
  '完成技能编辑': 'Done editing skills',
  '将技能加入队列': 'Queue skill',
  '在独立 Git 工作树中运行此任务，其更改不会影响当前文件夹。': 'Run this thread in a separate Git worktree so its changes don’t affect your current folder.',
  '此文件夹不是 Git 仓库，无法使用隔离工作区；任务将直接在此文件夹中运行。': 'This folder isn’t a git repository, so isolated workspaces aren’t available. Threads run directly in the folder.',
  '此任务直接在共享项目文件夹中运行。': 'This thread runs directly in the shared project folder.',
  '共享文件夹中的当前分支 — 选择其他分支以切换': 'Current branch in this shared folder — pick another to switch',
  '将安全关闭应用，然后重新打开更新后的版本。': 'Closing safely, then reopening the updated app.',
  '更新下载期间你可以继续工作。': 'You can keep working while the update downloads.',
  '取消下载': 'Cancel download',
  '当前工作完成后，应用将重启。': 'It will restart after your active work finishes.',
  '立即重启': 'Restart now',
  '保留此版本': 'Keep this version',
  'Freebuff 更新': 'Freebuff update',
  '正在检查更新': 'Checking for updates',
  '继续工作': 'Keep working',
  '已是最新版本': 'All up to date',
  '你已使用最新可用版本。Freebuff 会继续在后台检查更新。': 'You have the latest available version. Freebuff will keep checking in the background.',
  '开发版本': 'Development build',
  '此版本无法检查更新': 'Update checks are unavailable here',
  '请安装打包版 Freebuff 以测试自动更新。': 'Install a packaged Freebuff build to test automatic updates.',
  '连接问题': 'Connection problem',
  '无法检查更新': 'Could not check for updates',
  'Freebuff 无法连接更新服务。请重试，或下载最新安装程序。': 'Freebuff could not reach the update service. Try again, or download the latest installer.',
  '手动下载': 'Download manually',
  '暂不': 'Not now',
  '完成设置': 'Finish setup',
  '将 Freebuff 移到“应用程序”': 'Move Freebuff to Applications',
  '下载安装程序': 'Download installer',
  '更新已中断': 'Update interrupted',
  '当前版本未受影响。请重试更新，或下载安装程序。': 'Your current version is untouched. Try the update again, or download the installer.',
  '立即安装可最快完成更新，也可以等当前工作完成后再安装。': 'Install now for the quickest update, or let Freebuff wait until your active work is finished.',
  '安装并重启': 'Install and restart',
  '空闲时安装': 'Install when idle',
  '跳过此版本': 'Skip this version',
  '关闭更新对话框': 'Close update dialog',
  '更新会在安装前进行验证。': 'Updates are verified before installation.',
  '获取最新安装程序': 'Get latest installer',
  '部分界面未能启动。请重新加载一次；如果此页面再次出现，请重新安装最新版本。你的项目和会话数据是安全的。': 'Part of the interface did not start. Reload once; if this screen returns, reinstall the latest version. Your projects and conversations are safe.',
  }))

  // zh → en patterns for interpolated strings the zh patch renders via
  // regexes (mechanically inverted + hand-written function inverses).
  // Order matters: most specific first.
  const patterns = [
    [/^(.+) 的新 API 密钥$/, 'New API key for $1'],
    [/^(.+) 的推理力度$/, 'Reasoning effort for $1'],
    [/^(.+) 的详细信息$/, 'Details for $1'],
    [/^全部连接器（(\d+)）$/, 'All connectors ($1)'],
    [/^会话开始于 (.+)$/, 'Session started $1'],
    [/^已退还 ([\d,.]+) Freebucks$/, '$1 Freebucks refunded'],
    [/^正在重试结束 (\d+) 个会话$/, 'Retrying $1 session ends'],
    [/^(\d+) 笔退款处理中$/, '$1 refunds processing'],
    [/^没有匹配“(.+)”的连接器。$/, 'No connector matches “$1”.'],
    [/^已连接 · 管理 (.+)$/, 'Connected · Manage $1'],
    [/^为 (.+) 选择工具$/, 'Choose tools for $1'],
    [/^连接到 (.+)？$/, 'Connect to $1?'],
    [/^运行 (.+)？$/, 'Run $1?'],
    [/^管理 (.+)$/, 'Manage $1'],
    [/^正在连接 (.+)$/, 'Connecting $1'],
    [/^重新连接 (.+)$/, 'Reconnect $1'],
    [/^连接 (.+)$/, 'Connect $1'],
    [/^审查 (.+)$/, 'Review $1'],
    [/^启用 (.+)$/, 'Enable $1'],
    [/^禁用 (.+)$/, 'Disable $1'],
    [/^将在 (.+) 后重置$/, 'resets in $1'],
    [/^开启 · 已于 (.+) 同步$/, 'On · synced $1'],
    [/^错误：(.+)$/, 'Error: $1'],
    [/^第 1\/2 步 · 正在下载 Claude Code (.+) · (\d+)%$/, 'Step 1 of 2 · Downloading Claude Code $1 · $2%'],
    [/^第 1\/2 步 · 安装 (.+) CLI$/, 'Step 1 of 2 · Install the $1 CLI'],
    [/^更新 (.+) CLI · (.+)$/, 'Update the $1 CLI · $2'],
    [/^Freebuff 有 (\d+) 个问题$/, 'Freebuff has $1 questions'],
    [/^搜索 (\d+) 个连接器…$/, 'Search $1 connectors…'],
    [/^Freebucks 已用尽 · 将在 (.+) 后补充$/, 'Out of Freebucks · more in $1'],
    [/^余额即将用尽 · 今日额度将在 (.+) 后补充$/, 'Running low · today\'s refill in $1'],
    [/^优先使用免费会话 · 今日额度将在 (.+) 后重置$/, 'Free sessions are used first · today resets in $1'],
    [/^今日高级会话已用尽 · 将在 (.+) 后重置$/, 'Today\'s premium sessions are used · resets in $1'],
    [/^将在 (\d+) 分钟后释放$/, 'frees in $1m'],
    [/^本周剩余 (\d+(?:\.\d+)?)\/(\d+(?:\.\d+)?) 个免费会话$/, '$1 of $2 free sessions left this week'],
    [/^本月剩余 (\d+(?:\.\d+)?)\/(\d+(?:\.\d+)?) 个免费会话$/, '$1 of $2 free sessions left this month'],
    [/^(.+) 方案用量$/, '$1 plan usage'],
    [/^此标签页已停止。开始运行排队项目。$/, 'This tab is stopped. Start running the queued item.'],
    [/^队列已暂停。开始运行排队项目。$/, 'The queue is paused. Start running the queued item.'],
    [/^部分模型暂未在(.+)提供$/, restoreUnavailableRegion],
    [/^检测到正在使用(.+)；使用直连网络可获得更多模型$/, restorePrivacyConnection],
    [/^今日剩余 (\d+) 个会话 · 结束时间：(.+)$/, '$1 left today · ends $2'],
    [/^完成一个悬赏任务即可解锁 · 结束时间：(.+)$/, 'Complete a bounty to unlock · ends $1'],
    [/^已获得最高奖励（(\d+)\/(\d+)）$/, 'Max bonus earned ($1/$2)'],
    [/^邀请好友，每天增加 1 个会话（已获得 (\d+)）$/, 'invite friends for +1/day ($1 earned)'],
    [/^(\d+(?:\.\d+)?)\/(\d+(?:\.\d+)?) 个高级会话$/, '$1/$2 premium sessions'],
    [/^(\d+(?:\.\d+)?)\/(\d+(?:\.\d+)?) 个会话$/, '$1/$2 sessions'],
    [/^(\d+) 天连续使用$/, '$1 day streak'],
    [/^再坚持 (\d+) 天，即可每天额外获得 1 个会话$/, '$1 more days to unlock +1 bonus session every day'],
    [/^上下文 (\d+)%。显示令牌用量详情$/, 'Context $1%. Show token usage details'],
    [/^上下文 (\d+)%$/, 'Context $1%'],
    [/^上下文 ([\d.]+[kKmM]?)$/, 'Context $1'],
    [/^账户：(.+)$/, 'Account: $1'],
    [/^(.+) 账户$/, '$1 account'],
    [/^(.+) 的工作区和设置$/, 'Workspace and settings for $1'],
    [/^插入 (.+) 技能$/, 'Insert $1 skill'],
    [/^将“(.+)”移至新窗口$/, 'Move $1 to a new window'],
    [/^关闭“(.+)”$/, 'Close $1'],
    [/^发送前编辑“(.+)”$/, 'Edit "$1" before sending'],
    [/^新建任务（(.+)）$/, 'New thread ($1)'],
    [/^关闭标签页（(.+)）$/, 'Close tab ($1)'],
    [/^自动运行已停止：(.+)$/, 'Auto-run stopped: $1'],
    [/^(.+) 存在合并冲突$/, '$1 has merge conflicts'],
    [/^(.+) 已关闭且未合并$/, '$1 closed without merge'],
    [/^(.+) — 在浏览器中打开$/, '$1 — open in browser'],
    [/^(.+) 中的任务$/, 'Threads in $1'],
    [/^在 (.+) 中新建任务$/, 'New thread in $1'],
    [/^搜索(.+)任务$/, 'Search $1 threads'],
    [/^没有匹配“(.+)”的任务$/, 'No threads match “$1”'],
    [/^预览 (.+)$/, 'Preview $1'],
    [/^正在加载 (.+)$/, 'Loading $1'],
    [/^移除 (.+)$/, 'Remove $1'],
    [/^还有 (.+) 项 — 继续输入$/, '$1 more — keep typing'],
    [/^…还有 (\d+) 项 — 继续输入以缩小范围$/, '…and $1 more — keep typing to narrow it down'],
    [/^“(.+)”的更多操作$/, 'More actions for $1'],
    [/^重新排序“(.+)”$/, 'Reorder $1'],
    [/^关闭通知：(.+)$/, 'Dismiss notification: $1'],
    [/^删除“(.+)”$/, 'Delete “$1”'],
    [/^正在安装 Freebuff (.+)$/, 'Installing Freebuff $1'],
    [/^正在下载 Freebuff (.+)$/, 'Downloading Freebuff $1'],
    [/^Freebuff (.+) 已就绪$/, 'Freebuff $1 is ready'],
    [/^Freebuff (.+) 已是最新版本$/, 'Freebuff $1 is current'],
    [/^无法安装 Freebuff (.+)$/, 'Freebuff $1 could not be installed'],
    [/^Freebuff (.+) 可用$/, 'Freebuff $1 is available'],
    [/^从 (.+) 更新到 (.+)$/, 'Update from $1 to $2'],
    [/^正在查找比 Freebuff (.+) 更新的版本。$/, 'Looking for a newer version than Freebuff $1.'],
    [/^(\d+) 像素$/, '$1 pixels'],
    [/^已使用 ([\d,.]+) 个(高级)?会话。会话会在多轮对话间保持有效；关闭标签页或 1 小时窗口到期时结束。(今日|本周)已使用 ([\d./]+) 个(高级)?会话。(此额度仅适用于当前模型。|所有高级模型共享此额度。|所有可用免费模型共享此额度。)每个会话最长持续 1 小时；提前关闭标签页会结束会话，仅按实际使用时长计费（向上取整到 0\.1）。重置时间：(.+)。$/, restoreActiveSessionTooltip],
    [/^(今日|本周)已使用 ([\d./]+) 个(高级)?会话。(此额度仅适用于当前模型。|所有高级模型共享此额度。|所有可用免费模型共享此额度。)每个会话最长持续 1 小时；提前关闭标签页会结束会话，仅按实际使用时长计费（向上取整到 0\.1）。重置时间：(.+)。$/, restoreSessionQuotaDetails],
    [/^你已用尽(今日|本周)的 ([\d,.]+) 个(高级会话|当前模型的会话|会话)。(切换到 DeepSeek V4 Flash 以继续。)?重置时间：(.+)。$/, restoreExhaustedSessionTooltip],
    [/^(今日|本周)的(高级会话|当前模型的会话|会话)已用尽 · 将在 (.+) 后重置$/, restoreOutOfSessions],
  ]

  const translatedAttributes = ['aria-label', 'aria-valuetext', 'data-tooltip', 'placeholder', 'title']

  // zh → en tool-header labels (context-gated, same selectors as the zh patch).
  const toolNameLabels = { 搜索: 'Search', 读取: 'Read', 运行: 'Run' }
  const toolStatusLabels = { 成功: 'success', 失败: 'failure', 运行中: 'running' }


  // ── Hand-written inverses of the zh patch's replacement functions ──
  // Region names zh → en (Intl.DisplayNames, reversed from the zh patch).
  let regionNameTranslations = null
  function translateRegionName(region) {
    if (region === '你所在的地区') return 'your region'
    if (!regionNameTranslations) {
      regionNameTranslations = new Map()
      try {
        const chineseNames = new Intl.DisplayNames(['zh-CN'], { type: 'region' })
        const englishNames = new Intl.DisplayNames(['en'], { type: 'region' })
        for (let first = 65; first <= 90; first += 1) {
          for (let second = 65; second <= 90; second += 1) {
            const code = String.fromCharCode(first, second)
            const chinese = chineseNames.of(code)
            const english = englishNames.of(code)
            if (chinese && chinese !== code && english && english !== code) {
              regionNameTranslations.set(chinese, english)
            }
          }
        }
      } catch {
        // Keep the Chinese region name when Intl.DisplayNames is unavailable.
      }
    }
    return regionNameTranslations.get(region) ?? null
  }

  function restoreUnavailableRegion(_match, zhRegion) {
    const en = translateRegionName(zhRegion)
    return en === null ? null : `Some models aren't available in ${en} yet`
  }

  function restorePrivacyConnection(_match, signalList) {
    const labels = {
      '匿名网络': 'anonymized network',
      '代理': 'proxy',
      '中继': 'relay',
      '住宅代理': 'residential proxy',
      'Tor': 'Tor',
      'VPN': 'VPN',
      '托管网络': 'hosting network',
      '隐私服务': 'privacy service',
    }
    const restored = signalList
      .split('、')
      .filter(Boolean)
      .map((signal) => labels[signal] ?? signal)
      .join(', ')
    return `Using a ${restored}? More models are available on a direct connection`
  }

  function restoreSessionPeriod(period) {
    return period === '本周' ? 'this week' : 'today'
  }

  function restoreSessionUnit(premium) {
    return premium ? 'premium sessions' : 'sessions'
  }

  function restoreSessionScope(scope) {
    const translations = {
      '此额度仅适用于当前模型。': 'Specific to this model',
      '所有高级模型共享此额度。': 'Shared across all premium models',
      '所有可用免费模型共享此额度。': 'Shared across all available free models',
    }
    return translations[scope] ?? scope
  }

  function restoreSessionTier(tier) {
    const translations = {
      '高级会话': 'premium sessions',
      '当前模型的会话': 'sessions for this model',
      '会话': 'sessions',
    }
    return translations[tier] ?? tier
  }

  function restoreActiveSessionTooltip(_match, cost, activePremium, period, ratio, quotaPremium, scope, reset) {
    const unit = activePremium ? 'premium ' : ''
    const quotaUnit = quotaPremium ? 'premium ' : ''
    return `Used ${cost} ${unit}sessions so far. Stays active between turns; ends when you close the tab or its 1-hour session expires. ${ratio} ${quotaUnit}sessions used ${restoreSessionPeriod(period)}. ${restoreSessionScope(scope)}. Each lasts up to 1 hour; closing the tab ends it early, counts only time used (rounded up to 0.1). Resets ${reset}.`
  }

  function restoreSessionQuotaDetails(_match, period, ratio, premium, scope, reset) {
    const unit = premium ? 'premium ' : ''
    return `${ratio} ${unit}sessions used ${restoreSessionPeriod(period)}. ${restoreSessionScope(scope)}. Each lasts up to 1 hour; closing the tab ends it early, counts only time used (rounded up to 0.1). Resets ${reset}.`
  }

  function restoreExhaustedSessionTooltip(_match, period, limit, tier, switchHint, reset) {
    const hint = switchHint ? ' Switch to DeepSeek V4 Flash to keep going.' : ''
    return `You've used all ${limit} ${restoreSessionTier(tier)} ${restoreSessionPeriod(period)}.${hint} Resets ${reset}.`
  }

  function restoreOutOfSessions(_match, period, tier, remaining) {
    return `Out of ${restoreSessionTier(tier)} ${restoreSessionPeriod(period)} · resets in ${remaining}`
  }

  const stats = { text: 0, attributes: 0, contextual: 0, displaced: 0, passes: 0 }

  // Content regions that are user data or code — never translated (same
  // ignore list as the zh patch, so behavior is identical in both directions).
  const ignoredContentSelector = [
    'script', 'style', 'code', 'pre', 'textarea',
    '[data-freebuff-en-ignore]',
    '[contenteditable]:not([contenteditable="false"])',
    '.bubble', '.user-sticky-bubble', '.user-message-text', '.prose',
    '.fold-reasoning-text', '.reasoning-text', '.act-arg',
    '.tool-row-details', '.file-view-body', '.change-diff', '.diff-comment-text',
    '.xterm', '.terminal-context-output', '.note-text', '.qnote',
  ].join(',')

  function shouldIgnoreContent(element) {
    return Boolean(element?.closest?.(ignoredContentSelector) ||
      (element?.closest?.('.byok-saved-heading') && element?.closest?.('strong')))
  }

  function translateValue(value) {
    if (typeof value !== 'string' || value.length === 0) return value
    if (!CJK_RE.test(value)) return value
    const leading = value.match(/^\s*/)?.[0] ?? ''
    const trailing = value.match(/\s*$/)?.[0] ?? ''
    const key = value.trim()
    if (!key) return value
    const direct = exact.get(key)
    if (direct !== undefined) return `${leading}${direct}${trailing}`
    for (const [pattern, replacement] of patterns) {
      if (!pattern.test(key)) continue
      const out = typeof replacement === 'function'
        ? replacement(key, ...key.match(pattern).slice(1))
        : key.replace(pattern, replacement)
      if (out === null || out === undefined) continue
      return `${leading}${out}${trailing}`
    }
    return value
  }

  function translateUiLabel(value, element) {
    const key = value.trim()
    let translated
    const isToolHeader =
      element.parentElement?.matches?.('.tool-row-head') &&
      element.parentElement.parentElement?.matches?.('.tool-row')
    if (isToolHeader && element.matches?.('.act-name') && Object.hasOwn(toolNameLabels, key)) {
      translated = toolNameLabels[key]
    } else if (isToolHeader && element.matches?.('.tool-row-status') && Object.hasOwn(toolStatusLabels, key)) {
      translated = toolStatusLabels[key]
    } else if (element.tagName === 'BUTTON' && element.matches?.('.quote-btn') && key === '引用') {
      translated = 'Quote'
    }
    return translated === undefined ? null : value.replace(key, translated)
  }

  function translateTextNode(node) {
    const before = node.nodeValue
    // Fast reject: no translation surface can change a CJK-free string.
    if (!before || !CJK_RE.test(before)) return
    const parent = node.parentElement
    if (!parent) return
    if (parent.matches?.('.agent-option-title') && parent.closest?.('.agent-provider-option')) return
    if (shouldIgnoreContent(parent)) return
    const after = translateUiLabel(before, parent) ?? translateValue(before)
    if (after !== before) {
      node.nodeValue = after
      stats.text += 1
    }
  }

  function translateAttributes(element) {
    if (shouldIgnoreContent(element)) return
    for (const name of translatedAttributes) {
      if (!element.hasAttribute(name)) continue
      const before = element.getAttribute(name)
      if (!CJK_RE.test(before)) continue
      const after = translateValue(before)
      if (after !== before) {
        element.setAttribute(name, after)
        stats.attributes += 1
      }
    }
  }

  function applyContextRules(scope) {
    if (!scope?.querySelectorAll) return
    // Restore the "Agent changed N files" heading the zh patch localized.
    for (const heading of scope.querySelectorAll('.turn-changes-head')) {
      if (shouldIgnoreContent(heading)) continue
      const label = Array.from(heading.querySelectorAll('span')).find((candidate) =>
        /^智能体修改了\s+\d+\s+个文件$/.test(candidate.textContent.trim()))
      if (!label || shouldIgnoreContent(label)) continue
      const match = label.textContent.trim().match(/^智能体修改了\s+(\d+)\s+个文件$/)
      if (!match) continue
      label.textContent = `Agent changed ${match[1]} files`
      stats.contextual += 1
    }
    // Note: the zh patch's .acts-toggle rule strips a trailing 's' child
    // (singular/plural split). Singular/plural is not recoverable from the
    // DOM, so this patch intentionally leaves those labels untouched.
  }

  function isTranslatableRoot(node) {
    return node.nodeType === Node.ELEMENT_NODE ||
      node.nodeType === Node.DOCUMENT_NODE ||
      node.nodeType === Node.DOCUMENT_FRAGMENT_NODE
  }

  function translateTree(root) {
    if (!root) return
    if (root.nodeType === Node.TEXT_NODE) {
      translateTextNode(root)
      applyContextRules(root.parentElement)
      return
    }
    if (!isTranslatableRoot(root)) return
    // Whole ignorable subtrees (user code, prose, terminals) are skipped —
    // every node inside would fail shouldIgnoreContent anyway.
    if (root.nodeType === Node.ELEMENT_NODE && shouldIgnoreContent(root)) return
    if (root.nodeType === Node.ELEMENT_NODE) translateAttributes(root)
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT)
    let current
    while ((current = walker.nextNode())) {
      if (current.nodeType === Node.TEXT_NODE) translateTextNode(current)
      else if (!shouldIgnoreContent(current)) translateAttributes(current)
    }
    applyContextRules(root)
    stats.passes += 1
  }

  function start() {
    // Take over from a live zh patch: its observer would re-translate every
    // node this patch restores (mutual ping-pong). Disconnecting it makes
    // this patch a true restore layer.
    const zhObserver = globalThis.__FREEBUFF_ZH_PATCH__?.observer
    if (zhObserver?.disconnect) {
      zhObserver.disconnect()
      stats.displaced += 1
    }
    document.documentElement.lang = 'en'
    translateTree(document.documentElement)
    // Coalesce mutation bursts: dedupe targets in sets, flush in one
    // microtask (before paint, same-frame latency as per-record handling).
    const pendingText = new Set()
    const pendingAttrs = new Set()
    const pendingNodes = new Set()
    let scheduled = false
    const flush = () => {
      scheduled = false
      const text = [...pendingText]; pendingText.clear()
      const attrs = [...pendingAttrs]; pendingAttrs.clear()
      const nodes = [...pendingNodes]; pendingNodes.clear()
      for (const node of nodes) translateTree(node)
      for (const node of text) translateTree(node)
      for (const element of attrs) translateAttributes(element)
    }
    const schedule = () => {
      if (scheduled) return
      scheduled = true
      queueMicrotask(flush)
    }
    const observer = new MutationObserver((mutations) => {
      for (const mutation of mutations) {
        if (mutation.type === 'characterData') pendingText.add(mutation.target)
        else if (mutation.type === 'attributes') pendingAttrs.add(mutation.target)
        else for (const node of mutation.addedNodes) pendingNodes.add(node)
      }
      schedule()
    })
    observer.observe(document.documentElement, {
      subtree: true,
      childList: true,
      characterData: true,
      attributes: true,
      attributeFilter: translatedAttributes,
    })
    globalThis.__FREEBUFF_EN_PATCH__.observer = observer
  }

  globalThis.__FREEBUFF_EN_PATCH__ = {
    id: PATCH_ID,
    version: PATCH_VERSION,
    stats,
    translate: translateValue,
    translateAll: () => translateTree(document.documentElement),
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true })
  else start()
})()
