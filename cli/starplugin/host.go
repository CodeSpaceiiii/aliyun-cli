package starplugin

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aliyun/aliyun-cli/v3/config"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// HostContext holds the runtime state available to host functions.
type HostContext struct {
	Stdout io.Writer
	Stderr io.Writer

	// Set at execution time for host.call_api support
	Profile   *config.Profile
	Meta      *PluginMeta
	CmdDecl   *CommandDecl
	Endpoints map[string]string
}

// NewHostModule creates the `host` Starlark module with all built-in functions.
func NewHostModule(hctx *HostContext) *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "host",
		Members: starlark.StringDict{
			"printf":              starlark.NewBuiltin("host.printf", hctx.builtinPrintf),
			"eprintf":            starlark.NewBuiltin("host.eprintf", hctx.builtinEprintf),
			"print_result":       starlark.NewBuiltin("host.print_result", hctx.builtinPrintResult),
			"json_decode":        starlark.NewBuiltin("host.json_decode", hctx.builtinJSONDecode),
			"json_encode":        starlark.NewBuiltin("host.json_encode", hctx.builtinJSONEncode),
			"flatten":            starlark.NewBuiltin("host.flatten", hctx.builtinFlatten),
			"flatten_repeat_list": starlark.NewBuiltin("host.flatten_repeat_list", hctx.builtinFlattenRepeatList),
			"read_file":          starlark.NewBuiltin("host.read_file", hctx.builtinReadFile),
			"write_file":         starlark.NewBuiltin("host.write_file", hctx.builtinWriteFile),
			"get_env":            starlark.NewBuiltin("host.get_env", hctx.builtinGetEnv),
			"call_api":           starlark.NewBuiltin("host.call_api", hctx.builtinCallAPI),
		},
	}
}

func (h *HostContext) builtinPrintf(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if len(args) == 0 {
		return starlark.None, nil
	}
	format, ok := starlark.AsString(args[0])
	if !ok {
		return starlark.None, fmt.Errorf("host.printf: first argument must be string")
	}
	fmtArgs := make([]interface{}, len(args)-1)
	for i, a := range args[1:] {
		fmtArgs[i] = starlarkToGo(a)
	}
	fmt.Fprintf(h.Stdout, format, fmtArgs...)
	return starlark.None, nil
}

func (h *HostContext) builtinEprintf(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if len(args) == 0 {
		return starlark.None, nil
	}
	format, ok := starlark.AsString(args[0])
	if !ok {
		return starlark.None, fmt.Errorf("host.eprintf: first argument must be string")
	}
	fmtArgs := make([]interface{}, len(args)-1)
	for i, a := range args[1:] {
		fmtArgs[i] = starlarkToGo(a)
	}
	fmt.Fprintf(h.Stderr, format, fmtArgs...)
	return starlark.None, nil
}

func (h *HostContext) builtinPrintResult(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 1 {
		return starlark.None, fmt.Errorf("host.print_result: expects 1 argument")
	}
	goVal := starlarkToGo(args[0])
	data, err := json.MarshalIndent(goVal, "", "  ")
	if err != nil {
		return starlark.None, fmt.Errorf("host.print_result: %w", err)
	}
	fmt.Fprintln(h.Stdout, string(data))
	return starlark.None, nil
}

func (h *HostContext) builtinJSONDecode(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 1 {
		return starlark.None, fmt.Errorf("host.json_decode: expects 1 argument")
	}
	s, ok := starlark.AsString(args[0])
	if !ok {
		return starlark.None, fmt.Errorf("host.json_decode: argument must be string")
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return starlark.None, fmt.Errorf("host.json_decode: %w", err)
	}
	return goToStarlark(v), nil
}

func (h *HostContext) builtinJSONEncode(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 1 {
		return starlark.None, fmt.Errorf("host.json_encode: expects 1 argument")
	}
	goVal := starlarkToGo(args[0])
	data, err := json.Marshal(goVal)
	if err != nil {
		return starlark.None, fmt.Errorf("host.json_encode: %w", err)
	}
	return starlark.String(string(data)), nil
}

// host.flatten(dict, prefix, obj) — writes Prefix.Key=value pairs into dict
func (h *HostContext) builtinFlatten(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 3 {
		return starlark.None, fmt.Errorf("host.flatten: expects 3 arguments (dict, prefix, obj)")
	}
	target, ok := args[0].(*starlark.Dict)
	if !ok {
		return starlark.None, fmt.Errorf("host.flatten: first argument must be dict")
	}
	prefix, ok := starlark.AsString(args[1])
	if !ok {
		return starlark.None, fmt.Errorf("host.flatten: second argument must be string")
	}
	obj, ok := args[2].(*starlark.Dict)
	if !ok {
		return starlark.None, fmt.Errorf("host.flatten: third argument must be dict")
	}
	for _, item := range obj.Items() {
		key, _ := starlark.AsString(item[0])
		val := starlarkToGo(item[1])
		flatKey := prefix + "." + key
		_ = target.SetKey(starlark.String(flatKey), starlark.String(fmt.Sprintf("%v", val)))
	}
	return starlark.None, nil
}

// host.flatten_repeat_list(dict, prefix, array) — writes Prefix.N.Key=value pairs into dict
func (h *HostContext) builtinFlattenRepeatList(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 3 {
		return starlark.None, fmt.Errorf("host.flatten_repeat_list: expects 3 arguments (dict, prefix, array)")
	}
	target, ok := args[0].(*starlark.Dict)
	if !ok {
		return starlark.None, fmt.Errorf("host.flatten_repeat_list: first argument must be dict")
	}
	prefix, ok := starlark.AsString(args[1])
	if !ok {
		return starlark.None, fmt.Errorf("host.flatten_repeat_list: second argument must be string")
	}
	list, ok := args[2].(*starlark.List)
	if !ok {
		return starlark.None, fmt.Errorf("host.flatten_repeat_list: third argument must be list")
	}
	for i := 0; i < list.Len(); i++ {
		elem := list.Index(i)
		itemPrefix := fmt.Sprintf("%s.%d", prefix, i+1)
		if d, ok := elem.(*starlark.Dict); ok {
			for _, item := range d.Items() {
				key, _ := starlark.AsString(item[0])
				val := starlarkToGo(item[1])
				flatKey := itemPrefix + "." + key
				_ = target.SetKey(starlark.String(flatKey), starlark.String(fmt.Sprintf("%v", val)))
			}
		} else {
			val := starlarkToGo(elem)
			_ = target.SetKey(starlark.String(itemPrefix), starlark.String(fmt.Sprintf("%v", val)))
		}
	}
	return starlark.None, nil
}

func (h *HostContext) builtinReadFile(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 1 {
		return starlark.None, fmt.Errorf("host.read_file: expects 1 argument")
	}
	path, ok := starlark.AsString(args[0])
	if !ok {
		return starlark.None, fmt.Errorf("host.read_file: argument must be string")
	}
	if strings.Contains(path, "..") {
		return starlark.None, fmt.Errorf("host.read_file: path traversal not allowed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return starlark.None, fmt.Errorf("host.read_file: %w", err)
	}
	return starlark.String(string(data)), nil
}

func (h *HostContext) builtinWriteFile(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 2 {
		return starlark.None, fmt.Errorf("host.write_file: expects 2 arguments (path, content)")
	}
	path, ok := starlark.AsString(args[0])
	if !ok {
		return starlark.None, fmt.Errorf("host.write_file: first argument must be string")
	}
	if strings.Contains(path, "..") {
		return starlark.None, fmt.Errorf("host.write_file: path traversal not allowed")
	}
	content, ok := starlark.AsString(args[1])
	if !ok {
		return starlark.None, fmt.Errorf("host.write_file: second argument must be string")
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return starlark.None, fmt.Errorf("host.write_file: %w", err)
	}
	return starlark.Bool(true), nil
}

func (h *HostContext) builtinGetEnv(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 1 {
		return starlark.None, fmt.Errorf("host.get_env: expects 1 argument")
	}
	key, ok := starlark.AsString(args[0])
	if !ok {
		return starlark.None, fmt.Errorf("host.get_env: argument must be string")
	}
	return starlark.String(os.Getenv(key)), nil
}

// host.call_api(request_dict) — execute an API call from within Starlark.
// The request_dict has the same structure as build_request() returns:
//
//	{"method": "GET", "url": "/path", "action": "ActionName", "query": {...}, "body": {...}, ...}
//
// Returns the response body as a Starlark dict, or raises an error.
func (h *HostContext) builtinCallAPI(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 1 {
		return starlark.None, fmt.Errorf("host.call_api: expects 1 argument (request dict)")
	}
	reqDict, ok := args[0].(*starlark.Dict)
	if !ok {
		return starlark.None, fmt.Errorf("host.call_api: argument must be dict")
	}
	if h.Profile == nil || h.Meta == nil {
		return starlark.None, fmt.Errorf("host.call_api: not available outside run() context")
	}

	req := parseRequestResult(dictToMap(reqDict))
	rawResp, err := executeAPICall(h.Profile, h.Meta, h.CmdDecl, &req, h.Endpoints)
	if err != nil {
		return starlark.None, fmt.Errorf("host.call_api: %w", err)
	}

	// Extract body from SDK response envelope
	body, _ := rawResp["body"].(map[string]interface{})
	if body == nil {
		body = rawResp
	}
	return goToStarlark(body), nil
}
