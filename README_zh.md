# OpenAI Go 负载均衡器

一个为 OpenAI Go 客户端设计的负载均衡器，提供故障转移和轮询功能。该库基于 `github.com/openai/openai-go/v3` 构建，旨在作为标准 OpenAI Go 客户端的无缝替代品，让您可以轻松切换到更具弹性和可扩展性的解决方案。

## 功能特性

- **无缝替换**: 无需更改现有代码，即可轻松将标准 `openai.Client` 替换为 `openailb.Client`。
- **轮询负载均衡**: 将请求均匀地分配到多个 OpenAI API 密钥。
- **自动故障转移**: 使用断路器来检测和绕过不健康的节点，确保高可用性。
- **可定制的断路器**: 调整断路器设置以满足您的特定需求。
- **模型映射**: 根据客户端将请求路由到不同的模型。

## 安装

```bash
go get github.com/hi2code/openai-go-lb
```

## 使用方法

### 基本用法

使用您的 OpenAI API 密钥创建一个新的负载均衡器客户端，然后开始发出请求。

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/hi2code/openai-go-lb"
	"github.com/openai/openai-go/v3"
)

func main() {
	// 1. 初始化您的客户端配置
	configs := []openailb.OpenaiClientConfig{
		{APIKey: "您的_API_密钥_1",BaseURL:"https://api.openai.com/v1"},
		{APIKey: "您的_API_密钥_2",BaseURL:"https://openrouter.ai/api/v1"},
		// 根据需要添加更多客户端
	}

	// 2. 创建一个新的负载均衡器客户端
	client := openailb.NewClient(configs)

	// 3. 像使用标准 OpenAI 客户端一样发出请求
	params := openai.ChatCompletionNewParams{
		Model: "gpt-3.5-turbo",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("你好!"),
		},
	}

	resp, err := client.Chat.Completions.New(context.Background(), params)
	if err != nil {
		log.Fatalf("ChatCompletion 错误: %v\n", err)
	}

	fmt.Println(resp.Choices[0].Message.Content)
}
```

### 自定义断路器

您可以自定义断路器设置，以控制负载均衡器处理故障的方式。

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/hi2code/openai-go-lb"
	"github.com/openai/openai-go/v3"
	"github.com/sony/gobreaker/v2"
)

func main() {
	configs := []openailb.OpenaiClientConfig{
		{APIKey: "您的_API_密钥_1",BaseURL:"https://api.openai.com/v1"},
		{APIKey: "您的_API_密钥_2",BaseURL:"https://openrouter.ai/api/v1"},
	}

	// 定义自定义断路器设置
	customSettings := gobreaker.Settings{
		Name:    "Custom-Breaker",
		Timeout: 5 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// 仅在 1 次连续失败后就触发断路器
			return counts.ConsecutiveFailures >= 1
		},
	}

	// 使用自定义设置创建一个新的客户端
	client := openailb.NewClient(configs, openailb.WithCBSettings(customSettings))

	// ... 发出请求
}
```

### 模型映射

根据客户端将请求路由到不同的模型。

```go
package main

import (
	"context"
	"fmt"

	"github.com/hi2code/openai-go-lb"
	"github.com/openai/openai-go/v3"
)

func main() {
	configs := []openailb.OpenaiClientConfig{
		{
			APIKey: "您的_API_密钥_1",
			ModelMap: map[string]string{
				"gpt-3.5-turbo": "gpt-4",
			},
			BaseURL:"https://openrouter.ai/api/v1"
		},
		{APIKey: "您的_API_密钥_2",BaseURL:"https://api.openai.com/v1"},
	}

	client := openailb.NewClient(configs)

	// 当使用第一个客户端时，此请求将被路由到 "gpt-4"
	params := openai.ChatCompletionNewParams{
		Model: "gpt-3.5-turbo",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("你好!"),
		},
	}

	// ... 发出请求
}
```