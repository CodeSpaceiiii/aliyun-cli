package starplugin

// PluginMeta represents the plugin.json metadata file.
type PluginMeta struct {
	Name              string   `json:"name"`
	Version           string   `json:"version"`
	ProductCode       string   `json:"product_code"`
	Description       string   `json:"description"`
	DefaultAPIVersion string   `json:"default_api_version"`
	APIVersions       []string `json:"api_versions"`
	MinHostVersion    string   `json:"min_host_version"`
}

// CommandDecl is the parsed result of a .star file's command() function.
type CommandDecl struct {
	Name        string
	Description I18nText
	Style       string // ROA | RPC
	Params      []ParamDecl
	Pager       *PagerConfig
	Waiters     map[string]*WaiterConfig
	Retry       *RetryConfig
}

// I18nText holds bilingual text.
type I18nText struct {
	En string
	Zh string
}

// ParamDecl describes a CLI parameter for help and validation.
type ParamDecl struct {
	Name        string
	Type        string
	Required    bool
	APIName     string
	Position    string
	Description I18nText
	Default     interface{}
	Example     string
	Fields      []ParamDecl
	Element     *ParamDecl
}

// PagerConfig declares how pagination works for a command.
type PagerConfig struct {
	Mode           string `json:"mode"` // "token" | "number"
	TokenParam     string `json:"token_param"`
	TokenField     string `json:"token_field"`
	PageParam      string `json:"page_param"`
	SizeParam      string `json:"size_param"`
	TotalField     string `json:"total_field"`
	CollectionPath string `json:"collection_path"`
}

// WaiterConfig declares a polling wait condition.
type WaiterConfig struct {
	Description I18nText
	PollAPI     string   `json:"poll_api"`
	PollParams  []string `json:"poll_params"`
	Expr        string   `json:"expr"`
	To          string   `json:"to"`
	Timeout     int      `json:"timeout"`
	Interval    int      `json:"interval"`
}

// RetryConfig declares retry behavior.
type RetryConfig struct {
	MaxAttempts    int      `json:"max_attempts"`
	RetryableCodes []string `json:"retryable_codes"`
	Backoff        string   `json:"backoff"` // "linear" | "exponential" | "fixed"
	BaseDelayMs    int      `json:"base_delay_ms"`
}

// RequestResult is the parsed result of build_request().
type RequestResult struct {
	Method           string
	Action           string
	URL              string
	Query            map[string]interface{}
	Body             map[string]interface{}
	BodyType         string // "json" | "formData"
	Headers          map[string]string
	HostMap          map[string]string
	EndpointOverride string
}
