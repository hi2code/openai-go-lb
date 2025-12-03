package openailb

import (
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/sony/gobreaker/v2"
)

func TestLBRoundRobin(t *testing.T) {
	t.Parallel()

	mockOkServer1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices": [{"message": {"content": "Hello from Server 1"}}]}`))
	}))
	defer mockOkServer1.Close()
	mockOkServer2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices": [{"message": {"content": "Hello from Server 2"}}]}`))
	}))
	defer mockOkServer2.Close()

	configs := []OpenaiClientConfig{
		{APIKey: "mock-key-1", BaseURL: mockOkServer1.URL},
		{APIKey: "mock-key-2", BaseURL: mockOkServer2.URL},
	}

	client := NewLBOpenaiClient(configs)

	params := openai.ChatCompletionNewParams{
		Model: "test_model",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are an assistant. hello"),
			openai.UserMessage("test"),
		},
	}

	hitCounts := make(map[string]int)
	totalRequests := 10

	for i := 0; i < totalRequests; i++ {
		resp, err := client.Chat.Completions.New(context.Background(), params)
		if err != nil {
			t.Fatalf("Request %d failed unexpectedly: %v", i, err)
		}

		content := resp.Choices[0].Message.Content
		hitCounts[content]++
		t.Logf("Request %d got: %s", i, content)
	}

	// assert Round Robin
	expectedHits := totalRequests / 2

	if hitCounts["Hello from Server 1"] != expectedHits {
		t.Errorf("Load Balancing uneven for Server 1. Expected %d, got %d", expectedHits, hitCounts["Hello from Server 1"])
	}

	if hitCounts["Hello from Server 2"] != expectedHits {
		t.Errorf("Load Balancing uneven for Server 2. Expected %d, got %d", expectedHits, hitCounts["Hello from Server 2"])
	}
}

func TestLBCircuitBreakerWithDefaultOption(t *testing.T) {
	t.Parallel()

	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-123",
			"object": "chat.completion",
			"created": 1677652288,
			"model": "gpt-3.5-turbo-0613",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "Hello"
				},
				"finish_reason": "stop"
			}]
		}`))
	}))
	defer okServer.Close()

	lbConfigs := []OpenaiClientConfig{
		{APIKey: "fail-key", BaseURL: failServer.URL},
		{APIKey: "ok-key", BaseURL: okServer.URL},
	}
	lb := NewLBOpenaiClient(lbConfigs)

	params := openai.ChatCompletionNewParams{
		Model: "any-model",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("test"),
		},
	}

	log.Println("--- Triggering circuit breaker ---")
	// Total of 3 failures needed to trigger the circuit breaker
	// Since we use round-robin, we need 5 requests to make failServer fail 3 times
	for i := 0; i < 5; i++ {
		_, err := lb.Chat.Completions.New(context.Background(), params)
		// Even-indexed requests (0, 2, 4) should hit failServer and fail
		if i%2 == 0 {
			if err == nil {
				t.Fatalf("Request %d to failServer should have failed, but it succeeded", i)
			}
			log.Printf("Request %d to failServer failed as expected", i)
		} else { // Odd-indexed requests (1, 3) should hit okServer and succeed
			if err != nil {
				t.Fatalf("Request %d to okServer should have succeeded, but it failed: %v", i, err)
			}
			log.Printf("Request %d to okServer succeeded as expected", i)
		}
	}

	//  Verify that the circuit breaker for the first Client (failServer) is open
	failClient := lb.Chat.Completions.lb.clients[0]
	if failClient.CB.State() != gobreaker.StateOpen {
		t.Fatalf("Circuit breaker for failClient should be open, but it's %s", failClient.CB.State().String())
	}
	log.Println("breaker for failClient")

	//  Next request should automatically skip failServer and go to okServer
	resp, err := lb.Chat.Completions.New(context.Background(), params)
	if err != nil {
		t.Fatalf("Next request failed, but it should have been routed to okServer and succeeded: %v", err)
	}
	if resp.Choices[0].Message.Content != "Hello" {
		t.Fatalf("Expected response 'Hello', but got '%s'", resp.Choices[0].Message.Content)
	}
	log.Println("switch health server, content:", resp.Choices[0].Message.Content)
}

func TestLBCustomOptions(t *testing.T) {
	t.Parallel()

	// 1. Setup Mock Servers
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	// 2. Setup Config
	// We only need one failing server to test the breaker sensitivity
	configs := []OpenaiClientConfig{
		{APIKey: "fail-key", BaseURL: failServer.URL},
	}

	// 3. Define Custom Settings: Fail Fast (Trip after 1 failure)
	customSettings := gobreaker.Settings{
		Name:    "Custom-Breaker",
		Timeout: 5 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// CUSTOM LOGIC: Trip immediately after 1 failure
			return counts.ConsecutiveFailures >= 1
		},
	}

	// 4. Initialize LB with the Option
	client := NewLBOpenaiClient(configs, WithCBSettings(customSettings))

	params := openai.ChatCompletionNewParams{
		Model: "test_model",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("test"),
		},
	}

	// 5. Trigger the failure ONCE
	_, err := client.Chat.Completions.New(context.Background(), params)
	if err == nil {
		t.Fatal("Expected an error from the failing server, but got nil")
	}

	// 6. Assert: The breaker should be OPEN immediately
	// Access the internal client to check state (assuming you have access to internal fields for testing)

	internalClient := client.Chat.Completions.lb.clients[0]
	currentState := internalClient.CB.State()

	if currentState != gobreaker.StateOpen {
		t.Errorf("Expected Circuit Breaker to be OPEN after 1 failure (Custom Option), but it is %s. Default settings might be active.", currentState.String())
	} else {
		t.Log("Success: Circuit Breaker tripped after just 1 failure as configured.")
	}
}
