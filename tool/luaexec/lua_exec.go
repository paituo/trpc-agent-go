//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package luaexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	lua "github.com/yuin/gopher-lua"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const toolLuaExec = "lua_exec"

// luaExecTool implements tool.CallableTool for Lua script execution.
type luaExecTool struct {
	cfg Config
}

var _ tool.CallableTool = (*luaExecTool)(nil)

func (t *luaExecTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         toolLuaExec,
		Description:  buildDescription(t.cfg),
		InputSchema:  buildInputSchema(),
		OutputSchema: buildOutputSchema(),
	}
}

func (t *luaExecTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var args struct {
		Script  string `json:"script"`
		Timeout int    `json:"timeout,omitempty"`
		Args    any    `json:"args,omitempty"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Script) == "" {
		return nil, fmt.Errorf("script is required")
	}

	timeout := t.cfg.DefaultTimeout
	if args.Timeout > 0 {
		timeout = args.Timeout
	}

	cfg := t.cfg
	cfg.DefaultTimeout = timeout

	// Resolve ToolsProvider if set, merging result into cfg.Tools.
	if cfg.ToolsProvider != nil {
		cfg.Tools = cfg.ToolsProvider(ctx)
	}

	L, cancel := newState(&cfg, ctx)
	defer cancel()

	// Inject ARGS global variable if args are provided.
	if args.Args != nil {
		pushGoValue(L, args.Args)
		luaArgs := L.Get(-1)
		L.Pop(1)
		L.SetGlobal("ARGS", luaArgs)
	}

	// Redirect Lua print() output to a buffer.
	var stdout strings.Builder
	redirectPrint(L, &stdout)

	// Execute the script.
	result, execErr := executeScript(L, args.Script, &cfg)

	if execErr != nil {
		return buildErrorResponse(execErr, &cfg), nil
	}
	return buildSuccessResponse(result, stdout.String(), &cfg), nil
}

// redirectPrint overrides the Lua print() function to write to a buffer.
func redirectPrint(L *lua.LState, buf *strings.Builder) {
	// Override the global print function.
	printFn := L.NewFunction(func(L *lua.LState) int {
		top := L.GetTop()
		for i := 1; i <= top; i++ {
			if i > 1 {
				buf.WriteByte('\t')
			}
			buf.WriteString(L.ToStringMeta(L.Get(i)).String())
		}
		buf.WriteByte('\n')
		return 0
	})
	L.SetGlobal("print", printFn)
}

// executeScript runs a Lua script and returns the result.
func executeScript(L *lua.LState, script string, cfg *Config) (result any, err error) {
	// Recover from panics caused by L.Close() during timeout.
	defer func() {
		if r := recover(); r != nil {
			if L.Context().Err() != nil {
				err = &luaExecError{Type: ErrTypeTimeout, Err: L.Context().Err()}
			} else {
				err = &luaExecError{Type: ErrTypeRuntime, Err: fmt.Errorf("lua panic: %v", r)}
			}
		}
	}()

	fn, err := L.LoadString(script)
	if err != nil {
		return nil, &luaExecError{Type: ErrTypeSyntax, Err: err}
	}

	L.Push(fn)
	if err := L.PCall(0, lua.MultRet, nil); err != nil {
		// Check for context cancellation (timeout).
		if L.Context().Err() != nil {
			return nil, &luaExecError{Type: ErrTypeTimeout, Err: L.Context().Err()}
		}
		return nil, &luaExecError{Type: ErrTypeRuntime, Err: err}
	}

	// Collect return values.
	top := L.GetTop()
	if top == 0 {
		return nil, nil
	}
	// Only take the first return value for simplicity.
	return lValueToGo(L.Get(1)), nil
}

// buildSuccessResponse constructs a success response.
func buildSuccessResponse(result any, stdout string, cfg *Config) map[string]any {
	resp := map[string]any{
		"status": "success",
	}
	if result != nil {
		resp["result"] = result
	}
	if stdout != "" {
		resp["stdout"] = truncateMessage(stdout, cfg.MaxOutputLen)
	}
	return resp
}

// buildErrorResponse constructs an error response.
func buildErrorResponse(err error, cfg *Config) map[string]any {
	luaErr := &luaExecError{}
	if !asLuaExecError(err, luaErr) {
		luaErr = &luaExecError{Type: ErrTypeRuntime, Err: err}
	}

	luaErrors := []LuaError{{
		Type:    luaErr.Type,
		Message: truncateMessage(luaErr.Error(), cfg.MaxErrorLen),
	}}

	// Try to extract line number from GopherLua error.
	if line := extractLineNumber(luaErr.Err); line > 0 {
		luaErrors[0].Line = line
	}

	return map[string]any{
		"status": "error",
		"errors": luaErrors,
	}
}

// luaExecError wraps a Lua execution error with a type.
type luaExecError struct {
	Type string
	Err  error
}

func (e *luaExecError) Error() string { return e.Err.Error() }
func (e *luaExecError) Unwrap() error { return e.Err }

func asLuaExecError(err error, target *luaExecError) bool {
	if e, ok := err.(*luaExecError); ok {
		*target = *e
		return true
	}
	return false
}

// extractLineNumber tries to extract a line number from a GopherLua error message.
func extractLineNumber(err error) int {
	// GopherLua errors look like: "<string>:3: some error"
	msg := err.Error()
	if !strings.HasPrefix(msg, "<string>:") {
		return 0
	}
	rest := strings.TrimPrefix(msg, "<string>:")
	idx := strings.Index(rest, ":")
	if idx <= 0 {
		return 0
	}
	var line int
	fmt.Sscanf(rest[:idx], "%d", &line)
	return line
}

// buildDescription generates the tool description dynamically.
func buildDescription(cfg Config) string {
	desc := "在GopherLua沙箱中执行Lua 5.1脚本。适用于批量数据校验、结构化处理、一次性完成多次工具调用才能完成的任务。\n\n"
	desc += "【Lua版本】Lua 5.1（GopherLua实现）\n\n"
	desc += "【调用其他工具】\n"
	desc += "方式一（推荐）：tool.工具名(args_table) 直接调用。例: tool.fs_read_file({path=\"test.yaml\"})\n"
	desc += "方式二：tool.call(name, args_table) 字符串名调用。例: tool.call('fs_read_file', {path=\"test.yaml\"})\n"
	desc += "tool.list() 返回所有可用工具名。tool.declaration(name) 返回工具声明信息（含参数Schema）。\n"
	desc += "工具返回值自动转为对应Lua类型（table/string/number/bool/nil），可直接按字段访问。例: local r = tool.fs_read_file({...}); print(r.contents)\n"
	desc += "调用失败时返回 nil, {type=\"tool_call\", tool=\"工具名\", phase=\"阶段\", message=\"原因\"}。phase: not_found(工具不存在)/args_type(参数类型错)/args_conversion(参数转换错)/call_failed(执行失败)。\n\n"
	desc += "【禁止调用的工具】lua_exec自身（防递归）、所有名称包含agent的工具（防创建子智能体）、sessions_spawn/list/get/cancel（同subagents别名，防创建子智能体）。调用被禁工具返回 not_found 错误。\n\n"

	// Dynamic module list — 全局已注册，直接使用，禁止require。
	var available []string
	denied := toSet(cfg.DeniedModules)
	if !denied["yaml"] {
		available = append(available, "yaml: decode/encode")
		if cfg.AllowIOLib {
			available[len(available)-1] += "/read_file/write_file"
		}
	}
	if !denied["json"] {
		available = append(available, "json: decode/encode")
	}
	if !denied["re"] {
		available = append(available, "re: match/find/gsub/matches(标准正则语法)")
	}
	if !denied["html"] {
		available = append(available, "html: parse/find/find_all/select/select_all/get_text/get_attr/children/all_children/tag_name/parent(对齐BeautifulSoup,支持CSS选择器和方法式调用)")
	}
	if !denied["md"] {
		available = append(available, "md: parse/extract_tables/parse_table/detect_merge(对齐Python markdown库,支持字符串直接传入)")
	}
	if len(available) > 0 {
		desc += "【桥接模块——全局已注册，直接使用，禁止require】" + strings.Join(available, "; ") + "\n\n"
	}

	var stdLibs []string
	if cfg.AllowIOLib && !denied["io"] {
		stdLibs = append(stdLibs, "io: 标准io库(popen已移除)")
	}
	if cfg.AllowOSLib && !denied["os"] {
		stdLibs = append(stdLibs, "os: time/date等(execute/getenv/exit/remove/rename/tmpname已移除)")
	}
	if len(stdLibs) > 0 {
		desc += "【标准库（需配置开启）】" + strings.Join(stdLibs, "; ") + "\n\n"
	}

	desc += "【Lua 5.1 陷阱】①数组索引从1开始 ②不等号用~= ③注释用-- ④字符串拼接用.. ⑤nil和false是假值，0和空串是真值 ⑥不要使用require加载yaml/json/re，它们是全局变量 ⑦re模块使用标准正则语法（非Lua模式匹配） ⑧re.gsub仅支持字符串替换+捕获组引用($1/$2)，不支持函数回调 ⑨GopherLua不支持中文标识符，table的中文key必须用方括号语法：{[\"中文key\"] = value}，禁止简写语法{中文key = value}\n\n"
	desc += "【参数传递】通过args参数传入，脚本内以ARGS全局table访问。例: lua_exec(script=\"return ARGS.x\", args={x=42}) → result=42\n\n"
	desc += "【常见错误】\n"
	desc += "- attempt to call a nil value: 变量未定义或拼写错误，检查工具名是否正确\n"
	desc += "- bad argument #1 to 'pairs' (table expected, got string): 对非table值用了pairs()，先用type()检查\n"
	desc += "- registry overflow: 循环过大或表元素过多，加循环上限(≤10000)\n"
	desc += "- attempt to index a nil value: 工具返回nil(如搜索无结果)，先判nil再操作\n"
	desc += "- Invalid token near '中文': 中文标识符不支持，table中文key改用方括号语法 {[\"中文key\"] = value}"
	return desc
}

func buildInputSchema() *tool.Schema {
	return &tool.Schema{
		Type:     "object",
		Required: []string{"script"},
		Properties: map[string]*tool.Schema{
			"script": {
				Type:        "string",
				Description: "Lua 5.1脚本内容。脚本最后的return值作为result返回。",
			},
			"timeout": {
				Type:        "integer",
				Description: "超时秒数，默认300秒。部分任务执行耗时较长，可适当增大。",
			},
			"args": {
				Type:        "object",
				Description: "传递给脚本的参数，注入为全局变量ARGS（Lua table）。脚本通过ARGS.key访问。例: args={category=\"基础组件\"} → 脚本中 ARGS.category",
			},
		},
	}
}

func buildOutputSchema() *tool.Schema {
	return &tool.Schema{
		Type:     "object",
		Required: []string{"status"},
		Properties: map[string]*tool.Schema{
			"status": {
				Type:        "string",
				Description: "success 或 error",
			},
			"result": {
				Description: "Lua脚本return的值（自动转换为JSON兼容结构）。仅status=success时存在。",
			},
			"errors": {
				Type:        "array",
				Description: "结构化错误列表。仅status=error时存在。",
				Items: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"line":    {Type: "integer", Description: "出错行号（可选）"},
						"type":    {Type: "string", Description: "错误类型：syntax/runtime/timeout/tool_call/bridge/encoding"},
						"message": {Type: "string", Description: "错误消息（截断至MaxErrorLen）"},
					},
				},
			},
			"stdout": {
				Type:        "string",
				Description: "Lua print()输出（截断至MaxOutputLen）",
			},
			"tool_calls": {
				Type:        "array",
				Description: "执行期间的工具调用记录",
				Items: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"tool":        {Type: "string"},
						"duration_ms": {Type: "integer"},
						"error":       {Type: "string"},
					},
				},
			},
			"steps": {
				Type:        "array",
				Description: "步骤执行记录（前向兼容：供未来声明式编排DSL记录步骤级执行状态）",
				Items: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"id":          {Type: "string"},
						"status":      {Type: "string"},
						"duration_ms": {Type: "integer"},
						"error":       {Type: "string"},
					},
				},
			},
		},
	}
}
