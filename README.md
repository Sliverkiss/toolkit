<p align="center">
  <img src="logo.png" alt="toolkit" width="200">
</p>

<h1 align="center">toolkit</h1>

<p align="center">
  <b>给任意 LLM API 补上函数调用能力的纯 Go 中间件库</b><br>
  XYML 注入 · 文本解析回 tool_calls · 协议防泄漏 · 流式重组
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.22-00ADD8?logo=go&logoColor=white&style=flat-square">
  <img alt="License" src="https://img.shields.io/badge/License-CC_BY--NC--SA_4.0-EF9421?style=flat-square">
  <img alt="Dependencies" src="https://img.shields.io/badge/依赖-仅_yaml.v3-375A7F?style=flat-square">
  <img alt="API" src="https://img.shields.io/badge/兼容-OpenAI_Protocol-412991?style=flat-square">
  <img alt="Streaming" src="https://img.shields.io/badge/流式-SSE_重组-0DBD8B?style=flat-square">
  <a href="https://t.me/sliverkiss_blog"><img alt="Telegram" src="https://img.shields.io/badge/Telegram-%E9%A2%91%E9%81%93-blue?logo=telegram&logoColor=white&style=flat-square"></a>
</p>

---

## 项目简介

`toolkit` 是一个可嵌入的 **Go 函数调用中间件库**。当你的上游 LLM 不支持原生 function calling 时——不管是自建网关、第三方反代还是本地模型服务——toolkit 把工具定义以紧凑的 **XYML 协议块**注入 system prompt，将模型的文本输出解析回标准 `tool_calls`，清洗协议泄漏，并在流式场景下逐段重组工具调用片段。

**客户端无感知**：进来的是标准 OpenAI Chat Completions（带 `tools`），出去的是标准 `tool_calls`，`role:"tool"` 的结果再喂回来即可。**上游也无感知**：它只看到普通文本对话。

### 本库做什么

- **XYML 注入** — 把工具定义渲染成紧凑协议块合并进消息列表，支持 `auto` / `none`（不注入）/ `required` / 指定函数名四种 `tool_choice`；
- **文本→tool_calls 解析** — 解析模型输出的 XYML 调用块，容忍 CDATA 嵌套、标签融合、闭合标签特异性等边角语法（r24 / r33 / r35 三轮解析器加固）；
- **协议防泄漏** — 筛子（sieve）保证任何无法解析的协议文本都被剥离，XYML / DSML 标记永不漏到客户端；
- **流式重组** — 文本增量逐段喂入重组器，安全内容增量下发，完整 tool_calls 在流末统一冲刷，顺序保证：role → content → tool_calls → finish_reason → usage；
- **畸形恢复** — 半截调用、缺参、截断输出均有恢复路径，不给客户端喂坏数据。

### 本库不做什么

- **不做上游请求** — toolkit 是纯库，HTTP 发送由你的网关自己完成；
- **不做账号池 / 路由 / 鉴权** — 那是网关（如 workbuddy2api / chatglm2api）的职责；
- **不做下游协议转换** — 只聚焦函数调用这一层，Anthropic Messages、Gemini 适配交给下游。

## 包结构

| 包 | 职责 |
|---|---|
| `models` | 规范化内部类型：消息、工具定义、工具调用 |
| `jsonx` | 宽容 JSON 处理，吃下任意客户端负载 |
| `util` | token 估算、id 生成、SSE 工具、缓存、指标 |
| `config` | YAML 配置 + `${ENV}` 环境变量插值 |
| `engine` | XYML 核心：语法、解析、渲染、筛子（CDATA 嵌套、标签融合、闭合特异性） |
| `fc` | 注入、文本→tool_calls 解析、泄漏清洗、畸形恢复、模式策略 |
| `convert` | 协议转换（OpenAI Chat ⇄ Responses API） |
| `reqlog` | 请求日志辅助 |
| `stream` | 双协议的流式 tool_calls 重组 |
| `adapters` | 客户端协议适配类型 |

## 快速上手

```go
import (
    "github.com/Sliverkiss/toolkit/fc"
    "github.com/Sliverkiss/toolkit/models"
)

// 1. 注入：把 XYML 工具协议块合并进消息列表
msgs := fc.InjectPromptMessages(messages, tools, "openai", true)

// 2. 把 msgs 发往你的上游（任意 HTTP 客户端）……

// 3. 把模型文本解析回标准 tool calls
calls, err := fc.ParseTextToCalls(modelText, tools, "openai", true)
```

流式场景：把每个文本增量喂给 `stream.NewReassembler`——它缓存半截协议片段、增量下发安全内容、在流末冲刷完整 tool_calls。筛子保证任何 XYML / DSML 标记不会漏给你的客户端。

## 工作原理

整体链路：客户端说标准 OpenAI 协议，toolkit 在中间做 XYML 注入 / 解析 / 清洗，上游只看到纯文本对话。

```mermaid
flowchart LR
    subgraph CLIENT["客户端"]
        SDK["OpenAI SDK / 任意工具"]
    end

    subgraph TOOLKIT["toolkit（本库）"]
        direction TB
        INJ["fc — XYML 协议块注入<br/>system prompt"] --> UP["你的网关发请求<br/>（HTTP 由你完成）"]
        UP --> REASM["stream — 流式重组器<br/>缓存半截片段 · 增量下发"]
        REASM --> PARSE["fc / engine — 解析回 tool_calls<br/>CDATA 嵌套 · 标签融合容错"]
        PARSE --> SIEVE["engine sieve — 协议防泄漏<br/>解析失败的标记一律剥离"]
    end

    subgraph UPSTREAM["上游（无原生 FC）"]
        LLM["LLM API<br/>反代 / 自建 / 本地模型"]
    end

    SDK -- "tools / tool_choice" --> INJ
    SIEVE -- "普通 prompt" --> LLM
    LLM -- "文本流 (SSE)" --> REASM
    SIEVE -- "标准 tool_calls" --> SDK
    SDK -- "role:tool 结果" --> INJ
```

一次完整调用的时序：

```mermaid
sequenceDiagram
    participant C as 客户端 (OpenAI SDK)
    participant T as toolkit
    participant U as 上游 LLM

    C->>T: POST /chat/completions (tools, tool_choice)
    T->>T: fc 注入 XYML 协议块 (none 则零注入)
    T->>U: 纯文本消息列表
    U-->>T: 文本流 (SSE deltas)
    T->>T: stream 重组器缓存片段 · sieve 剥离协议标记
    T-->>C: 安全内容增量下发
    T->>T: 流末解析完整 tool_calls (engine XYML 解析)
    T-->>C: tool_calls + finish_reason:"tool_calls"
    C->>T: role:"tool" 执行结果
    T->>U: 渲染 [Tool Result] 块，继续下一轮
    U-->>C: 最终回答
```

包依赖层次（下层不感知上层）：

```mermaid
flowchart TD
    stream --> convert & engine & fc & reqlog & util
    adapters --> convert & engine & fc & util
    convert --> engine & jsonx & models & util
    fc --> config & engine & jsonx & models
    engine --> jsonx & models & util
    models & reqlog & config --> jsonx
    util --> jsonx & models
```

行为细节：

- `tool_choice` 全量支持：`auto`、`none`（零注入）、`required`、指定函数名；
- `tool_choice` 中未知的工具名提前拒绝；
- 解析不了的协议文本一律剥离，绝不泄漏；
- 流式顺序严格保持：role → content* → tool_calls → finish_reason → usage。

## 安装

```
go get github.com/Sliverkiss/toolkit
```

要求 Go 1.22+。唯一外部依赖：`gopkg.in/yaml.v3`。

## 致谢
[YuJunZhiXue](https://github.com/YuJunZhiXue)

## 许可

**CC BY-NC-SA 4.0**，详见 [LICENSE](LICENSE)。**禁止商用。**
