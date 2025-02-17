package newmeta

import (
	"encoding/json"
	"strings"

	aliyunopenapimeta "github.com/aliyun/aliyun-cli/aliyun-openapi-meta"
)

type ProductSet struct {
	Products []Product `json:"products"`
}

type Product struct {
	Code         string              `json:"code"`
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	EndpointType string              `json:"endpointType"`
	Endpoints    map[string]Endpoint `json:"endpoints"`
}

type Endpoint struct {
	RegionId string `json:"regionId"`
	Name     string `json:"regionName"`
	AreaId   string `json:"areaId"`
	AreaName string `json:"areaName"`
	Public   string `json:"public"`
	VPC      string `json:"vpc"`
}

type Version struct {
	Version string         `json:"version"`
	Style   string         `json:"style"`
	APIs    map[string]API `json:"apis"`
}

type API struct {
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Deprecated bool   `json:"deprecated"`
}

type APIDetail struct {
	Name        string             `json:"name"`
	Auth        []string           `json:"security"`
	Deprecated  bool               `json:"deprecated"`
	Protocol    string             `json:"protocol"`
	Method      string             `json:"method"`
	PathPattern string             `json:"pathPattern"`
	Parameters  []RequestParameter `json:"parameters"`
}

func (api *APIDetail) IsAnonymousAPI() bool {
	for _, v := range api.Auth {
		if v == "Anonymous" {
			return true
		}
	}
	return false
}

type RequestParameter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Position    string `json:"position"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
}

func GetProductName(language, code string) (name string, err error) {
	content, err := GetMetadata(language, "/products.json")
	if err != nil {
		return
	}

	products := new(ProductSet)
	err = json.Unmarshal(content, &products)
	if err != nil {
		return
	}

	for _, p := range products.Products {
		if strings.EqualFold(p.Code, code) {
			name = strings.TrimSpace(p.Name)
			break
		}
	}

	return
}

func GetAPI(language, code, name string) (api *API, err error) {
	content, err := GetMetadata(language, "/"+strings.ToLower(code)+"/version.json")
	if err != nil {
		return
	}

	version := new(Version)
	err = json.Unmarshal(content, &version)
	if err != nil {
		return
	}

	if found, ok := version.APIs[name]; ok {
		api = &found
	}

	return
}

func GetAPIDetail(language, code, name string) (api *APIDetail, err error) {
	content, err := GetMetadata(language, "/"+strings.ToLower(code)+"/"+name+".json")
	if err != nil {
		return
	}

	detail := new(APIDetail)
	err = json.Unmarshal(content, &detail)
	if err != nil {
		return
	}

	api = detail
	return
}

func GetMetadataPrefix(language string) string {
	if language == "en" {
		return "en-US"
	}
	return "zh-CN"
}

func GetMetadata(language string, path string) (content []byte, err error) {
	content, err = aliyunopenapimeta.Metadatas.ReadFile(GetMetadataPrefix(language) + path)
	return
}
