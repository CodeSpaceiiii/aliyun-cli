package starplugin

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	jmespath "github.com/jmespath/go-jmespath"

	"github.com/aliyun/aliyun-cli/v3/config"
)

// ExecOptions holds post-call processing options extracted from CLI flags.
type ExecOptions struct {
	Pager      *StarPager
	Waiter     *StarWaiter
	QueryExpr  string // JMESPath filter (--cli-query)
	Quiet      bool   // suppress output (--quiet)
	DryRun     bool   // skip actual call (--dryrun)
	OutputMode string // "json" | "table" | "text"
}

// StarPager implements pagination for the star plugin system.
type StarPager struct {
	Mode           string // "token" | "number"
	TokenParam     string // request param name for next token
	TokenField     string // response field (JMESPath) for next token
	PageParam      string // request param name for page number
	SizeParam      string // request param name for page size
	TotalField     string // response field (JMESPath) for total count
	CollectionPath string // response field (JMESPath) for result array
	PageSize       int
}

// StarWaiter implements polling wait for the star plugin system.
type StarWaiter struct {
	Expr     string // JMESPath expression to evaluate
	To       string // expected value
	Timeout  int    // seconds, default 180
	Interval int    // seconds, default 5
}

type apiCallFn func() (map[string]interface{}, error)

// executePager calls the API repeatedly, collecting paginated results.
func executePager(pager *StarPager, callFn apiCallFn, setParam func(key string, value interface{})) (map[string]interface{}, error) {
	var results []interface{}
	currentPage := 1

	for {
		resp, err := callFn()
		if err != nil {
			return nil, err
		}

		body, err := json.Marshal(resp)
		if err != nil {
			return nil, err
		}
		var parsed interface{}
		_ = json.Unmarshal(body, &parsed)

		// Extract collection
		if pager.CollectionPath != "" {
			if val, err := jmespath.Search(pager.CollectionPath, parsed); err == nil {
				if arr, ok := val.([]interface{}); ok {
					results = append(results, arr...)
				}
			}
		} else {
			path := detectArrayPath(resp)
			if path != "" {
				if val, err := jmespath.Search(path, parsed); err == nil {
					if arr, ok := val.([]interface{}); ok {
						results = append(results, arr...)
					}
				}
			}
		}

		// Check if there are more pages
		if pager.Mode == "token" {
			nextToken := ""
			if pager.TokenField != "" {
				if val, err := jmespath.Search(pager.TokenField, parsed); err == nil {
					if s, ok := val.(string); ok {
						nextToken = s
					}
				}
			}
			if nextToken == "" {
				break
			}
			setParam(pager.TokenParam, nextToken)
		} else {
			// number mode
			totalCount := 0
			if pager.TotalField != "" {
				if val, err := jmespath.Search(pager.TotalField, parsed); err == nil {
					switch v := val.(type) {
					case float64:
						totalCount = int(v)
					case json.Number:
						n, _ := v.Int64()
						totalCount = int(n)
					}
				}
			}
			pageSize := pager.PageSize
			if pageSize <= 0 {
				pageSize = 10
			}
			pages := int(math.Ceil(float64(totalCount) / float64(pageSize)))
			currentPage++
			if currentPage > pages {
				break
			}
			setParam(pager.PageParam, currentPage)
		}
	}

	// Build result in the collection path structure
	return buildPagerResult(pager.CollectionPath, results), nil
}

func buildPagerResult(collectionPath string, results []interface{}) map[string]interface{} {
	if collectionPath == "" {
		return map[string]interface{}{"Items": results}
	}
	parts := strings.Split(collectionPath, ".")
	key := strings.TrimSuffix(parts[len(parts)-1], "[]")

	if len(parts) >= 2 {
		parent := parts[len(parts)-2]
		return map[string]interface{}{
			parent: map[string]interface{}{
				key: results,
			},
		}
	}
	return map[string]interface{}{key: results}
}

func detectArrayPath(m map[string]interface{}) string {
	for k, v := range m {
		if sub, ok := v.(map[string]interface{}); ok {
			for k2, v2 := range sub {
				if _, ok := v2.([]interface{}); ok {
					return k + "." + k2
				}
			}
		}
	}
	return ""
}

// executeWaiter polls the API until the JMESPath expression matches the target value.
func executeWaiter(waiter *StarWaiter, callFn apiCallFn) (map[string]interface{}, error) {
	timeout := time.Duration(waiter.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	interval := time.Duration(waiter.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}

	begin := time.Now()
	for {
		resp, err := callFn()
		if err != nil {
			return nil, err
		}

		body, err := json.Marshal(resp)
		if err != nil {
			return nil, err
		}
		var parsed interface{}
		_ = json.Unmarshal(body, &parsed)

		if val, err := jmespath.Search(waiter.Expr, parsed); err == nil {
			actual := fmt.Sprintf("%v", val)
			if actual == waiter.To {
				return resp, nil
			}
		}

		if time.Since(begin) > timeout {
			return nil, fmt.Errorf("waiter timeout after %v: %s != %s", timeout, waiter.Expr, waiter.To)
		}
		time.Sleep(interval)
	}
}

// ExecuteWithOptions is the enhanced entry point that supports pager/waiter/query.
func ExecuteWithOptions(profile *config.Profile, productName string, args []string, opts *ExecOptions, stdout, stderr interface{ Write([]byte) (int, error) }) error {
	// For now, delegate to the basic Execute. Pager/waiter will be integrated
	// when the routing layer passes ExecOptions extracted from CLI flags.
	return Execute(profile, productName, args, stdout, stderr)
}
