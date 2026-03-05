---
name: test-driven-development  
description: Use when implementing any feature or bugfix, before writing implementation code。  看不到这个头吗？
---

❯ 关于agent与大模型的交互过程，我是这样理解的：agent 获取用户的输入，然后携带该agent可用的tool列表打包一起发送给大模型，大模型通过输入的提示词和tool列表，决定使用哪个tool查询哪些信息（但是大模型本身不执行tool），并把这      
些操作返回给agent来执行（可能会有多轮交互执行），最终形成结果发给用户。请问我这样理解有什么问题吗？

● 你的理解基本正确，核心流程没有问题。但有两个细节值得补充，对照 main.go 的代码会更清晰：

1. 工具执行结果不是直接给用户，而是回传给大模型

这是最容易忽略的一点。流程不是：
```
大模型 → 指示调用工具 → Agent 执行 → 结果给用户
```
而是：
```
大模型 → 指示调用工具 → Agent 执行 → 结果回传给大模型 → 大模型决定下一步
```
对应代码第 100-158 行的循环：
```go
// 把模型的回复加入对话历史
messages = append(messages, message.ToParam())

// 收集所有工具执行结果
for _, block := range message.Content {
// ... 执行工具，拿到 response
toolResults = append(toolResults, anthropic.NewToolResultBlock(...))
}

// 工具结果作为 user 消息回传给模型
messages = append(messages, anthropic.NewUserMessage(toolResults...))
// 然后回到循环顶部，再次请求模型

if response.stop_reason != "tool_use" {
	// 说明模型不需要继续执行工具，直接返回结果给用户
}
```
大模型拿到工具结果后，自己决定：
```
- 还需要调更多工具？→ 继续返回 tool_use（如 Round 2 用坐标去查天气）
- 信息够了？→ 返回 text，生成最终回答
```
2. 大模型不仅选择工具，还生成调用参数

你说"决定使用哪个 tool 查询哪些信息"已经隐含了这点，但值得明确：大模型输出的是结构化的函数调用指令，包括工具名和具体参数：
```json
{
    "type": "tool_use",
    "name": "get_weather",
    "input": {"lat": 37.7749, "long": -122.4194, "unit": "fahrenheit"}
}
```
参数值是大模型根据上下文（用户问题 + 前几轮工具返回的数据）推理生成的，不是从固定选项中选择。

完整循环图
```
用户: "旧金山天气怎样？"
│
▼
┌─── Agent 循环 ────────────────────────────────┐
│                                                │
│  ┌──────────┐   消息 + 工具列表                │
│  │  大模型   │◄──────────────────┐              │
│  └────┬─────┘                   │              │
│       │                         │              │
│       ├─ 返回 tool_use ──► Agent 执行工具      │
│       │                    结果回传给大模型 ────┘│
│       │                                        │
│       └─ 返回 text ──► 退出循环                │
│                                                │
└────────────────────────────────────────────────┘
│
▼
用户收到最终回答
```
所以 Agent 本质上是一个循环调度器：它不理解语义，只负责把消息转发给大模型、执行大模型指定的工具、把结果送回去，直到大模型认为任务完成。所有"智能"都在大模型侧。

---
---

大模型直接连mcp server 这点与我实际测试的结果有差异，我本地部署了一个mcp服务:
```json                                                                 
{                                                                                    
"mcpServers": {                                                         
    "gw": {                                                              
        "type": "sse",                                                      
        "url": "http://192.168.168.176:8080/sse"                             
    }                                                                    
}                                                                    
```
这是一个内网部署的mcp服务，很显然大模型无法连接，但是claude code 却可以使用。

● 你说得对，我之前的解释有误。你的实测结果揭示了关键点：Claude Code 里的 MCP 并不是大模型直连 MCP Server。

Claude Code 中 MCP 的真实流程

Claude Code 自己充当 MCP Client，流程其实和普通 Tool 完全一样：
```
                      Claude Code（Agent）
                           │
            ┌──────────────┼──────────────┐
            ▼              │              ▼
     连接 MCP Server       │        Anthropic API
     发现工具列表          │         （大模型）
            │              │              │
            ▼              │              │
     转换成普通 Tool Schema ──────────►  发送给大模型
                           │              │
                           │         返回 tool_use
                           │              │
                           ◄──────────────┘
                           │
                      Claude Code 调用
                      MCP Server 执行工具
                           │
                      把结果回传给大模型
                           │
                      大模型返回最终回答
```
本质上就是：
```
1. Claude Code 连接你的 MCP Server（192.168.168.176:8080/sse）
2. 发现工具列表（MCP 协议的 tools/list）
3. 把 MCP 工具转换成普通的 Tool Schema，和其他工具一起发给大模型
4. 大模型返回 tool_use → Claude Code 去调 MCP Server 执行（MCP 协议的 tools/call）
5. 结果回传大模型
```
大模型全程不知道 MCP 的存在，它看到的只是普通的 tool 定义。MCP 只是 Claude Code 这个 Agent 获取和执行工具的一种方式。

所以实际上有两种 MCP 模式
```
┌───────────────────┬──────────────────────────────────┬─────────────────────────────────────────┐
│                   │ Claude Code 的方式（客户端 MCP） │ Anthropic Beta API 的方式（服务端 MCP） │
├───────────────────┼──────────────────────────────────┼─────────────────────────────────────────┤
│ 谁连 MCP Server   │ Agent（Claude Code）             │ Anthropic 服务端                        │
├───────────────────┼──────────────────────────────────┼─────────────────────────────────────────┤
│ 内网 MCP 能用吗   │ 能（你的场景）                   │ 不能（服务端访问不到内网）              │
├───────────────────┼──────────────────────────────────┼─────────────────────────────────────────┤
│ 大模型感知 MCP 吗 │ 不感知，只看到普通 tool          │ 感知，有专门的 mcp_tool_use 类型        │
├───────────────────┼──────────────────────────────────┼─────────────────────────────────────────┤
│ 对应 API          │ 稳定版（普通 tools 参数）        │ Beta API（mcp_servers 参数）            │
└───────────────────┴──────────────────────────────────┴─────────────────────────────────────────┘
```
你的实测完美验证了这一点——内网地址 192.168.168.176 Anthropic 服务端根本访问不到，能用只说明是 Claude Code 本地在连接和执行。

之前我把 SDK Beta API 的服务端 MCP 模式和 Claude Code 的客户端 MCP 模式混为一谈了，感谢你的纠正。

✻ Cogitated for 34s

❯ 除了mcp server，我了解到还有一个 skill 的概念，这个是什么呢？如何与agent和大模型交互的呢？

● Skill 和 Tool/MCP 是完全不同层次的概念。简单说：

Skill 是什么

Skill 本质上就是一段预写好的提示词（Prompt 模板），不是 API 层面的工具。

它的作用是：当 Claude Code 遇到特定类型的任务时，加载一套结构化的指令来引导自己的行为。

对比三者
```
┌─────────────────────────────────────────────────┐
│              Claude Code（Agent）                │
│                                                  │
│  ┌──────────┐                                    │
│  │  Skill   │  ← 纯文本指令，注入到对话上下文     │
│  │ (提示词)  │     不涉及任何外部调用              │
│  └──────────┘                                    │
│                                                  │
│  ┌──────────┐     ┌──────────┐                   │
│  │  Tool    │     │MCP Server│                   │
│  │(本地函数) │     │(外部服务) │                   │
│  └──────────┘     └──────────┘                   │
│       ↕                ↕                         │
│    Agent 执行       Agent 执行                    │
│                                                  │
│          ↕ 全部打包成 tool schema                  │
│                                                  │
│     Anthropic API（大模型）                       │
└─────────────────────────────────────────────────┘

┌────────────────┬─────────────────────┬────────────────────────┬───────────────────────────┐
│                │        Tool         │          MCP           │           Skill           │
├────────────────┼─────────────────────┼────────────────────────┼───────────────────────────┤
│ 本质           │ 函数定义 + 执行逻辑 │ 外部服务协议           │ 提示词模板                │
├────────────────┼─────────────────────┼────────────────────────┼───────────────────────────┤
│ 大模型能看到吗 │ 能（tool schema）   │ 能（转成 tool schema） │ 不能（只影响 Agent 行为） │
├────────────────┼─────────────────────┼────────────────────────┼───────────────────────────┤
│ 涉及外部调用吗 │ 看实现              │ 是（MCP Server）       │ 否                        │
├────────────────┼─────────────────────┼────────────────────────┼───────────────────────────┤
│ 谁在用         │ 大模型决定调用      │ 大模型决定调用         │ Agent 自己遵循            │
└────────────────┴─────────────────────┴────────────────────────┴───────────────────────────┘
```
Skill 的交互流程

以 test-driven-development 这个 skill 为例：
```
用户: "给登录功能加个验证"
│
▼
Claude Code 判断: 这是实现功能 → 匹配 TDD skill
│
▼
加载 skill 内容（一段 markdown 指令），注入到对话上下文:
"你必须先写测试，再写实现，测试通过后再重构..."
│
▼
Claude Code 按照这套指令行事:
1. 先写 test
2. 运行 test（失败）
3. 写实现代码
4. 运行 test（通过）
5. 重构
│
▼
```
整个过程中与大模型的交互还是正常的 tool-use 流程,
只是 Claude Code 的"行为模式"被 skill 约束了

打个比喻

- Tool：给了你一把锤子（工具），你决定什么时候用
- MCP：给了你一个五金店的地址（服务），你可以去那里拿各种工具
- Skill：给了你一本《施工规范手册》（流程指导），告诉你应该怎么干活，但不提供任何工具

Skill 不参与 Agent 和大模型之间的 tool-use 交互循环，它只是改变了 Agent（Claude Code）的工作方式和决策逻辑。

---
---

你举例的  test-driven-development 这个 skill 具体是啥样的？有哪些内容？

● Skill(superpowers:test-driven-development)                                                                                                                                                                                    
⎿  Successfully loaded skill

● 以上就是 test-driven-development skill 的完整内容。可以看到它就是一份纯文本的工作规范文档，包含：

Skill 的结构
```
┌─ 概述 ─────────── 核心原则："没看到测试失败，就不知道测试是否正确"                                                                                                                                                          
│                                                                                                                                                                                                                             
├─ 适用场景 ──────── 新功能、Bug修复、重构...
│
├─ 核心流程 ──────── Red → Green → Refactor 循环
│  │
│  ├─ RED:    先写一个会失败的测试
│  ├─ 验证:   运行测试，确认它确实失败了（必须做）
│  ├─ GREEN:  写最少的代码让测试通过
│  ├─ 验证:   运行测试，确认通过（必须做）
│  └─ REFACTOR: 清理代码，保持测试通过
│
├─ Good/Bad 示例 ── 什么样的测试是好的、坏的
│
├─ 常见借口表 ────── "太简单不用测" → 反驳
│                   "先写完再补测试" → 反驳
│                   "删掉X小时的代码太浪费" → 反驳
│
├─ 红线清单 ──────── 出现这些情况必须停下来重新开始
│
└─ 验证 Checklist ── 完成前逐项检查
```
它如何影响 Claude Code 的行为

加载这个 skill 后，Claude Code 在实现功能时会被约束为：
```
用户: "给登录加邮箱验证"
│
▼
Claude Code（已加载 TDD skill）
│
├─ 1. 先写测试代码 test_email_validation
├─ 2. 运行测试 → 确认失败 ✗
├─ 3. 写最小实现代码
├─ 4. 运行测试 → 确认通过 ✓
├─ 5. 重构清理
└─ 6. 对照 Checklist 检查
```
如果没有这个 skill，Claude Code 可能直接写完实现代码再补测试，甚至不写测试。

所以 skill 的本质就是：预先注入一套行为规范到对话上下文里，让大模型按照特定的方法论来工作。它不提供任何工具能力，只改变工作方式。

❯ 既然skill只是一份纯文本的工作规范文档，那么claude code 是如何理解这个skill，识别并注入到自己的对话上下文中的呢？请举例一个非常简单的skill来说明。

● 这个问题问得好。让我拆解 Claude Code 加载 skill 的完整机制。

第一步：系统启动时，只加载技能清单

Claude Code 启动时，系统提示词里会注入一份技能目录（只有名称 + 简短描述，不含完整内容）：
```
The following skills are available for use with the Skill tool:

- simplify: Review changed code for reuse, quality, and efficiency
- superpowers:brainstorming: You MUST use this before any creative work
- superpowers:test-driven-development: Use when implementing any feature or bugfix
- superpowers:systematic-debugging: Use when encountering any bug or test failure
  ...
```
这就是大模型在每轮对话中始终能看到的部分——一个索引目录。
```
第二步：用户输入 → 大模型匹配 skill

用户: "帮我实现一个登录功能"
│
▼
大模型内部推理（这就是 LLM 在做的事）:
"实现功能" → 匹配到 "test-driven-development:
Use when implementing any feature"
│
▼
大模型调用 Skill 工具: Skill("superpowers:test-driven-development")

本质上就是大模型做文本匹配——用户意图 vs 技能描述。和大模型决定调用哪个 tool 是一样的推理过程。
```
第三步：Skill 工具加载完整内容
```
Skill 工具被调用
│
▼
从文件系统读取完整 skill 内容（那份长长的 markdown）
│
▼
作为工具返回结果注入到对话上下文中
│
▼
大模型在后续回复中就能看到并遵循这些指令
```
用一个最简单的 skill 举例

假设创建一个 always-say-hi skill：

skill 文件结构：
~/.claude/skills/always-say-hi.md

文件内容：
```
---
name: always-say-hi
description: Use when user greets you
  ---
```
# Always Say Hi

在回复用户问候时，必须先说"你好朋友！"再回答问题。

完整交互过程：
```
┌─ 系统启动 ────────────────────────────────────────────────┐
│                                                            │
│  系统提示词自动包含:                                        │
│  "Available skills:                                        │
│   - always-say-hi: Use when user greets you"               │
│                                                            │
│  注意: 此时大模型只看到这一行描述，看不到完整内容             │
└────────────────────────────────────────────────────────────┘
│
▼
┌─ 用户输入 ────────────────────────────────────────────────┐
│  用户: "你好，帮我看个bug"                                  │
└────────────────────────────────────────────────────────────┘
│
▼
┌─ 大模型推理 ──────────────────────────────────────────────┐
│                                                            │
│  "你好" → 这是问候 → 匹配 "always-say-hi: Use when        │
│                              user greets you"              │
│                                                            │
│  决定: 调用 Skill 工具加载它                                │
└────────────────────────────────────────────────────────────┘
│
▼
┌─ Skill 工具执行 ─────────────────────────────────────────┐
│                                                            │
│  读取 ~/.claude/skills/always-say-hi.md                    │
│  返回完整内容: "在回复用户问候时，必须先说'你好朋友！'..."    │
│                                                            │
│  这段内容现在成为对话上下文的一部分                           │
└────────────────────────────────────────────────────────────┘
│
▼
┌─ 大模型回复 ──────────────────────────────────────────────┐
│                                                            │
│  "你好朋友！来看看你的 bug..."                              │
│   ↑                                                        │
│   遵循了 skill 中的指令                                     │
└────────────────────────────────────────────────────────────┘
```
总结：Skill 的机制就三句话
```
1. 注册：skill 文件的 description 字段被提取，作为目录常驻系统提示词
2. 匹配：大模型用自然语言理解能力，把用户意图和 skill 描述做匹配
3. 注入：通过 Skill 工具把完整内容加载到对话上下文，大模型后续遵循执行
```
没有任何"魔法"——skill 系统完全建立在大模型的文本理解能力之上。Skill 目录本身就是提示词的一部分，匹配是大模型的推理，注入是工具调用的返回值。整个过程和 tool-use 循环用的是同一套机制。

✻ Cooked for 45s


❯ 看来一切都是文本啊，只是skill会将文本注入到上下文中来影响大模型的决策。 我通过 /skill 命令发现我本地已经安装了 test-driven-development 这个skill，请问这个skill在本地的文件系统位置在哪里啊

● 之前加载 TDD skill 时，系统已经显示了路径：
```
C:\Users\lzb\.claude\plugins\cache\claude-plugins-official\superpowers\4.3.1\skills\test-driven-development
```
● Read 2 files (ctrl+o to expand)

● 结构很清晰：
```
C:\Users\lzb\.claude\plugins\cache\claude-plugins-official\superpowers\4.3.1\                                                                                                                                                 
└── skills/                                                                                                                                                                                                                   
├── brainstorming/                                                                                                                                                                                                        
├── dispatching-parallel-agents/                                                                                                                                                                                          
├── executing-plans/                                                                                                                                                                                                      
├── finishing-a-development-branch/                                                                                                                                                                                       
├── receiving-code-review/                                                                                                                                                                                                
├── requesting-code-review/
├── subagent-driven-development/
├── systematic-debugging/
├── test-driven-development/
│   ├── SKILL.md                    ← 主文件（就是刚才看到的那份长文档）
│   └── testing-anti-patterns.md    ← 被 SKILL.md 引用的补充材料
├── using-git-worktrees/
├── using-superpowers/
├── verification-before-completion/
├── writing-plans/
└── writing-skills/
```
每个 skill 就是一个文件夹，核心是 SKILL.md。这些文件来自 claude-plugins-official 插件包（superpowers 4.3.1 版本），缓存在 ~/.claude/plugins/cache/ 下。

你的总结完全正确——一切都是文本。Skill 只是一种结构化的方式，把特定场景的提示词组织起来，在需要时注入上下文来引导大模型的行为。

每个skill.md 都是一下面的模板开头的：  

---
name: test-driven-development  
description: Use when implementing any feature or bugfix, before writing implementation code
---