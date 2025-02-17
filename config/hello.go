// Copyright (c) 2009-present, Alibaba Cloud All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package config

import (
	"fmt"
	"os"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/aliyun/aliyun-cli/cli"
)

func doHello(ctx *cli.Context, profile *Profile) (err error) {
	profile.OverwriteWithFlags(ctx)
	credential, err := profile.GetCredential(ctx, nil)
	if err != nil {
		return
	}

	config := &openapi.Config{
		Credential: credential,
	}

	config.Endpoint = tea.String(getSTSEndpoint(profile.StsRegion))
	client, err := openapi.NewClient(config)
	if err != nil {
		return
	}

	params := &openapi.Params{
		// 接口名称
		Action: tea.String("GetCallerIdentity"),
		// 接口版本
		Version: tea.String("2015-04-01"),
		// 接口协议
		Protocol: tea.String("HTTPS"),
		// 接口 HTTP 方法
		Method:   tea.String("POST"),
		AuthType: tea.String("AK"),
		Style:    tea.String("RPC"),
		// 接口 PATH
		Pathname: tea.String("/"),
		// 接口请求体内容格式
		ReqBodyType: tea.String("json"),
		// 接口响应体内容格式
		BodyType: tea.String("json"),
	}
	// runtime options
	runtime := &util.RuntimeOptions{}
	request := &openapi.OpenApiRequest{}

	ua := USER_AGENT + "/" + cli.GetVersion()
	if vendorEnv, ok := os.LookupEnv(ENV_SUFFIX + "_CLOUD_VENDOR"); ok {
		ua += " vendor/" + vendorEnv
	}

	client.UserAgent = tea.String(ua)
	_, err = client.CallApi(params, request, runtime)
	return
}

func DoHello(ctx *cli.Context, profile *Profile) {
	w := ctx.Stdout()
	err := doHello(ctx, profile)
	if err != nil {
		cli.Println(w, "-----------------------------------------------")
		cli.Println(w, "!!! Configure Failed please configure again !!!")
		cli.Println(w, "-----------------------------------------------")
		cli.Println(w, err)
		cli.Println(w, "-----------------------------------------------")
		cli.Println(w, "!!! Configure Failed please configure again !!!")
		cli.Println(w, "-----------------------------------------------")
		return
	}

	fmt.Println(icon)
}

var icon = string(`
Configure Done!!!`)
