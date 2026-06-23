# 开放封闭原则(OCP)违规分析与重构设计方案

## 一、开放封闭原则概述

**开放封闭原则 (Open-Closed Principle, OCP)**: 软件实体（类、模块、函数等）应该对扩展开放，对修改封闭。

- **对扩展开放**: 可以通过添加新代码来扩展行为
- **对修改封闭**: 扩展行为时不需要修改已有代码

**核心判断标准**: 当需要添加新功能/新类型时，是否需要修改已有的源代码？如果需要，则违反了OCP。

---

## 二、OCP违规分析

### 违规1: model/provider/options.go — Callbacks 结构体 [严重]

**位置**: [options.go](file:///workspace/model/provider/options.go#L54-L95)

**问题描述**:
`Callbacks` 结构体为每个 Provider 硬编码了4个回调字段（Request/Response/Chunk/StreamComplete），当前有5个Provider共20个字段。每新增一个Provider，必须修改此结构体添加4个新字段。

```go
type Callbacks struct {
    OpenAIChatRequest       openai.ChatRequestCallbackFunc
    OpenAIChatResponse      openai.ChatResponseCallbackFunc
    OpenAIChatChunk         openai.ChatChunkCallbackFunc
    OpenAIStreamComplete    openai.ChatStreamCompleteCallbackFunc
    AnthropicChatRequest    anthropic.ChatRequestCallbackFunc
    AnthropicChatResponse   anthropic.ChatResponseCallbackFunc
    // ... 每个Provider 4个字段，共20个
    HunyuanStreamComplete   hunyuan.ChatStreamCompleteCallbackFunc
}
```

**OCP违规证据**:
- 添加 Bedrock Provider → 需要在此结构体添加4个字段
- 添加 DeepSeek Provider → 需要在此结构体添加4个字段
- `WithCallbacks` 函数（L146-L210）有20个if判断，每添加1个Provider需添加4个if

**影响范围**: 所有使用 `Callbacks` 的代码，包括序列化、测试、配置加载

---

### 违规2: model/provider/options.go — Options 结构体 [严重]

**位置**: [options.go](file:///workspace/model/provider/options.go#L28-L51)

**问题描述**:
`Options` 结构体为每个Provider硬编码了选项字段（`OpenAIOption`, `AnthropicOption`, `GeminiOption`, `OllamaOption`, `HunyuanOption`）。每新增一个Provider，必须修改此结构体添加新字段。

```go
type Options struct {
    // ... 通用字段 ...
    OpenAIOption    []openai.Option
    AnthropicOption []anthropic.Option
    GeminiOption    []gemini.Option
    OllamaOption    []ollama.Option
    HunyuanOption   []hunyuan.Option
}
```

**OCP违规证据**:
- 添加新Provider → 需要在此结构体添加 `XxxOption []xxx.Option` 字段
- 需要添加对应的 `WithXxxOption` 函数
- options.go 直接 import 了所有5个Provider包，形成强耦合

---

### 违规3: model/provider/provider.go — Provider 工厂函数 [严重]

**位置**: [provider.go](file:///workspace/model/provider/provider.go#L26-L331)

**问题描述**:
每个Provider有一个硬编码的工厂函数（`openaiProvider`, `anthropicProvider`, `geminiProvider`, `ollamaProvider`, `hunyuanProvider`），这些函数包含大量重复的选项映射逻辑。`init()` 函数硬编码了5个Provider的注册。

```go
func init() {
    Register("openai", openaiProvider)
    Register("anthropic", anthropicProvider)
    Register("gemini", geminiProvider)
    Register("ollama", ollamaProvider)
    Register("hunyuan", hunyuanProvider)
}
```

**OCP违规证据**:
- 每个Provider工厂函数有30-60行重复的选项映射代码
- 添加新Provider需要在同一文件中添加新的工厂函数
- `init()` 函数需要添加新的 Register 调用
- 所有Provider包在同一个文件中import，无法独立部署

**代码重复度**: 5个工厂函数中，选项映射逻辑（APIKey、BaseURL、ChannelBufferSize、TokenTailoring等）的重复率超过70%

---

### 违规4: tool/luaexec/sandbox.go — newState() 硬编码模块注册 [中等]

**位置**: [sandbox.go](file:///workspace/tool/luaexec/sandbox.go#L59-L124)

**问题描述**:
`newState()` 函数通过一系列硬编码的 if 语句注册每个桥接模块。添加新模块需要修改此函数。

```go
if !denied["tool"] && len(cfg.Tools) > 0 {
    registerToolBridge(L, cfg.Tools, cfg.DeniedTools)
    L.PreloadModule("tool", preloadGlobal(L, "tool"))
}
if !denied["yaml"] {
    registerYAMLBridge(L, cfg.AllowIOLib)
    L.PreloadModule("yaml", preloadGlobal(L, "yaml"))
}
if !denied["json"] { ... }
if !denied["html"] { ... }
if !denied["md"] { ... }
if !denied["htmltable"] { ... }
if !denied["summarize"] { ... }
if !denied["utf8"] { ... }
if cfg.AllowFSLib && !denied["fs"] { ... }
if !denied["log"] { ... }
```

**OCP违规证据**:
- 10个硬编码的模块注册块
- 添加新桥接模块（如 `http`, `csv`, `xml`）需要修改此函数
- 每个模块的注册逻辑略有不同（有的需要额外参数），缺乏统一的注册接口

---

### 违规5: tool/luaexec/config.go — AllowXxxLib 布尔字段模式 [中等]

**位置**: [config.go](file:///workspace/tool/luaexec/config.go#L63-L78)

**问题描述**:
每个Lua标准库有一个独立的布尔开关字段（`AllowIOLib`, `AllowOSLib`, `AllowFSLib`）。添加新库的控制开关需要修改Config结构体。

```go
type Config struct {
    AllowIOLib bool   // 控制io库
    AllowOSLib bool   // 控制os库
    AllowFSLib bool   // 控制fs桥接模块
    // 添加新库需要在此添加新字段
}
```

**OCP违规证据**:
- 添加新标准库（如 `debug`, `coroutine`）需要添加新的 `AllowXxxLib` 字段
- 需要添加对应的 `WithAllowXxxLib` 选项函数
- 需要修改 `defaultConfig()` 函数
- 需要修改 `newState()` 中的加载逻辑

**与DeniedModules的关系**: `DeniedModules` 字段已经提供了对桥接模块的关闭控制，但标准库（io/os）和桥接模块（fs）的控制方式不一致——标准库用AllowXxxLib，桥接模块用DeniedModules。

---

### 违规6: tool/agent/agent_tool.go — dynamicOptions 暴露选项 [轻微]

**位置**: [agent_tool.go](file:///workspace/tool/agent/agent_tool.go#L80-L94)

**问题描述**:
`dynamicOptions` 中的 `exposeToolSelection`、`exposeSkillSelection`、`exposeInstruction` 是独立的布尔字段。添加新的可暴露维度需要修改结构体。

```go
type dynamicOptions struct {
    exposeToolSelection    bool
    exposeSkillSelection   bool
    exposeInstruction      bool
    // 添加新的暴露维度需要修改此结构体
}
```

**OCP违规证据**:
- 添加新的可暴露维度（如 `exposeModel`, `exposeExecutor`）需要修改结构体
- 需要添加对应的 `WithExposeXxx` 选项函数
- 需要修改 `buildDynamicInputSchema` 和 `buildDynamicDescription` 中的逻辑

---

### 违规7: tool/skill/run.go — 多处硬编码分支逻辑 [中等]

**位置**: [run.go](file:///workspace/tool/skill/run.go)

**问题描述**:
`run.go` 中存在多处基于类型或字符串的硬编码分支逻辑：

1. **L1164-L1233**: 命令允许/拒绝策略的硬编码检查逻辑
2. **L1477-L1493**: `pathListSeparatorForEngine` 通过类型断言检查引擎类型
3. **L1808-L1824**: `prepareOutputs` 中 legacy vs new 输出模式的 if-else 分支
4. **L2065-L2076**: `shouldInlineFileContent` 中硬编码的文件类型判断
5. **L2517-L2533**: `mergeManifestArtifactRefs` 中硬编码的 manifest 类型处理

**OCP违规证据**:
- 添加新的引擎类型需要修改 `pathListSeparatorForEngine`
- 添加新的输出收集策略需要修改 `prepareOutputs`
- 添加新的文件内联判断规则需要修改 `shouldInlineFileContent`

---

## 三、重构设计方案

### 方案1: Provider 回调与选项的泛化注册 [针对违规1+2+3]

**设计思路**: 将 Provider 特定的回调和选项从中央结构体中解耦，改为每个 Provider 自行注册。

#### 1.1 引入 Provider 自注册接口

```go
// model/provider/provider.go

// ProviderBuilder 定义了 Provider 的完整构建接口
// 每个 Provider 实现此接口并通过 init() 自注册
type ProviderBuilder interface {
    // Name 返回 Provider 的唯一标识符
    Name() string

    // Build 根据 Options 构建一个 model.Model 实例
    Build(opts *Options) (model.Model, error)

    // ParseCallbacks 从通用回调映射中解析 Provider 特定的回调
    // key 格式: "chat_request", "chat_response", "chat_chunk", "stream_complete"
    ParseCallbacks(callbacks map[string]any) any

    // ParseRawOptions 从通用选项映射中解析 Provider 特定的选项
    ParseRawOptions(rawOpts []any) []any
}
```

#### 1.2 Options 结构体泛化

```go
// model/provider/options.go

type Options struct {
    // 通用字段（不变）
    ProviderName         string
    ModelName            string
    Variant              string
    APIKey               string
    BaseURL              string
    HTTPClientName       string
    HTTPClientTransport  http.RoundTripper
    ChannelBufferSize    *int
    Headers              map[string]string
    ExtraFields          map[string]any
    EnableTokenTailoring *bool
    MaxInputTokens       *int
    ContextWindow        *int
    TokenCounter         model.TokenCounter
    TailoringStrategy    model.TailoringStrategy
    TokenTailoringConfig *model.TokenTailoringConfig

    // 替换 Provider 特定字段
    // 旧: OpenAIOption []openai.Option, AnthropicOption []anthropic.Option, ...
    // 新: ProviderOptions 存储 Provider 特定的原始选项
    ProviderOptions map[string]any  // key: provider name, value: provider-specific opts

    // 替换 Callbacks 结构体
    // 旧: Callbacks *Callbacks (20个硬编码字段)
    // 新: ProviderCallbacks 存储 Provider 特定的回调
    ProviderCallbacks map[string]map[string]any  // key1: provider, key2: callback type
}
```

#### 1.3 Provider 自注册实现

```go
// model/provider/openai_provider.go (新文件)
package provider

import (
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func init() {
    Register("openai", &openaiBuilder{})
}

type openaiBuilder struct{}

func (b *openaiBuilder) Name() string { return "openai" }

func (b *openaiBuilder) Build(opts *Options) (model.Model, error) {
    var res []openai.Option
    if opts.APIKey != "" {
        res = append(res, openai.WithAPIKey(opts.APIKey))
    }
    if opts.BaseURL != "" {
        res = append(res, openai.WithBaseURL(opts.BaseURL))
    }
    // ... 其他通用选项映射 ...

    // 追加 Provider 特定选项
    if raw, ok := opts.ProviderOptions["openai"]; ok {
        if specific, ok := raw.([]openai.Option); ok {
            res = append(res, specific...)
        }
    }
    return openai.New(opts.ModelName, res...), nil
}

func (b *openaiBuilder) ParseCallbacks(callbacks map[string]any) any {
    // 从通用回调映射解析 OpenAI 特定回调
    result := &openaiCallbacks{}
    if cb, ok := callbacks["chat_request"]; ok {
        result.chatRequest = cb.(openai.ChatRequestCallbackFunc)
    }
    // ...
    return result
}

func (b *openaiBuilder) ParseRawOptions(rawOpts []any) []any {
    var opts []openai.Option
    for _, raw := range rawOpts {
        if opt, ok := raw.(openai.Option); ok {
            opts = append(opts, opt)
        }
    }
    return opts
}
```

#### 1.4 通用选项映射提取

```go
// model/provider/common_options.go (新文件)
package provider

// CommonOptionMapper 定义了从 Options 到 Provider 特定选项的通用映射逻辑
// 消除5个工厂函数中70%的重复代码
type CommonOptionMapper interface {
    MapAPIKey(key string) any
    MapBaseURL(url string) any
    MapChannelBufferSize(size int) any
    MapTokenTailoring(enabled bool) any
    MapContextWindow(tokens int) any
    // ...
}
```

#### 1.5 迁移策略

```
阶段1: 添加 ProviderBuilder 接口和自注册机制（不删除旧代码）
阶段2: 逐个将 Provider 工厂函数迁移为 ProviderBuilder 实现
阶段3: 将 Callbacks 和 Options 中的 Provider 特定字段标记为 deprecated
阶段4: 移除旧代码，完成迁移
```

---

### 方案2: Lua 桥接模块的插件化注册 [针对违规4+5]

**设计思路**: 将硬编码的模块注册逻辑替换为可插拔的模块注册表。

#### 2.1 定义 BridgeModule 接口

```go
// tool/luaexec/bridge.go (新文件)
package luaexec

import lua "github.com/yuin/gopher-lua"

// BridgeModule 定义了 Lua 桥接模块的注册接口
type BridgeModule interface {
    // Name 返回模块名称（用于 DeniedModules 检查和 require() 加载）
    Name() string

    // Register 在 Lua VM 中注册此模块
    // 返回此模块的 LogCollector（如果有的话），否则返回 nil
    Register(L *lua.LState, cfg *Config) *LogCollector
}

// BridgeModuleFunc 是 BridgeModule 的函数适配器
type BridgeModuleFunc struct {
    name     string
    register func(L *lua.LState, cfg *Config) *LogCollector
}

func (f *BridgeModuleFunc) Name() string { return f.name }
func (f *BridgeModuleFunc) Register(L *lua.LState, cfg *Config) *LogCollector {
    return f.register(L, cfg)
}
```

#### 2.2 模块注册表

```go
// tool/luaexec/registry.go (新文件)
package luaexec

import (
    "sync"
    lua "github.com/yuin/gopher-lua"
)

var (
    moduleMu  sync.RWMutex
    modules   = make(map[string]BridgeModule)
    modOrder  []string  // 保持注册顺序
)

// RegisterModule 注册一个 Lua 桥接模块
// 在 init() 中调用以注册内置模块
func RegisterModule(m BridgeModule) {
    moduleMu.Lock()
    defer moduleMu.Unlock()
    if _, exists := modules[m.Name()]; !exists {
        modOrder = append(modOrder, m.Name())
    }
    modules[m.Name()] = m
}
```

#### 2.3 内置模块自注册

```go
// tool/luaexec/bridge_yaml.go
package luaexec

func init() {
    RegisterModule(&BridgeModuleFunc{
        name: "yaml",
        register: func(L *lua.LState, cfg *Config) *LogCollector {
            registerYAMLBridge(L, cfg.AllowIOLib)
            L.PreloadModule("yaml", preloadGlobal(L, "yaml"))
            return nil
        },
    })
}
```

```go
// tool/luaexec/bridge_fs.go
package luaexec

func init() {
    RegisterModule(&BridgeModuleFunc{
        name: "fs",
        register: func(L *lua.LState, cfg *Config) *LogCollector {
            registerFSBridge(L, cfg)
            L.PreloadModule("fs", preloadGlobal(L, "fs"))
            return nil
        },
    })
}
```

#### 2.4 重构 newState()

```go
// tool/luaexec/sandbox.go — 重构后
func newState(cfg *Config, callerCtx context.Context) (*lua.LState, context.CancelFunc, *LogCollector) {
    // ... VM 创建和基础库加载不变 ...

    var logCollector *LogCollector
    denied := toSet(cfg.DeniedModules)

    // 通过注册表动态加载模块（替代硬编码的 if 块）
    moduleMu.RLock()
    for _, name := range modOrder {
        m := modules[name]
        if denied[name] {
            continue
        }
        // 特殊处理：io/os 标准库受 AllowIOLib/AllowOSLib 控制
        if name == "io" && !cfg.AllowIOLib {
            continue
        }
        if name == "os" && !cfg.AllowOSLib {
            continue
        }
        if name == "fs" && !cfg.AllowFSLib {
            continue
        }
        if lc := m.Register(L, cfg); lc != nil {
            logCollector = lc
        }
    }
    moduleMu.RUnlock()

    return L, cancel, logCollector
}
```

#### 2.5 统一 AllowXxxLib 和 DeniedModules

**长期方案**: 将 `AllowIOLib`、`AllowOSLib`、`AllowFSLib` 统一为 `AllowedModules` 列表，与 `DeniedModules` 形成对称控制：

```go
type Config struct {
    // DeniedModules 列出被禁用的模块（黑名单，默认空）
    DeniedModules []string

    // AllowedModules 列出被允许的标准库模块（白名单，默认 ["io","os","fs"]）
    // 当非空时，只有此列表中的标准库模块可用
    // 与 DeniedModules 互斥：DeniedModules 优先
    AllowedModules []string
}
```

**迁移策略**:
```
阶段1: 添加 AllowedModules 字段，保留 AllowIOLib/AllowOSLib/AllowFSLib 作为语法糖
阶段2: AllowIOLib/AllowOSLib/AllowFSLib 标记为 deprecated
阶段3: 移除旧字段
```

---

### 方案3: Dynamic AgentTool 暴露选项的泛化 [针对违规6]

**设计思路**: 将独立的布尔暴露选项替换为可扩展的暴露维度注册表。

#### 3.1 定义暴露维度

```go
// tool/agent/expose.go (新文件)
package agent

// ExposeDimension 定义了动态 AgentTool 可暴露给模型的维度
type ExposeDimension string

const (
    ExposeTools       ExposeDimension = "tools"
    ExposeSkills      ExposeDimension = "skills"
    ExposeInstruction ExposeDimension = "instruction"
)

// ExposeConfig 控制哪些维度暴露给模型
type ExposeConfig struct {
    dimensions map[ExposeDimension]bool
    defaults   map[ExposeDimension]bool
}

func NewExposeConfig() *ExposeConfig {
    return &ExposeConfig{
        dimensions: make(map[ExposeDimension]bool),
        defaults: map[ExposeDimension]bool{
            ExposeTools:       true,
            ExposeSkills:      false,
            ExposeInstruction: true,
        },
    }
}

func (c *ExposeConfig) IsExposed(dim ExposeDimension) bool {
    if v, ok := c.dimensions[dim]; ok {
        return v
    }
    return c.defaults[dim]
}

func (c *ExposeConfig) Set(dim ExposeDimension, expose bool) {
    c.dimensions[dim] = expose
}
```

#### 3.2 重构 dynamicOptions

```go
// 旧:
type dynamicOptions struct {
    exposeToolSelection    bool
    exposeSkillSelection   bool
    exposeInstruction      bool
    // ...
}

// 新:
type dynamicOptions struct {
    expose *ExposeConfig
    // ...
}
```

#### 3.3 选项函数泛化

```go
// 旧:
func WithExposeToolSelection(expose bool) Option
func WithExposeSkillSelection(expose bool) Option
func WithExposeInstruction(expose bool) Option

// 新:
func WithExposeDimension(dim ExposeDimension, expose bool) Option {
    return func(opts *agentToolOptions) {
        opts.ensureDynamicOptions().expose.Set(dim, expose)
    }
}

// 保留便捷函数（向后兼容）
func WithExposeToolSelection(expose bool) Option {
    return WithExposeDimension(ExposeTools, expose)
}
```

---

### 方案4: run.go 策略模式重构 [针对违规7]

**设计思路**: 将硬编码的分支逻辑替换为策略接口。

#### 4.1 引擎路径分隔符策略

```go
// 旧: 通过类型断言
func pathListSeparatorForEngine(eng codeexecutor.Engine) string {
    if eng == nil || eng.Runner() == nil {
        return posixPathListSep
    }
    provider, ok := eng.Runner().(pathListSeparatorProvider)
    if !ok {
        return posixPathListSep
    }
    // ...
}

// 新: Engine 接口直接支持
// 在 codeexecutor.Engine 接口中添加可选方法
type PathListSeparatorAware interface {
    PathListSeparator() string
}
```

#### 4.2 输出收集策略

```go
// tool/skill/output.go (新文件)
package skill

// OutputCollector 定义了输出收集策略接口
type OutputCollector interface {
    Collect(ctx context.Context, eng codeexecutor.Engine, ws codeexecutor.Workspace, in runInput) (
        files []codeexecutor.File, manifest *codeexecutor.OutputManifest, warnings []string, err error)
}

// SpecOutputCollector 使用 OutputSpec 收集输出
type SpecOutputCollector struct{}

func (c *SpecOutputCollector) Collect(ctx context.Context, eng codeexecutor.Engine, ws codeexecutor.Workspace, in runInput) (
    []codeexecutor.File, *codeexecutor.OutputManifest, []string, error) {
    // 原 prepareOutputs 中 OutputSpec 分支的逻辑
}

// LegacyOutputCollector 使用 output_files 收集输出
type LegacyOutputCollector struct {
    tool *RunTool
}

func (c *LegacyOutputCollector) Collect(ctx context.Context, eng codeexecutor.Engine, ws codeexecutor.Workspace, in runInput) (
    []codeexecutor.File, *codeexecutor.OutputManifest, []string, error) {
    // 原 prepareOutputs 中 legacy 分支的逻辑
}
```

---

## 四、重构优先级与实施路线图

### 优先级矩阵

| 违规编号 | 严重程度 | 重构难度 | 业务影响 | 优先级 |
|----------|----------|----------|----------|--------|
| 违规1+2+3 | 严重 | 高 | Provider 扩展受阻 | P0 |
| 违规4+5 | 中等 | 中 | 模块扩展受阻 | P1 |
| 违规7 | 中等 | 中 | 引擎扩展受阻 | P2 |
| 违规6 | 轻微 | 低 | 暴露维度扩展受阻 | P3 |

### 实施路线图

```
第1阶段 (P0): Provider 回调与选项泛化
├── 1.1 定义 ProviderBuilder 接口
├── 1.2 实现 Provider 自注册机制
├── 1.3 泛化 Options 和 Callbacks
├── 1.4 提取通用选项映射逻辑
└── 1.5 逐个迁移 Provider 实现

第2阶段 (P1): Lua 桥接模块插件化
├── 2.1 定义 BridgeModule 接口
├── 2.2 实现模块注册表
├── 2.3 逐个迁移内置模块为自注册
├── 2.4 重构 newState() 使用注册表
└── 2.5 统一 AllowXxxLib 和 DeniedModules

第3阶段 (P2): run.go 策略模式重构
├── 3.1 提取 OutputCollector 策略接口
├── 3.2 重构引擎类型判断为接口方法
└── 3.3 提取文件内联判断策略

第4阶段 (P3): Dynamic AgentTool 暴露选项泛化
├── 4.1 定义 ExposeDimension 和 ExposeConfig
├── 4.2 重构 dynamicOptions 使用 ExposeConfig
└── 4.3 提供向后兼容的便捷函数
```

---

## 五、重构风险评估

### 高风险项

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| Provider 回调接口变更导致下游编译失败 | 高 | 分阶段迁移，保留旧接口作为 deprecated |
| Lua 模块注册顺序变化影响行为 | 中 | 使用 modOrder 保持注册顺序 |
| Options 泛化后类型安全性降低 | 中 | 提供 Provider 特定的类型断言辅助函数 |

### 低风险项

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| ExposeDimension 新增维度无默认值 | 低 | ExposeConfig 提供默认值映射 |
| OutputCollector 策略选择错误 | 低 | 保留原逻辑作为默认策略 |

---

## 六、重构验证标准

每个阶段完成后需满足以下验证条件：

1. **编译通过**: `go build ./...` 无错误
2. **测试通过**: `go test ./...` 所有测试通过
3. **向后兼容**: 旧 API 在 deprecated 期间仍可正常使用
4. **扩展性验证**: 能够通过添加新文件（不修改已有文件）来添加新的 Provider/模块/策略
5. **代码覆盖率**: 新增代码的测试覆盖率不低于原有水平

---

## 七、总结

PAITUO 的提交主要聚焦于命名一致性和代码格式的表面优化，虽然这些修改有价值，但未触及代码库中更深层的架构问题。本分析识别出7处OCP违规，其中3处为严重级别，集中在 `model/provider` 包的 Provider 回调和选项管理机制中。

核心问题是 **Provider 特定的逻辑被硬编码在通用结构体和工厂函数中**，导致每添加一个新 Provider 就需要修改多处已有代码。重构的核心思路是 **将 Provider 特定的逻辑下沉到各 Provider 自身**，通过接口和自注册机制实现扩展开放、修改封闭。
