package starplugin

import (
	"fmt"
	"sort"

	"go.starlark.net/starlark"
)

// goToStarlark converts a Go value to a Starlark value.
func goToStarlark(v interface{}) starlark.Value {
	switch val := v.(type) {
	case nil:
		return starlark.None
	case bool:
		return starlark.Bool(val)
	case int:
		return starlark.MakeInt(val)
	case int64:
		return starlark.MakeInt64(val)
	case float64:
		return starlark.Float(val)
	case string:
		return starlark.String(val)
	case []interface{}:
		elems := make([]starlark.Value, len(val))
		for i, e := range val {
			elems[i] = goToStarlark(e)
		}
		return starlark.NewList(elems)
	case map[string]interface{}:
		d := starlark.NewDict(len(val))
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_ = d.SetKey(starlark.String(k), goToStarlark(val[k]))
		}
		return d
	default:
		return starlark.String(fmt.Sprintf("%v", val))
	}
}

// starlarkToGo converts a Starlark value to a Go value.
func starlarkToGo(v starlark.Value) interface{} {
	switch val := v.(type) {
	case starlark.NoneType:
		return nil
	case starlark.Bool:
		return bool(val)
	case starlark.Int:
		if i, ok := val.Int64(); ok {
			return i
		}
		if u, ok := val.Uint64(); ok {
			return u
		}
		return val.String()
	case starlark.Float:
		return float64(val)
	case starlark.String:
		return string(val)
	case *starlark.List:
		result := make([]interface{}, val.Len())
		for i := 0; i < val.Len(); i++ {
			result[i] = starlarkToGo(val.Index(i))
		}
		return result
	case starlark.Tuple:
		result := make([]interface{}, len(val))
		for i, e := range val {
			result[i] = starlarkToGo(e)
		}
		return result
	case *starlark.Dict:
		return dictToMap(val)
	default:
		return v.String()
	}
}

// dictToMap converts a Starlark Dict to a Go map.
func dictToMap(d *starlark.Dict) map[string]interface{} {
	result := make(map[string]interface{}, d.Len())
	for _, item := range d.Items() {
		key := starlarkToGo(item[0])
		val := starlarkToGo(item[1])
		if k, ok := key.(string); ok {
			result[k] = val
		}
	}
	return result
}

// mapToDict converts a Go map to a Starlark Dict.
func mapToDict(m map[string]interface{}) *starlark.Dict {
	d := starlark.NewDict(len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_ = d.SetKey(starlark.String(k), goToStarlark(m[k]))
	}
	return d
}

// parseCommandDecl parses the dict returned by command() into CommandDecl.
func parseCommandDecl(d map[string]interface{}) CommandDecl {
	cmd := CommandDecl{}
	if v, ok := d["name"].(string); ok {
		cmd.Name = v
	}
	if v, ok := d["style"].(string); ok {
		cmd.Style = v
	}
	if v, ok := d["description"]; ok {
		cmd.Description = parseI18n(v)
	}
	if v, ok := d["params"].([]interface{}); ok {
		cmd.Params = parseParams(v)
	}
	if v, ok := d["pager"].(map[string]interface{}); ok {
		cmd.Pager = parsePagerConfig(v)
	}
	if v, ok := d["waiters"].(map[string]interface{}); ok {
		cmd.Waiters = parseWaiters(v)
	}
	if v, ok := d["retry"].(map[string]interface{}); ok {
		cmd.Retry = parseRetryConfig(v)
	}
	return cmd
}

func parseI18n(v interface{}) I18nText {
	switch val := v.(type) {
	case map[string]interface{}:
		t := I18nText{}
		if en, ok := val["en"].(string); ok {
			t.En = en
		}
		if zh, ok := val["zh"].(string); ok {
			t.Zh = zh
		}
		return t
	case string:
		return I18nText{En: val, Zh: val}
	default:
		return I18nText{}
	}
}

func parseParams(items []interface{}) []ParamDecl {
	params := make([]ParamDecl, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]interface{}); ok {
			params = append(params, parseParamDecl(m))
		}
	}
	return params
}

func parseParamDecl(m map[string]interface{}) ParamDecl {
	p := ParamDecl{
		Type:     "string",
		Position: "query",
	}
	if v, ok := m["name"].(string); ok {
		p.Name = v
	}
	if v, ok := m["type"].(string); ok {
		p.Type = v
	}
	if v, ok := m["required"].(bool); ok {
		p.Required = v
	}
	if v, ok := m["api_name"].(string); ok {
		p.APIName = v
	}
	if v, ok := m["position"].(string); ok {
		p.Position = v
	}
	if v, ok := m["description"]; ok {
		p.Description = parseI18n(v)
	}
	if v, ok := m["default"]; ok {
		p.Default = v
	}
	if v, ok := m["example"].(string); ok {
		p.Example = v
	}
	if v, ok := m["fields"].([]interface{}); ok {
		p.Fields = parseParams(v)
	}
	if v, ok := m["element"].(map[string]interface{}); ok {
		elem := parseParamDecl(v)
		p.Element = &elem
	}
	return p
}

func parsePagerConfig(m map[string]interface{}) *PagerConfig {
	c := &PagerConfig{}
	if v, ok := m["mode"].(string); ok {
		c.Mode = v
	}
	if v, ok := m["token_param"].(string); ok {
		c.TokenParam = v
	}
	if v, ok := m["token_field"].(string); ok {
		c.TokenField = v
	}
	if v, ok := m["page_param"].(string); ok {
		c.PageParam = v
	}
	if v, ok := m["size_param"].(string); ok {
		c.SizeParam = v
	}
	if v, ok := m["total_field"].(string); ok {
		c.TotalField = v
	}
	if v, ok := m["collection_path"].(string); ok {
		c.CollectionPath = v
	}
	return c
}

func parseWaiters(m map[string]interface{}) map[string]*WaiterConfig {
	waiters := make(map[string]*WaiterConfig)
	for k, v := range m {
		if wm, ok := v.(map[string]interface{}); ok {
			w := &WaiterConfig{}
			if desc, ok := wm["description"]; ok {
				w.Description = parseI18n(desc)
			}
			if s, ok := wm["poll_api"].(string); ok {
				w.PollAPI = s
			}
			if arr, ok := wm["poll_params"].([]interface{}); ok {
				for _, item := range arr {
					if s, ok := item.(string); ok {
						w.PollParams = append(w.PollParams, s)
					}
				}
			}
			if s, ok := wm["expr"].(string); ok {
				w.Expr = s
			}
			if s, ok := wm["to"].(string); ok {
				w.To = s
			}
			if n, ok := wm["timeout"].(int64); ok {
				w.Timeout = int(n)
			}
			if n, ok := wm["interval"].(int64); ok {
				w.Interval = int(n)
			}
			waiters[k] = w
		}
	}
	return waiters
}

func parseRetryConfig(m map[string]interface{}) *RetryConfig {
	c := &RetryConfig{
		MaxAttempts: 5,
		Backoff:     "linear",
		BaseDelayMs: 1000,
	}
	if v, ok := m["max_attempts"].(int64); ok {
		c.MaxAttempts = int(v)
	}
	if v, ok := m["retryable_codes"].([]interface{}); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				c.RetryableCodes = append(c.RetryableCodes, s)
			}
		}
	}
	if v, ok := m["backoff"].(string); ok {
		c.Backoff = v
	}
	if v, ok := m["base_delay_ms"].(int64); ok {
		c.BaseDelayMs = int(v)
	}
	return c
}

// parseRequestResult parses the dict returned by build_request() into RequestResult.
func parseRequestResult(m map[string]interface{}) RequestResult {
	r := RequestResult{
		BodyType: "json",
	}
	if v, ok := m["method"].(string); ok {
		r.Method = v
	}
	if v, ok := m["action"].(string); ok {
		r.Action = v
	}
	if v, ok := m["url"].(string); ok {
		r.URL = v
	}
	if v, ok := m["body_type"].(string); ok {
		r.BodyType = v
	}
	if v, ok := m["endpoint_override"].(string); ok {
		r.EndpointOverride = v
	}
	if v, ok := m["query"].(map[string]interface{}); ok {
		r.Query = v
	}
	if v, ok := m["body"].(map[string]interface{}); ok {
		r.Body = v
	}
	if v, ok := m["headers"].(map[string]interface{}); ok {
		r.Headers = make(map[string]string)
		for k, val := range v {
			if s, ok := val.(string); ok {
				r.Headers[k] = s
			}
		}
	}
	if v, ok := m["host_map"].(map[string]interface{}); ok {
		r.HostMap = make(map[string]string)
		for k, val := range v {
			if s, ok := val.(string); ok {
				r.HostMap[k] = s
			}
		}
	}
	return r
}
