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
	"os"
	"path/filepath"
	"strings"
	"time"

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
		InputSchema:  buildInputSchema(t.cfg),
		OutputSchema: buildOutputSchema(),
	}
}

func (t *luaExecTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var args struct {
		Script     string `json:"script"`
		ScriptPath string `json:"script_path,omitempty"`
		Timeout    int    `json:"timeout,omitempty"`
		Args       any    `json:"args,omitempty"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	// script and script_path are mutually exclusive; exactly one is required.
	hasScript := strings.TrimSpace(args.Script) != ""
	hasScriptPath := strings.TrimSpace(args.ScriptPath) != ""

	if !hasScript && !hasScriptPath {
		return nil, fmt.Errorf("script or script_path is required")
	}
	if hasScript && hasScriptPath {
		return nil, fmt.Errorf("script and script_path are mutually exclusive, provide only one")
	}

	// Resolve script content: either from string or from file path.
	var script string
	if hasScriptPath {
		resolved, err := resolveScriptPath(args.ScriptPath, t.cfg.AllowedScriptDirs)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return nil, fmt.Errorf("failed to read script file %s: %w", args.ScriptPath, err)
		}
		script = string(data)
	} else {
		script = args.Script
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

	L, cancel, lc := newState(&cfg, ctx)
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
	startTime := time.Now()
	result, execErr := executeScript(L, script, &cfg)
	durationMs := time.Since(startTime).Milliseconds()

	if execErr != nil {
		return buildErrorResponse(execErr, &cfg, durationMs, lc, stdout.String()), nil
	}
	return buildSuccessResponse(result, &cfg, durationMs, lc, stdout.String()), nil
}

// resolveScriptPath validates that scriptPath is under one of the allowed
// directories and returns the cleaned absolute path. It prevents path
// traversal attacks (e.g. "../../etc/passwd").
func resolveScriptPath(scriptPath string, allowedDirs []string) (string, error) {
	if len(allowedDirs) == 0 {
		return "", fmt.Errorf("script_path is disabled: no allowed_script_dirs configured")
	}

	absPath, err := filepath.Abs(filepath.Clean(scriptPath))
	if err != nil {
		return "", fmt.Errorf("invalid script_path %q: %w", scriptPath, err)
	}

	// Check that the resolved path is under one of the allowed directories.
	allowed := false
	for _, dir := range allowedDirs {
		absDir, err := filepath.Abs(filepath.Clean(dir))
		if err != nil {
			continue
		}
		// Ensure the directory trailing separator for prefix matching.
		prefix := absDir
		if !strings.HasSuffix(prefix, string(filepath.Separator)) {
			prefix += string(filepath.Separator)
		}
		if strings.HasPrefix(absPath, prefix) || absPath == absDir {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("script_path %q is not under any allowed_script_dirs", scriptPath)
	}

	return absPath, nil
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
// diagnostics holds print output and log entries, separate from the business result.
func buildSuccessResponse(result any, cfg *Config, durationMs int64, lc *LogCollector, stdout string) map[string]any {
	resp := map[string]any{
		"status":      "success",
		"duration_ms": durationMs,
	}
	if result != nil {
		resp["result"] = result
	}
	if diag := buildDiagnostics(cfg, lc, stdout); diag != nil {
		resp["diagnostics"] = diag
	}
	return resp
}

// buildErrorResponse constructs an error response.
func buildErrorResponse(err error, cfg *Config, durationMs int64, lc *LogCollector, stdout string) map[string]any {
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

	resp := map[string]any{
		"status":      "error",
		"duration_ms": durationMs,
		"errors":      luaErrors,
	}
	if diag := buildDiagnostics(cfg, lc, stdout); diag != nil {
		resp["diagnostics"] = diag
	}
	return resp
}

// buildDiagnostics constructs the diagnostics node containing print output and log entries.
// Returns nil if there is nothing to report.
func buildDiagnostics(cfg *Config, lc *LogCollector, stdout string) map[string]any {
	diag := map[string]any{}
	hasContent := false
	if stdout != "" {
		diag["stdout"] = truncateMessage(stdout, cfg.MaxOutputLen)
		hasContent = true
	}
	if lc != nil {
		if entries := lc.collect(); len(entries) > 0 {
			diag["logs"] = entries
			hasContent = true
		}
	}
	if !hasContent {
		return nil
	}
	return diag
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
			available[len(available)-1] += "/read_file/write_file/read_file_auto(自动检测UTF-8/GBK编码)/read_text_file(读取文本文件，支持编码转换，不解析YAML)"
		}
	}
	if !denied["json"] {
		available = append(available, "json: decode/encode")
	}
	if !denied["utf8"] {
		available = append(available, "utf8: len/sub/reverse/upper/lower/char/codepoint/codes/byteoffset/validate/find/match/gsub/matches/encode/decode/detect(统一文本处理：字符级操作+正则匹配+编码转换)")
	}
	if !denied["html"] {
		available = append(available, "html: parse/find/find_all/select/select_all/get_text/get_attr/children/all_children/tag_name/parent")
	}
	if !denied["md"] {
		available = append(available, "md: parse/extract_tables/parse_table/detect_merge")
	}
	if !denied["log"] {
		logDesc := "log: info/warn/error/debug(需配置enable_debug开启)"
		available = append(available, logDesc)
	}
	if cfg.AllowFSLib && !denied["fs"] {
		available = append(available, "fs: read_file/write_file/list_dir/file_exists/is_dir/mkdir/remove/copy/move/stat(受控文件系统操作，路径限制在白名单目录)")
	}
	if len(available) > 0 {
		desc += "【桥接模块——全局已注册，直接使用，禁止require】" + strings.Join(available, "; ") + "\n\n"
	}

	var stdLibs []string
	if cfg.AllowIOLib && !denied["io"] {
		stdLibs = append(stdLibs, "io: lines(逐行读取)/open/read/write等(popen/execute已移除)")
	}
	if cfg.AllowOSLib && !denied["os"] {
		stdLibs = append(stdLibs, "os: time/date/clock等(execute/getenv/exit/remove/rename/tmpname已移除)")
	}
	if len(stdLibs) > 0 {
		desc += "【标准库（需配置开启）】" + strings.Join(stdLibs, "; ") + "\n\n"
	}

	desc += "【注意事项】①nil/false是假值，0和空串是真值 ②禁止require，桥接模块已全局注册 ③GopherLua不支持中文标识符，table中文key用{[\"key\"]=val} ④处理任何文本必须用utf8模块，string库按字节操作不适用于中文\n\n"

	desc += "【html模块】elem:get_text()/get_attr(name)/find(tag)/find_all(tag)/select(css)/select_all(css)。html.parse(str)返回根节点。建议pcall包裹防崩溃。\n\n"

	desc += "【md模块】md.parse/parse_table/extract_tables/detect_merge\n\n"

	desc += "【utf8模块——字符级操作+正则匹配+编码转换】\n"
	desc += "字符操作：utf8.len/sub/reverse/upper/lower/char/codepoint/codes/byteoffset/validate\n"
	desc += "正则匹配：utf8.find/match/gsub/matches\n"
	desc += "编码转换：utf8.encode(s,from,to)/decode(s,from)/detect(s)→\"utf-8\"/\"gbk\"/\"unknown\"\n\n"

	desc += "【yaml模块】yaml.decode/encode/read_file/write_file/read_file_auto/read_text_file(path[,encoding])\n\n"

	desc += "【log模块】log.info/warn/error/debug(...)。日志和print输出在diagnostics节点，与result分离。\n\n"

	desc += "【参数传递】通过args参数传入，脚本内以ARGS全局table访问。例: lua_exec(script=\"return ARGS.x\", args={x=42}) → result=42\n\n"

	if len(cfg.AllowedScriptDirs) > 0 {
		desc += "【脚本文件执行】支持script_path参数指定白名单目录下的.lua文件路径，避免回显完整脚本内容。script和script_path二选一，不可同时提供。例: lua_exec(script_path=\"/scripts/validate.lua\", args={category=\"基础组件\"})\n\n"
	}

	desc += "【错误处理】\n"
	desc += "① 桥接函数(fs.*/yaml.*)失败时返回 nil, {type=\"bridge\", message=\"原因\"}，用 err.message 获取错误描述\n"
	desc += "② io.lines(path) 文件不存在时抛出错误，用pcall包裹\n"
	desc += "③ 脚本末尾必须return结果\n"
	desc += "④ 工具调用失败时返回 nil, {type=\"tool_call\", ...}，检查第一个返回值\n\n"

	desc += "【常见错误】\n"
	desc += "- attempt to call a nil value: 变量未定义或拼写错误\n"
	desc += "- attempt to call a non-function object: 调用了非函数值，html模块用elem:get_text()而非elem:text()\n"
	desc += "- bad argument #1 to 'pairs' (table expected, got string): 对非table值用了pairs()\n"
	desc += "- registry overflow: 循环过大或表元素过多，加循环上限(≤10000)\n"
	desc += "- attempt to index a nil value: 工具返回nil，先判nil再操作\n"
	desc += "- Invalid token near '中文': 中文标识符不支持，table中文key用{[\"中文key\"]=val}\n"
	desc += "- bad argument #1 to 'load': GopherLua的load()行为不同，用loadstring()替代\n"
	desc += "- os.execute/io.popen: 沙箱已移除，用yaml.write_file或tool调用替代"
	return desc
}

func buildInputSchema(cfg Config) *tool.Schema {
	schema := &tool.Schema{
		Type:     "object",
		Required: []string{}, // will be set below based on allowed_script_dirs
		Properties: map[string]*tool.Schema{
			"script": {
				Type:        "string",
				Description: "Lua 5.1脚本内容。脚本最后的return值作为result返回。与script_path互斥，二选一。",
			},
			"script_path": {
				Type:        "string",
				Description: "Lua脚本文件路径。文件内容作为脚本执行，与script互斥。路径必须在allowed_script_dirs配置的白名单目录下。",
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
	// When allowed_script_dirs is configured, script_path is available;
	// otherwise fall back to requiring script only.
	if len(cfg.AllowedScriptDirs) > 0 {
		schema.Description = "script和script_path二选一。script_path指定白名单目录下的脚本文件路径，避免回显完整脚本内容。"
	} else {
		schema.Required = []string{"script"}
	}
	return schema
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
			"duration_ms": {
				Type:        "integer",
				Description: "脚本执行耗时（毫秒）",
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
			"diagnostics": {
				Type:        "object",
				Description: "诊断信息节点，包含print输出和log日志，与业务返回result分离",
				Properties: map[string]*tool.Schema{
					"stdout": {
						Type:        "string",
						Description: "Lua print()输出（截断至MaxOutputLen）",
					},
					"logs": {
						Type:        "array",
						Description: "log模块输出的日志条目",
						Items: &tool.Schema{
							Type: "object",
							Properties: map[string]*tool.Schema{
								"level":     {Type: "string", Description: "日志级别：info/warn/error/debug"},
								"timestamp": {Type: "string", Description: "ISO 8601时间戳"},
								"message":   {Type: "string", Description: "日志消息"},
							},
						},
					},
				},
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
