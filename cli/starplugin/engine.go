package starplugin

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	openapiClient "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	openapiTeaUtils "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	jmespath "github.com/jmespath/go-jmespath"

	"github.com/aliyun/aliyun-cli/v3/config"
	"github.com/aliyun/aliyun-cli/v3/util"

	"go.starlark.net/starlark"
)

// GlobalFlags holds flags common to all commands (extracted before plugin arg parsing).
type GlobalFlags struct {
	DryRun   bool
	Region   string
	Endpoint string
	Query    string // JMESPath
	Quiet    bool
	Pager    string
	LogLevel string
}

// PluginExists checks whether a starlark plugin exists for the given product name.
func PluginExists(productName string) bool {
	_, _, err := FindPlugin(productName)
	return err == nil
}

// Execute is the main entry point for Starlark plugin execution.
func Execute(profile *config.Profile, productName string, args []string, stdout io.Writer, stderr io.Writer) error {
	meta, pluginDir, err := FindPlugin(productName)
	if err != nil {
		return err
	}

	// Determine API version
	apiVersion := meta.DefaultAPIVersion

	// Extract global flags before processing
	gflags, remainingArgs := extractGlobalFlags(args)

	// Override region/endpoint from global flags
	if gflags.Region != "" {
		profile.RegionId = gflags.Region
	}

	// No args or --help → show product help
	if len(remainingArgs) == 0 || (len(remainingArgs) == 1 && (remainingArgs[0] == "--help" || remainingArgs[0] == "-h" || remainingArgs[0] == "help")) {
		return showProductHelp(meta, pluginDir, apiVersion, productName, stdout)
	}

	cmdName := remainingArgs[0]
	cmdArgs := remainingArgs[1:]

	// Check for command-level --help
	for _, a := range cmdArgs {
		if a == "--help" || a == "-h" {
			return showCommandHelp(meta, pluginDir, apiVersion, cmdName, productName, stdout)
		}
	}

	// Resolve and load the .star file
	starFile, err := ResolveCommandFile(pluginDir, apiVersion, cmdName)
	if err != nil {
		return err
	}

	hctx := &HostContext{Stdout: stdout, Stderr: stderr}
	globals, err := LoadStarFile(pluginDir, starFile, hctx)
	if err != nil {
		return err
	}

	// Call command() to get declaration
	commandFn, ok := globals["command"]
	if !ok {
		return fmt.Errorf("star file %s: missing command() function", starFile)
	}
	thread := &starlark.Thread{Name: "exec:" + cmdName}
	cmdResult, err := starlark.Call(thread, commandFn, nil, nil)
	if err != nil {
		return fmt.Errorf("calling command(): %w", err)
	}
	cmdDict, ok := cmdResult.(*starlark.Dict)
	if !ok {
		return fmt.Errorf("command() must return a dict")
	}
	cmdDecl := parseCommandDecl(dictToMap(cmdDict))

	// Parse CLI flags based on param declarations
	parsedArgs, err := parseFlags(cmdDecl.Params, cmdArgs)
	if err != nil {
		return err
	}

	// Check required params (skip in dry-run mode for better UX)
	if !gflags.DryRun {
		for _, p := range cmdDecl.Params {
			if p.Required {
				if _, exists := parsedArgs[p.Name]; !exists {
					return fmt.Errorf("required parameter --%s is missing", p.Name)
				}
			}
		}
	}

	// Build the ctx dict for Starlark
	ctxMap := map[string]interface{}{
		"region":        profile.RegionId,
		"output_format": "json",
		"language":      profile.Language,
		"plugin_dir":    pluginDir,
		"api_version":   apiVersion,
		"product_code":  meta.ProductCode,
	}
	starlarkCtx := mapToDict(ctxMap)
	starlarkArgs := mapToDict(parsedArgs)

	// Load endpoints
	endpoints, _ := loadEndpoints(pluginDir)

	// Wire up host context for call_api support
	hctx.Profile = profile
	hctx.Meta = meta
	hctx.CmdDecl = &cmdDecl
	hctx.Endpoints = endpoints

	// Handle --cli-dry-run: build request and print details without sending
	if gflags.DryRun {
		return executeDryRun(profile, meta, &cmdDecl, globals, thread, starlarkCtx, starlarkArgs, endpoints, &gflags, stdout, stderr)
	}

	// Check if the star file defines run() — custom multi-step execution
	if runFn, ok := globals["run"]; ok {
		return executeRunMode(thread, runFn, starlarkCtx, starlarkArgs, globals, &gflags, stdout)
	}

	// Standard single-request path: build_request(ctx, args)
	buildRequestFn, ok := globals["build_request"]
	if !ok {
		return fmt.Errorf("star file %s: missing build_request() or run() function", starFile)
	}

	reqResult, err := starlark.Call(thread, buildRequestFn, starlark.Tuple{starlarkCtx, starlarkArgs}, nil)
	if err != nil {
		return fmt.Errorf("calling build_request(): %w", err)
	}
	reqDict, ok := reqResult.(*starlark.Dict)
	if !ok {
		return fmt.Errorf("build_request() must return a dict")
	}
	request := parseRequestResult(dictToMap(reqDict))

	// Override endpoint from global flags
	if gflags.Endpoint != "" {
		request.EndpointOverride = gflags.Endpoint
	}

	// Execute the API call
	rawResponse, err := executeAPICall(profile, meta, &cmdDecl, &request, endpoints)
	if err != nil {
		// Check on_error hook
		if onErrorFn, ok := globals["on_error"]; ok {
			errMap := map[string]interface{}{
				"message": err.Error(),
			}
			_, _ = starlark.Call(thread, onErrorFn, starlark.Tuple{starlarkCtx, mapToDict(errMap)}, nil)
		}
		return err
	}

	// Extract the body from the SDK response envelope
	response, _ := rawResponse["body"].(map[string]interface{})
	if response == nil {
		response = rawResponse
	}

	// after_call hook
	if afterCallFn, ok := globals["after_call"]; ok {
		result, err := starlark.Call(thread, afterCallFn, starlark.Tuple{starlarkCtx, goToStarlark(response)}, nil)
		if err == nil && result != starlark.None {
			if d, ok := result.(*starlark.Dict); ok {
				response = dictToMap(d)
			}
		}
	}

	// format_output hook
	if formatFn, ok := globals["format_output"]; ok {
		result, err := starlark.Call(thread, formatFn, starlark.Tuple{starlarkCtx, goToStarlark(response)}, nil)
		if err == nil && result == starlark.None {
			return nil // plugin handled output itself
		}
		if err == nil {
			if d, ok := result.(*starlark.Dict); ok {
				response = dictToMap(d)
			}
		}
	}

	// Apply --cli-query JMESPath filter
	if gflags.Query != "" {
		filtered, err := applyJMESPath(response, gflags.Query)
		if err != nil {
			return fmt.Errorf("--cli-query error: %w", err)
		}
		if gflags.Quiet {
			return nil
		}
		out, err := json.MarshalIndent(filtered, "", "\t")
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, string(out))
		return nil
	}

	if gflags.Quiet {
		return nil
	}

	// Default output: JSON
	out, err := json.MarshalIndent(response, "", "\t")
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, string(out))
	return nil
}

// executeRunMode handles commands that define run(ctx, args) for multi-step logic.
// The run() function controls all API calls via host.call_api() and output via host.printf/print_result.
// If run() returns a non-None dict, it is printed as JSON (same as standard mode).
func executeRunMode(thread *starlark.Thread, runFn starlark.Value, ctx *starlark.Dict, args *starlark.Dict, globals starlark.StringDict, gflags *GlobalFlags, stdout io.Writer) error {
	result, err := starlark.Call(thread, runFn, starlark.Tuple{ctx, args}, nil)
	if err != nil {
		// Check on_error hook
		if onErrorFn, ok := globals["on_error"]; ok {
			errMap := map[string]interface{}{
				"message": err.Error(),
			}
			_, _ = starlark.Call(thread, onErrorFn, starlark.Tuple{ctx, mapToDict(errMap)}, nil)
		}
		return fmt.Errorf("calling run(): %w", err)
	}

	// If run() returns None, it handled output itself
	if result == starlark.None {
		return nil
	}

	// If run() returns a dict, output it as JSON
	if d, ok := result.(*starlark.Dict); ok {
		response := dictToMap(d)
		if gflags.Query != "" {
			filtered, err := applyJMESPath(response, gflags.Query)
			if err != nil {
				return fmt.Errorf("--cli-query error: %w", err)
			}
			if gflags.Quiet {
				return nil
			}
			out, err := json.MarshalIndent(filtered, "", "\t")
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, string(out))
			return nil
		}
		if gflags.Quiet {
			return nil
		}
		out, err := json.MarshalIndent(response, "", "\t")
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, string(out))
	}
	return nil
}

// executeAPICall performs the actual HTTP request using the OpenAPI SDK.
func executeAPICall(profile *config.Profile, meta *PluginMeta, cmdDecl *CommandDecl, req *RequestResult, endpoints map[string]string) (map[string]interface{}, error) {
	credential, err := profile.GetCredential(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("get credential failed: %w", err)
	}

	conf := openapiClient.Config{
		Credential: credential,
		RegionId:   tea.String(profile.RegionId),
	}

	// Resolve endpoint
	endpoint := resolveEndpoint(profile.RegionId, endpoints, req.EndpointOverride)
	if endpoint != "" {
		conf.Endpoint = tea.String(endpoint)
	}

	ua := util.GetAliyunCliUserAgent()
	conf.SetUserAgent(ua)

	if profile.ReadTimeout > 0 {
		conf.SetReadTimeout(profile.ReadTimeout * 1000)
	}
	if profile.ConnectTimeout > 0 {
		conf.SetConnectTimeout(profile.ConnectTimeout * 1000)
	}

	client, err := openapiClient.NewClient(&conf)
	if err != nil {
		return nil, fmt.Errorf("create openapi client failed: %w", err)
	}

	// Build the OpenAPI request
	openapiReq := &openapiutil.OpenApiRequest{
		Query:   map[string]*string{},
		Headers: map[string]*string{},
		HostMap: map[string]*string{},
	}

	// Set query params
	if req.Query != nil {
		for k, v := range req.Query {
			openapiReq.Query[k] = tea.String(fmt.Sprintf("%v", v))
		}
	}

	// Set headers
	if req.Headers != nil {
		for k, v := range req.Headers {
			openapiReq.Headers[k] = tea.String(v)
		}
	}

	// Set host map
	if req.HostMap != nil {
		for k, v := range req.HostMap {
			openapiReq.HostMap[k] = tea.String(v)
		}
	}

	// Set body
	if req.Body != nil {
		openapiReq.Body = convertBodyToTeaMap(req.Body)
	}

	// Inject bearer token if configured
	profile.InjectBearerTokenHeader(openapiReq.Headers)

	// Runtime options
	runtime := &openapiTeaUtils.RuntimeOptions{}
	if profile.RetryCount > 0 {
		runtime.SetAutoretry(true)
		runtime.SetMaxAttempts(profile.RetryCount)
	}

	// Determine style and method
	style := strings.ToUpper(cmdDecl.Style)
	if style == "" {
		style = "ROA"
	}
	method := strings.ToUpper(req.Method)
	if method == "" {
		if style == "RPC" {
			method = "POST"
		} else {
			method = "GET"
		}
	}

	action := req.Action
	if action == "" {
		action = meta.ProductCode
	}
	version := meta.DefaultAPIVersion
	authType := profile.OpenAPIAuthType()

	// Execute using legacy methods that handle signing internally
	var resp map[string]interface{}
	if style == "ROA" {
		if req.BodyType == "formData" {
			resp, err = client.DoROARequestWithForm(
				tea.String(action),
				tea.String(version),
				tea.String("HTTPS"),
				tea.String(method),
				tea.String(authType),
				tea.String(req.URL),
				tea.String("json"),
				openapiReq,
				runtime,
			)
		} else {
			resp, err = client.DoROARequest(
				tea.String(action),
				tea.String(version),
				tea.String("HTTPS"),
				tea.String(method),
				tea.String(authType),
				tea.String(req.URL),
				tea.String("json"),
				openapiReq,
				runtime,
			)
		}
	} else {
		// RPC style
		params := &openapiClient.Params{
			Action:      tea.String(action),
			Version:     tea.String(version),
			Protocol:    tea.String("HTTPS"),
			Method:      tea.String(method),
			AuthType:    tea.String(authType),
			Style:       tea.String("RPC"),
			Pathname:    tea.String("/"),
			ReqBodyType: tea.String(req.BodyType),
			BodyType:    tea.String("json"),
		}
		resp, err = client.DoRequest(params, openapiReq, runtime)
	}

	if err != nil {
		return nil, err
	}

	return resp, nil
}

func resolveEndpoint(region string, endpoints map[string]string, override string) string {
	if override != "" {
		return override
	}
	if endpoints == nil {
		return ""
	}
	// Check regional endpoint
	if ep, ok := endpoints[region]; ok {
		return ep
	}
	// Check global endpoint
	if ep, ok := endpoints["global"]; ok {
		return ep
	}
	return ""
}

func convertBodyToTeaMap(body map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range body {
		result[k] = v
	}
	return result
}

// loadEndpoints loads the endpoints.star file and returns the endpoint map.
func loadEndpoints(pluginDir string) (map[string]string, error) {
	epFile := pluginDir + "/endpoints.star"
	if _, err := os.Stat(epFile); err != nil {
		return nil, nil
	}

	hctx := &HostContext{Stdout: os.Stdout, Stderr: os.Stderr}
	globals, err := LoadStarFile(pluginDir, epFile, hctx)
	if err != nil {
		return nil, err
	}

	epFn, ok := globals["endpoints"]
	if !ok {
		return nil, nil
	}

	thread := &starlark.Thread{Name: "endpoints"}
	result, err := starlark.Call(thread, epFn, nil, nil)
	if err != nil {
		return nil, err
	}

	d, ok := result.(*starlark.Dict)
	if !ok {
		return nil, nil
	}

	epMap := dictToMap(d)
	flat := make(map[string]string)

	// Flatten: if there's a "regional" sub-dict, merge it into flat map
	if regional, ok := epMap["regional"].(map[string]interface{}); ok {
		for k, v := range regional {
			if s, ok := v.(string); ok {
				flat[k] = s
			}
		}
	}
	if global, ok := epMap["global"].(string); ok {
		flat["global"] = global
	}

	return flat, nil
}

// parseFlags parses CLI args based on ParamDecl into a map.
func parseFlags(params []ParamDecl, args []string) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	i := 0
	for i < len(args) {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			i++
			continue
		}
		flagName := strings.TrimPrefix(arg, "--")

		// Find matching param
		var param *ParamDecl
		for idx := range params {
			if params[idx].Name == flagName {
				param = &params[idx]
				break
			}
		}
		if param == nil {
			// Unknown flag - skip it (might be a global flag handled elsewhere)
			i++
			if i < len(args) && !strings.HasPrefix(args[i], "--") {
				i++
			}
			continue
		}

		// Boolean flag: might not have a value
		if param.Type == "bool" {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				val := args[i+1]
				result[flagName] = (val == "true" || val == "1" || val == "yes")
				i += 2
			} else {
				result[flagName] = true
				i++
			}
			continue
		}

		// Expect value
		if i+1 >= len(args) {
			return nil, fmt.Errorf("flag --%s requires a value", flagName)
		}
		val := args[i+1]
		i += 2

		switch param.Type {
		case "int":
			var n int
			if _, err := fmt.Sscanf(val, "%d", &n); err != nil {
				return nil, fmt.Errorf("flag --%s: expected integer, got %q", flagName, val)
			}
			result[flagName] = n
		case "float":
			var f float64
			if _, err := fmt.Sscanf(val, "%f", &f); err != nil {
				return nil, fmt.Errorf("flag --%s: expected float, got %q", flagName, val)
			}
			result[flagName] = f
		case "array", "object", "map", "any":
			// Parse as JSON
			var parsed interface{}
			if err := json.Unmarshal([]byte(val), &parsed); err != nil {
				// If not valid JSON, treat as string
				result[flagName] = val
			} else {
				result[flagName] = parsed
			}
		default:
			result[flagName] = val
		}
	}
	return result, nil
}

// showProductHelp displays the product-level help (list of commands).
func showProductHelp(meta *PluginMeta, pluginDir string, version string, productName string, w io.Writer) error {
	commands, err := ListCommands(pluginDir, version)
	if err != nil {
		return err
	}

	lang := os.Getenv("ALIBABA_CLOUD_LANGUAGE")
	if lang == "" {
		lang = "zh"
	}

	fmt.Fprintf(w, "\n描述: %s\n\n", meta.Description)
	fmt.Fprintf(w, "用法: aliyun %s <命令> [参数]\n\n", productName)
	fmt.Fprintf(w, "可用命令:\n")

	// Sort commands by name
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Name < commands[j].Name
	})

	maxLen := 0
	for _, cmd := range commands {
		if len(cmd.Name) > maxLen {
			maxLen = len(cmd.Name)
		}
	}

	for _, cmd := range commands {
		desc := cmd.Description.Zh
		if lang == "en" {
			desc = cmd.Description.En
		}
		fmt.Fprintf(w, "  %-*s    %s\n", maxLen, cmd.Name, desc)
	}
	fmt.Fprintln(w)
	return nil
}

// showCommandHelp displays help for a single command, matching Go binary plugin format.
func showCommandHelp(meta *PluginMeta, pluginDir string, version string, cmdName string, productName string, w io.Writer) error {
	starFile, err := ResolveCommandFile(pluginDir, version, cmdName)
	if err != nil {
		return err
	}

	hctx := &HostContext{Stdout: w, Stderr: os.Stderr}
	globals, err := LoadStarFile(pluginDir, starFile, hctx)
	if err != nil {
		return err
	}

	commandFn, ok := globals["command"]
	if !ok {
		return fmt.Errorf("missing command() function")
	}

	thread := &starlark.Thread{Name: "help:" + cmdName}
	result, err := starlark.Call(thread, commandFn, nil, nil)
	if err != nil {
		return err
	}
	d, ok := result.(*starlark.Dict)
	if !ok {
		return fmt.Errorf("command() must return a dict")
	}
	cmdDecl := parseCommandDecl(dictToMap(d))

	lang := os.Getenv("ALIBABA_CLOUD_LANGUAGE")
	if lang == "" {
		lang = "zh"
	}

	desc := cmdDecl.Description.Zh
	if lang == "en" {
		desc = cmdDecl.Description.En
	}

	// Match the Go binary plugin help format exactly
	fmt.Fprintf(w, "阿里云CLI命令行工具 %s\n\n", meta.Version)
	fmt.Fprintf(w, "描述: %s\n\n", desc)
	fmt.Fprintf(w, "API 版本: %s\n\n", version)
	fmt.Fprintf(w, "使用:\n")
	fmt.Fprintf(w, "  aliyun %s %s [parameters]\n\n", productName, cmdDecl.Name)

	if len(cmdDecl.Params) > 0 {
		fmt.Fprintf(w, "参数:\n")
		// Use fixed column width 26 for flag names (matching Go binary plugin)
		const flagColWidth = 26

		for _, p := range cmdDecl.Params {
			required := ""
			if p.Required {
				required = " (必填)"
			}
			pdesc := p.Description.Zh
			if lang == "en" {
				pdesc = p.Description.En
			}
			typeName := p.Type
			if typeName == "" {
				typeName = "string"
			}
			flagName := "--" + p.Name
			fullDesc := fmt.Sprintf("%s, %s%s", typeName, pdesc, required)
			printWrappedFlag(w, flagName, fullDesc, flagColWidth, 80)
		}
	}

	// Print global flags
	fmt.Fprintf(w, "\n全局参数:\n")
	printGlobalFlagsHelp(w, cmdDecl.Name)

	// Print examples if we have any info
	fmt.Fprintf(w, "\n示例:\n")
	fmt.Fprintf(w, "  aliyun %s %s\n", productName, cmdDecl.Name)

	return nil
}

// printGlobalFlagsHelp prints the global flags section in help output.
func printGlobalFlagsHelp(w io.Writer, cmdName string) {
	const flagColWidth = 26
	globalFlags := []struct {
		name string
		desc string
	}{
		{"--cli-ai-mode", "bool, 本次执行启用 AI 模式"},
		{"--cli-dry-run", "bool, 启用模拟运行模式: 打印请求详细信息但不发送实际的 API 调用"},
		{"--cli-query", "string, 使用 `--cli-query <jmespath>` 通过 JMESPath 表达式过滤输出"},
		{"--endpoint", "string, 覆盖服务端点(例如: --endpoint https://ecs.cn-hangzhou.aliyuncs.com)"},
		{"--log-level", "string, 设置日志级别: DEBUG、INFO、WARN、ERROR(默认: ERROR)"},
		{"--pager, --all-pages", "list, 使用 `--pager` 合并可分页 API 的页面 (format: key=value key2=value2)"},
		{"-q, --quiet", "bool, 禁用输出(安静模式)"},
		{"--region", "string, 覆盖服务端点的地域ID(例如: --region cn-hangzhou)"},
		{"-h, --help", "bool, help for " + cmdName},
	}

	for _, gf := range globalFlags {
		printWrappedFlag(w, gf.name, gf.desc, flagColWidth, 78)
	}
}

// printWrappedFlag prints a flag name and its description with proper text wrapping.
// flagColWidth is the total width allocated for the flag column (including "  " prefix).
// maxWidth is the maximum line width in display columns.
func printWrappedFlag(w io.Writer, flagName string, desc string, flagColWidth int, maxWidth int) {
	prefix := fmt.Sprintf("  %-*s", flagColWidth, flagName)
	indentStr := strings.Repeat(" ", flagColWidth+2)
	descWidth := maxWidth - flagColWidth - 2

	// Split description by newlines first
	lines := strings.Split(desc, "\n")
	first := true
	for _, line := range lines {
		if first {
			wrapped := wrapLineByDisplayWidth(line, descWidth)
			for i, wl := range wrapped {
				if i == 0 {
					fmt.Fprintf(w, "%s%s\n", prefix, wl)
				} else {
					fmt.Fprintf(w, "%s%s\n", indentStr, wl)
				}
			}
			first = false
		} else {
			if line == "" {
				continue
			}
			wrapped := wrapLineByDisplayWidth(line, descWidth)
			for _, wl := range wrapped {
				fmt.Fprintf(w, "%s%s\n", indentStr, wl)
			}
		}
	}
}

// displayWidth returns the display width of a string, treating CJK characters as width 2.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isCJK(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// isCJK checks if a rune is a CJK character (takes 2 display columns).
func isCJK(r rune) bool {
	return (r >= 0x2E80 && r <= 0x9FFF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0xFF00 && r <= 0xFFEF) ||
		(r >= 0x20000 && r <= 0x2FA1F)
}

// wrapLineByDisplayWidth wraps a single line of text to fit within maxWidth display columns.
func wrapLineByDisplayWidth(text string, maxWidth int) []string {
	if maxWidth <= 0 || displayWidth(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	runes := []rune(text)

	for len(runes) > 0 {
		w := 0
		breakIdx := 0
		lastSpace := -1

		for i, r := range runes {
			rw := 1
			if isCJK(r) {
				rw = 2
			}
			if w+rw > maxWidth {
				break
			}
			w += rw
			breakIdx = i + 1
			if r == ' ' {
				lastSpace = i
			}
		}

		if breakIdx >= len(runes) {
			lines = append(lines, string(runes))
			break
		}

		// Prefer breaking at space, but only if it's reasonably close to the end
		if lastSpace > breakIdx/2 {
			lines = append(lines, string(runes[:lastSpace]))
			runes = runes[lastSpace+1:]
		} else {
			// For CJK text without spaces, break at any CJK character boundary
			lines = append(lines, string(runes[:breakIdx]))
			runes = runes[breakIdx:]
		}
	}

	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// extractGlobalFlags separates global flags from plugin-specific args.
func extractGlobalFlags(args []string) (GlobalFlags, []string) {
	var gf GlobalFlags
	var remaining []string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--cli-dry-run":
			gf.DryRun = true
			i++
		case "--quiet", "-q":
			gf.Quiet = true
			i++
		case "--region":
			if i+1 < len(args) {
				gf.Region = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--endpoint":
			if i+1 < len(args) {
				gf.Endpoint = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--cli-query":
			if i+1 < len(args) {
				gf.Query = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--pager", "--all-pages":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				gf.Pager = args[i+1]
				i += 2
			} else {
				gf.Pager = "true"
				i++
			}
		case "--log-level":
			if i+1 < len(args) {
				gf.LogLevel = args[i+1]
				i += 2
			} else {
				i++
			}
		default:
			remaining = append(remaining, args[i])
			i++
		}
	}
	return gf, remaining
}

func hasBodyParams(cmdDecl *CommandDecl) bool {
	for _, p := range cmdDecl.Params {
		if p.Position == "body" || p.Position == "formData" {
			return true
		}
	}
	return false
}

// executeDryRun builds the request and prints details without sending.
func executeDryRun(profile *config.Profile, meta *PluginMeta, cmdDecl *CommandDecl, globals starlark.StringDict, thread *starlark.Thread, ctx *starlark.Dict, args *starlark.Dict, endpoints map[string]string, gflags *GlobalFlags, w io.Writer, errW io.Writer) error {
	style := strings.ToUpper(cmdDecl.Style)
	if style == "" {
		style = "RPC"
	}

	var request *RequestResult

	// Try build_request first
	if buildRequestFn, ok := globals["build_request"]; ok {
		reqResult, err := starlark.Call(thread, buildRequestFn, starlark.Tuple{ctx, args}, nil)
		if err != nil {
			return fmt.Errorf("calling build_request(): %w", err)
		}
		reqDict, ok := reqResult.(*starlark.Dict)
		if !ok {
			return fmt.Errorf("build_request() must return a dict")
		}
		req := parseRequestResult(dictToMap(reqDict))
		request = &req
	} else {
		// For run() mode, we can't fully build the request but show what we can
		request = &RequestResult{
			Method: "POST",
			Action: meta.ProductCode,
		}
	}

	// Resolve endpoint
	endpointOverride := request.EndpointOverride
	if gflags.Endpoint != "" {
		endpointOverride = gflags.Endpoint
	}
	endpoint := resolveEndpoint(profile.RegionId, endpoints, endpointOverride)

	// Determine method
	method := strings.ToUpper(request.Method)
	if method == "" {
		if style == "RPC" {
			method = "POST"
		} else {
			method = "GET"
		}
	}

	action := request.Action
	if action == "" {
		action = meta.ProductCode
	}
	version := meta.DefaultAPIVersion

	// Print dry-run details to stderr (matching Go binary plugin behavior)
	fmt.Fprintf(errW, "============================================================\n")
	fmt.Fprintf(errW, "DRY-RUN MODE: Request Details (No actual API call)\n")
	fmt.Fprintf(errW, "============================================================\n")
	fmt.Fprintf(errW, "Method: %s\n", method)
	if style == "ROA" && request.URL != "" {
		fmt.Fprintf(errW, "URL: %s\n", request.URL)
	} else {
		fmt.Fprintf(errW, "URL: \n")
	}
	fmt.Fprintf(errW, "Endpoint: %s\n", endpoint)
	fmt.Fprintf(errW, "API Version: %s\n", version)
	fmt.Fprintf(errW, "API Action: %s\n", action)
	fmt.Fprintf(errW, "Protocol: HTTPS\n")
	fmt.Fprintf(errW, "Style: %s\n", style)

	// Print query parameters
	if len(request.Query) > 0 {
		fmt.Fprintf(errW, "Query Parameters:\n")
		keys := make([]string, 0, len(request.Query))
		for k := range request.Query {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(errW, "  %s: %v\n", k, request.Query[k])
		}
	}

	// Print body parameters
	if len(request.Body) > 0 {
		fmt.Fprintf(errW, "Body Parameters:\n")
		keys := make([]string, 0, len(request.Body))
		for k := range request.Body {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(errW, "  %s: %v\n", k, request.Body[k])
		}
	} else if hasBodyParams(cmdDecl) || (style == "ROA" && method != "GET") {
		fmt.Fprintf(errW, "Body:\n")
		fmt.Fprintf(errW, "  {}\n")
	}

	// Print headers
	if len(request.Headers) > 0 {
		fmt.Fprintf(errW, "Headers:\n")
		for k, v := range request.Headers {
			fmt.Fprintf(errW, "  %s: %s\n", k, v)
		}
	}

	fmt.Fprintf(errW, "============================================================\n")
	fmt.Fprintf(errW, "Request NOT sent (dry-run mode)\n")
	fmt.Fprintf(errW, "============================================================\n")

	// Print JSON result to stdout (matching Go binary plugin behavior)
	msg := map[string]string{"message": "dry-run mode - no request sent"}
	out, _ := json.MarshalIndent(msg, "", "\t")
	fmt.Fprintln(w, string(out))

	return nil
}

// applyJMESPath applies a JMESPath expression to the response.
func applyJMESPath(data interface{}, expr string) (interface{}, error) {
	result, err := jmespath.Search(expr, data)
	if err != nil {
		return nil, err
	}
	return result, nil
}
