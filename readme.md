
# OpenAI Go Load Balancer

[简体中文](README_zh.md) | English

A load balancer for the OpenAI Go client that provides failover and round-robin capabilities. This library is built on top of `github.com/openai/openai-go/v3` and is designed as a drop-in replacement for the standard OpenAI Go client, allowing you to seamlessly switch to a more resilient and scalable solution.

## Features

- **Drop-in Replacement**: Easily replace the standard `openai.Client` with `openailb.Client` without changing your existing code.
- **Round-Robin Load Balancing**: Distributes requests evenly across multiple OpenAI API keys.
- **Automatic Failover**: Uses a circuit breaker to detect and bypass unhealthy nodes, ensuring high availability.
- **Customizable Circuit Breaker**: Tune the circuit breaker settings to match your specific needs.
- **Model Mapping**: Route requests to different models based on the client.

## Installation

```bash
go get github.com/hi2code/openai-go-lb
```

## Usage

### Basic Usage

Create a new load balancer client with your OpenAI API keys and start making requests.

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
	// 1. Initialize your client configurations
	configs := []openailb.OpenaiClientConfig{
		{APIKey: "YOUR_API_KEY_1",BaseURL:"https://api.openai.com/v1"},
		{APIKey: "YOUR_API_KEY_2",BaseURL:"https://openrouter.ai/api/v1"},
		// Add more clients as needed
	}

	// 2. Create a new load balancer client
	client := openailb.NewClient(configs)

	// 3. Make requests as you would with the standard OpenAI client
	params := openai.ChatCompletionNewParams{
		Model: "gpt-3.5-turbo",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Hello!"),
		},
	}

	resp, err := client.Chat.Completions.New(context.Background(), params)
	if err != nil {
		log.Fatalf("ChatCompletion error: %v\n", err)
	}

	fmt.Println(resp.Choices[0].Message.Content)
}
```

### Customizing the Circuit Breaker

You can customize the circuit breaker settings to control how the load balancer handles failures.

```go
package main

import (
	"context"
	"fmt"

	"github.com/hi2code/openai-go-lb"
	"github.com/openai/openai-go/v3"
	"github.com/sony/gobreaker/v2"
	"time"
)

func main() {
	configs := []openailb.OpenaiClientConfig{
		{APIKey: "YOUR_API_KEY_1",BaseURL:"https://api.openai.com/v1"},
		{APIKey: "YOUR_API_KEY_2",BaseURL:"https://openrouter.ai/api/v1"},
	}

	// Define custom circuit breaker settings
	customSettings := gobreaker.Settings{
		Name:    "Custom-Breaker",
		Timeout: 5 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// Trip the breaker after just 1 consecutive failure
			return counts.ConsecutiveFailures >= 1
		},
	}

	// Create a new client with the custom settings
	client := openailb.NewClient(configs, openailb.WithCBSettings(customSettings))

	// ... make requests
}
```

### Model Mapping

Route requests to different models based on the client.

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
			APIKey: "YOUR_API_KEY_1",
			ModelMap: map[string]string{
				"gpt-3.5-turbo": "gpt-4",
			},
			BaseURL:"https://api.openai.com/v1"
		},
		{APIKey: "YOUR_API_KEY_2",BaseURL:"https://openrouter.ai/api/v1"},
	}

	client := openailb.NewClient(configs)

	// This request will be routed to "gpt-4" when using the first client.
	params := openai.ChatCompletionNewParams{
		Model: "gpt-3.5-turbo",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Hello!"),
		},
	}

	// ... make requests
}
```
