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
	if !denied["re"] {
		available = append(available, "re: find/matches/gsub(标准Go正则语法，非Lua模式匹配)")
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

	desc += "【Lua 5.1 陷阱】①数组索引从1开始 ②不等号用~= ③注释用-- ④字符串拼接用.. ⑤nil和false是假值，0和空串是真值 ⑥不要使用require加载yaml/json/re/html/md，它们是全局变量 ⑦re模块使用Go标准正则语法（非Lua模式匹配），如\\d而非%d、\\s而非%s ⑧re.gsub仅支持字符串替换+捕获组引用(${1}/${2})，不支持函数回调 ⑨GopherLua不支持中文标识符，table的中文key必须用方括号语法：{[\"中文key\"] = value}，禁止简写语法{中文key = value} ⑩GopherLua不支持require函数，所有桥接模块（yaml/json/re/html/md/log）已全局注册，直接使用模块名即可，无需也无法require\n\n"

	desc += "【html模块关键API说明——与BeautifulSoup/标准Lua差异】\n"
	desc += "① 获取元素文本用 elem:get_text()，不是 elem:text() 或 elem.text\n"
	desc += "② 获取元素属性用 elem:get_attr(name)，不是 elem.attrs[name] 或 elem:get(name)\n"
	desc += "③ 查找子元素用 html.find(parent, tag) 或 html.find_all(parent, tag)，不是 parent:find(tag)\n"
	desc += "④ 方法式链式调用支持：elem:find(tag) 等价于 html.find(elem, tag)，elem:find_all(tag) 等价于 html.find_all(elem, tag)\n"
	desc += "⑤ CSS选择器：html.select(elem, \"#id\") / html.select_all(elem, \".class\")，支持 #id/.class/tag/[attr]\n"
	desc += "⑥ html.parse(str) 返回文档根节点，html.find(doc, \"table\") 查找第一个table元素\n"
	desc += "⑦ 所有html函数调用建议用pcall包裹防崩溃：local ok, result = pcall(html.parse, str)\n\n"

	desc += "【md模块关键API说明】\n"
	desc += "① md.parse(str) 解析Markdown返回AST userdata\n"
	desc += "② md.extract_tables(str_or_ast) 提取所有表格，支持直接传入字符串\n"
	desc += "③ md.parse_table(str_or_ast, index) 解析第N个表格（1-based，默认1）\n"
	desc += "④ md.detect_merge(str_or_table) 检测合并单元格，支持直接传入字符串\n\n"

	desc += "【re模块关键API说明——与标准Lua模式匹配差异】\n"
	desc += "① re.find(str, pattern) 返回 full_match, cap1, cap2... 或 nil（不是boolean）\n"
	desc += "② re.matches(str, pattern) 返回字符串数组（无捕获组）或捕获组数组（有捕获组），不是boolean\n"
	desc += "③ re.gsub(str, pattern, replacement) 捕获组引用用 ${1}/${2}，不是 $1/$2\n"
	desc += "④ 正则语法是Go regexp：\\d \\s \\w . * + ? {n,m} () [] |，不是Lua的 %d %s %w\n"
	desc += "⑤ 判断是否匹配：if re.find(str, pattern) then ... end（re.find返回nil表示不匹配）\n\n"

	desc += "【yaml模块关键API说明——全局桥接模块，直接使用，禁止require】\n"
	desc += "① yaml.decode(str) 解析YAML字符串为Lua table\n"
	desc += "② yaml.encode(table) 将Lua table序列化为YAML字符串\n"
	desc += "③ yaml.read_file(path) 读取YAML文件为Lua table（需AllowIOLib）\n"
	desc += "④ yaml.write_file(path, table) 将Lua table写入YAML文件（需AllowIOLib，自动创建父目录）\n"
	desc += "⑤ yaml.read_file_auto(path) 自动检测编码(UTF-8/GBK)读取YAML文件（需AllowIOLib）\n"
	desc += "⑥ yaml.read_text_file(path [, encoding]) 读取文本文件为字符串，支持编码转换但不解析YAML（需AllowIOLib）。encoding: utf-8/utf-8-bom/gbk/auto(默认)。适用于读取Markdown等非YAML文本文件。\n\n"

	desc += "【log模块关键API说明——脚本执行日志】\n"
	desc += "① log.info(...) 输出INFO级别日志，可接受多个参数\n"
	desc += "② log.warn(...) 输出WARN级别日志\n"
	desc += "③ log.error(...) 输出ERROR级别日志\n"
	desc += "④ log.debug(...) 输出DEBUG级别日志（需配置enable_debug=true，默认关闭）\n"
	desc += "⑤ 日志输出与print()输出均存放在响应的diagnostics节点中（logs数组+stdout字符串），与脚本return的业务结果result完全分离，不会混杂\n"
	desc += "⑥ 例: log.info(\"开始处理文件:\", filename); log.warn(\"配置缺失，使用默认值\")\n\n"

	desc += "【GopherLua闭包与变量作用域注意事项】\n"
	desc += "① 局部变量必须在使用前声明：local x = {} 必须在引用x的函数定义之前\n"
	desc += "② 函数闭包捕获变量引用（非值）：函数内修改外部local变量会影响外部\n"
	desc += "③ 避免函数参数名与外部变量同名（变量遮蔽/shadowing）：如 function fn(buffer) 中buffer会遮蔽外部的buffer变量\n"
	desc += "④ loadstring()加载的脚本在新闭包中执行，无法访问当前脚本的local变量\n"
	desc += "⑤ 全局变量（无local前缀）可跨闭包访问，但应谨慎使用\n\n"

	desc += "【参数传递】通过args参数传入，脚本内以ARGS全局table访问。例: lua_exec(script=\"return ARGS.x\", args={x=42}) → result=42\n\n"

	if len(cfg.AllowedScriptDirs) > 0 {
		desc += "【脚本文件执行】支持script_path参数指定白名单目录下的.lua文件路径，避免回显完整脚本内容。script和script_path二选一，不可同时提供。例: lua_exec(script_path=\"/scripts/validate.lua\", args={category=\"基础组件\"})\n\n"
	}

	desc += "【错误处理要求——必须遵守】\n"
	desc += "① yaml.write_file/yaml.read_file 等桥接函数失败时返回 nil, {type=\"bridge\", message=\"原因\"}，必须检查返回值：local ok, err = yaml.write_file(path, data); if not ok then error(\"写入失败: \" .. (err and err.message or \"unknown\")) end\n"
	desc += "② io.lines(path) 文件不存在时会抛出运行时错误，必须用pcall包裹：local ok, iter = pcall(io.lines, path); if not ok then error(\"文件不可读: \" .. path) end\n"
	desc += "③ 脚本末尾必须return执行结果（至少包含status字段），禁止无return结束。例: return { status=\"success\", stats={...} }\n"
	desc += "④ pcall捕获的错误不要静默忽略，至少用print输出WARNING：if not ok then print(\"WARNING: \" .. tostring(err)) end\n"
	desc += "⑤ 工具调用失败时返回 nil, {type=\"tool_call\", ...}，必须检查第一个返回值是否为nil\n\n"

	desc += "【常见错误】\n"
	desc += "- attempt to call a nil value: 变量未定义或拼写错误，检查工具名/函数名是否正确\n"
	desc += "- attempt to call a non-function object: 调用了非函数值，常见于html模块：elem:text()不存在，应改为elem:get_text()\n"
	desc += "- bad argument #1 to 'pairs' (table expected, got string): 对非table值用了pairs()，先用type()检查\n"
	desc += "- registry overflow: 循环过大或表元素过多，加循环上限(≤10000)\n"
	desc += "- attempt to index a nil value: 工具返回nil(如搜索无结果)，先判nil再操作\n"
	desc += "- Invalid token near '中文': 中文标识符不支持，table中文key改用方括号语法 {[\"中文key\"] = value}\n"
	desc += "- bad argument #1 to 'load' (function expected, got string): GopherLua的load()行为与标准Lua不同，使用loadstring()替代\n"
	desc += "- attempt to call a non-function object (os.execute/io.popen): 沙箱已移除这些函数，用yaml.write_file或tool调用替代"
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
