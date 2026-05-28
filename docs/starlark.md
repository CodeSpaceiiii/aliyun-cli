# Starlark 插件系统接口文档

## 概述

Starlark 插件系统是 aliyun-cli 的轻量级插件机制，使用 Starlark（Python-like）脚本语言编写插件，替代传统的 Go 二进制插件。

## 架构

```
aliyun-cli (Go 主程序)
    ├── commando.go: 路由分发
    │   └── 检测 starlark 插件 → starplugin.Execute()
    └── cli/starplugin/
        ├── engine.go:    执行引擎（主入口、API调用、dry-run、help）
        ├── loader.go:    插件发现、.star文件加载、模块解析
        ├── host.go:      host模块（Go侧内置函数）
        ├── types.go:     数据结构定义
        ├── converter.go: Go/Starlark 值转换
        └── invoker.go:   分页器/等待器
```

## 路由机制

### 路由优先级

1. 主程序检查 starlark 插件是否存在（`starplugin.PluginExists(productName)`）
2. 若存在，直接路由到 starlark 引擎
3. 若不存在，继续检查 Go 二进制插件

### 插件发现路径

按以下顺序搜索：
1. `ALIBABA_CLOUD_STAR_PLUGIN_PATH` 环境变量指定的路径
2. `~/.aliyun/star-plugins/`

搜索时查找 `<path>/<product_name>/plugin.json` 文件。

## 插件目录结构

```
<product_name>/
├── plugin.json           # 插件元数据
├── endpoints.star        # 端点配置
└── apis/
    └── <version>/        # API 版本目录
        ├── describe_regions.star
        ├── create_instance.star
        └── ...
```

## 文件格式

### plugin.json

```json
{
  "name": "ecs",
  "version": "0.1.0",
  "product_code": "Ecs",
  "description": "阿里云CLI插件, 用于云服务器 ECS操作。",
  "default_api_version": "2014-05-26",
  "api_versions": ["2014-05-26"],
  "min_host_version": "3.3.1"
}
```

### endpoints.star

```python
def endpoints():
    return {
        "regional": {
            "cn-hangzhou": "ecs.cn-hangzhou.aliyuncs.com",
            "cn-beijing": "ecs.cn-beijing.aliyuncs.com",
            # ...
        },
        "global": "ecs.cn-hangzhou.aliyuncs.com",
    }
```

## 插件提供的函数

### command() — 命令声明（必须）

每个 .star 文件必须定义 `command()` 函数，返回命令的元数据字典。

```python
def command():
    return {
        "name": "describe-regions",       # 命令名（kebab-case）
        "style": "RPC",                   # API 风格: "RPC" | "ROA"
        "description": i18n("...", "..."), # 双语描述
        "params": [                       # 参数列表
            param("accept-language", type="string", api_name="AcceptLanguage",
                  position="query", description=i18n("...", "...")),
        ],
        # 可选字段:
        "pager": { ... },                 # 分页配置
        "waiters": { ... },              # 等待器配置
        "retry": { ... },                # 重试配置
    }
```

#### 参数声明 (param)

```python
param(
    name,                 # CLI 参数名（kebab-case, 如 "instance-id"）
    type="string",        # 类型: "string" | "int" | "float" | "bool" | "array" | "object" | "map" | "any"
    required=False,       # 是否必填
    api_name="",          # API 参数名（如 "InstanceId"）, 默认与 name 相同
    position="query",     # 参数位置: "query" | "body" | "header" | "path"
    description=None,     # 参数描述 (i18n)
    default=None,         # 默认值
    example="",           # 示例值
    fields=None,          # 嵌套字段（用于 object 类型）
    element=None,         # 数组元素定义
)
```

### build_request(ctx, args) — 构建请求（模式A）

标准单请求模式。返回一个描述 API 请求的字典。

```python
def build_request(ctx, args):
    query = {}
    if args.get("accept-language"):
        query["AcceptLanguage"] = args["accept-language"]

    return {
        "method": "POST",                    # HTTP 方法
        "action": "DescribeRegions",         # API Action 名称
        # RPC 风格用 query:
        "query": query,                      # Query 参数
        # ROA 风格用 url:
        "url": "/listProducts",              # ROA 请求路径
        # 可选字段:
        "body": {},                          # Body 参数
        "body_type": "json",                 # Body 类型: "json" | "formData"
        "headers": {},                       # 自定义 Headers
        "host_map": {},                      # Host 模板变量
        "endpoint_override": "",             # 覆盖端点
    }
```

### run(ctx, args) — 自定义执行（模式B）

多步骤/自定义逻辑模式。使用 `host.call_api()` 发起多次 API 调用。

```python
def run(ctx, args):
    # 第一次 API 调用
    resp = host.call_api({
        "method": "POST",
        "action": "DescribeInstances",
        "query": {"RegionId": ctx["region"]},
    })

    # 自定义逻辑
    instances = resp["Instances"]["Instance"]
    
    # 第二次 API 调用
    for inst in instances:
        detail = host.call_api({
            "method": "POST",
            "action": "DescribeInstanceAttribute",
            "query": {"InstanceId": inst["InstanceId"]},
        })
        host.printf("Instance: %s Status: %s\n", inst["InstanceId"], detail["Status"])
    
    return None  # 返回 None 表示已自行处理输出
    # 或返回 dict，引擎会输出为 JSON
```

### 可选 Hook 函数

```python
def on_error(ctx, err):
    """错误处理钩子。err 是包含 "message" 字段的 dict。"""
    host.eprintf("Error: %s\n", err["message"])

def after_call(ctx, response):
    """请求成功后的处理钩子。可以修改并返回 response。"""
    return response  # 返回修改后的 response，或 None 不修改

def format_output(ctx, response):
    """自定义输出格式。返回 None 表示已自行处理输出。"""
    host.printf("Custom format: %s\n", host.json_encode(response))
    return None
```

## 主程序提供的能力 (host 模块)

### I/O 函数

| 函数 | 签名 | 说明 |
|------|------|------|
| `host.printf` | `(format, *args)` | 格式化输出到 stdout |
| `host.eprintf` | `(format, *args)` | 格式化输出到 stderr |
| `host.print_result` | `(data)` | JSON 格式化输出到 stdout |

### JSON 处理

| 函数 | 签名 | 说明 |
|------|------|------|
| `host.json_decode` | `(string) → dict/list` | 解析 JSON 字符串 |
| `host.json_encode` | `(value) → string` | 序列化为 JSON 字符串 |

### RPC 参数展开

| 函数 | 签名 | 说明 |
|------|------|------|
| `host.flatten` | `(dict, prefix, obj)` | 将 obj 展开为 `Prefix.Key=value` 写入 dict |
| `host.flatten_repeat_list` | `(dict, prefix, array)` | 将 array 展开为 `Prefix.N.Key=value` 写入 dict |

示例：
```python
query = {}
host.flatten(query, "Tag", {"Key": "env", "Value": "prod"})
# 结果: query = {"Tag.Key": "env", "Tag.Value": "prod"}

host.flatten_repeat_list(query, "Tag", [
    {"Key": "env", "Value": "prod"},
    {"Key": "team", "Value": "infra"},
])
# 结果: query = {"Tag.1.Key": "env", "Tag.1.Value": "prod", "Tag.2.Key": "team", "Tag.2.Value": "infra"}
```

### 文件操作

| 函数 | 签名 | 说明 |
|------|------|------|
| `host.read_file` | `(path) → string` | 读取文件内容（禁止 `..` 路径穿越） |
| `host.write_file` | `(path, content) → bool` | 写入文件（禁止 `..` 路径穿越） |

### 环境变量

| 函数 | 签名 | 说明 |
|------|------|------|
| `host.get_env` | `(key) → string` | 读取环境变量 |

### API 调用

| 函数 | 签名 | 说明 |
|------|------|------|
| `host.call_api` | `(request_dict) → dict` | 发起 API 调用（仅在 `run()` 上下文中可用） |

`host.call_api` 的 request_dict 格式与 `build_request()` 返回值相同。

## ctx 上下文字典

传入 `build_request(ctx, args)` 和 `run(ctx, args)` 的 ctx 包含：

| 字段 | 类型 | 说明 |
|------|------|------|
| `region` | string | 当前 Region ID（来自 profile 或 --region） |
| `output_format` | string | 输出格式（当前固定 "json"） |
| `language` | string | 语言设置 |
| `plugin_dir` | string | 插件目录路径 |
| `api_version` | string | 当前 API 版本 |
| `product_code` | string | 产品代码（如 "Ecs"） |

## 全局 CLI 标志

主程序解析并转发给 starlark 引擎的全局标志：

| 标志 | 说明 |
|------|------|
| `--cli-dry-run` | 模拟运行模式，打印请求详情但不发送 |
| `--region <id>` | 覆盖服务地域 |
| `--endpoint <url>` | 覆盖服务端点 |
| `--cli-query <expr>` | JMESPath 表达式过滤输出 |
| `--quiet` / `-q` | 静默模式 |
| `--pager` / `--all-pages` | 分页聚合 |
| `--log-level` | 日志级别 |

## 共享模块 (load 机制)

### @shared/ 前缀

使用 `load("@shared/helpers.star", "i18n", "param")` 加载共享模块。
`@shared/` 路径解析为插件根目录的 `_shared/` 文件夹。

### _shared/helpers.star

预定义的辅助函数：

```python
def i18n(en, zh=""):
    """创建双语文本字典"""
    return {"en": en, "zh": zh if zh else en}

def param(name, type="string", required=False, api_name="", position="query",
          description=None, default=None, example="", fields=None, element=None):
    """参数定义辅助函数"""
    return {
        "name": name,
        "type": type,
        "required": required,
        "api_name": api_name,
        "position": position,
        "description": description or i18n("", ""),
        "default": default,
        "example": example,
        "fields": fields,
        "element": element,
    }
```

## 端点解析逻辑

1. 检查 `request.endpoint_override`（来自 build_request 或 --endpoint）
2. 检查 `endpoints.star` 中的 regional 端点（按 region 匹配）
3. 检查 `endpoints.star` 中的 global 端点
4. 留空（SDK 自行解析）

## 完整示例

### 简单 RPC 命令

```python
# ecs/apis/2014-05-26/describe_regions.star
load("@shared/helpers.star", "i18n", "param")

def command():
    return {
        "name": "describe-regions",
        "style": "RPC",
        "description": i18n(
            "Queries regions based on parameters such as the billing method and resource type",
            "根据计费方式、资源类型等参数查询地域信息列表",
        ),
        "params": [
            param("accept-language", type="string", api_name="AcceptLanguage",
                  position="query",
                  description=i18n("Response language", "返回结果语言")),
        ],
    }

def build_request(ctx, args):
    query = {}
    if args.get("accept-language"):
        query["AcceptLanguage"] = args["accept-language"]
    return {
        "method": "POST",
        "action": "DescribeRegions",
        "query": query,
    }
```

### ROA 命令

```python
# openapiexplorer/apis/2024-11-30/list_products.star
load("@shared/helpers.star", "i18n", "param")

def command():
    return {
        "name": "list-products",
        "style": "ROA",
        "description": i18n("Lists all products", "查询所有产品"),
        "params": [
            param("filter"),
        ],
    }

def build_request(ctx, args):
    query = {}
    if args.get("filter"):
        query["filter"] = args["filter"]
    return {
        "method": "GET",
        "url": "/listProducts",
        "action": "ListProducts",
        "query": query if query else None,
    }
```

### 多步骤 run() 模式

```python
# openapiexplorer/apis/2024-11-30/init_mcp_core.star
load("@shared/helpers.star", "i18n", "param")

def command():
    return {
        "name": "init-mcp-core",
        "style": "ROA",
        "description": i18n("Initialize MCP Core", "初始化 MCP Core"),
        "params": [],
    }

def run(ctx, args):
    # Step 1: Check if already initialized
    resp = host.call_api({
        "method": "GET",
        "url": "/listApiMcpServerCores",
        "action": "ListApiMcpServerCores",
        "query": {"PageSize": "1"},
    })
    
    cores = resp.get("Data", {}).get("Items", [])
    if len(cores) > 0:
        host.printf("MCP Core already initialized: %s\n", cores[0]["Id"])
        return None
    
    # Step 2: Create if not exists
    resp = host.call_api({
        "method": "POST",
        "url": "/createApiMcpServerCore",
        "action": "CreateApiMcpServerCore",
        "body": {"Name": "default"},
        "body_type": "json",
    })
    
    host.printf("MCP Core created: %s\n", resp.get("Data", {}).get("Id", ""))
    return None
```
