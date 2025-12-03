package openailb

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/sony/gobreaker/v2"
)

type LoadBalancer struct {
	clients []*SafeClient
	counter uint64
}

// GetNextClient intelligently retrieves the next available client (skipping circuit-tripped nodes).
func (lb *LoadBalancer) GetNextClient() (*SafeClient, error) {
	total := len(lb.clients)
	if total == 0 {
		return nil, errors.New("no clients configured")
	}

	// Try at most 'total' times to avoid an infinite loop when all clients are down.
	for i := 0; i < total; i++ {
		current := atomic.AddUint64(&lb.counter, 1)
		index := (current - 1) % uint64(total)
		safeClient := lb.clients[index]

		// Key: If the circuit breaker is in the StateOpen, it means the node is faulty, so skip it.
		if safeClient.CB.State() == gobreaker.StateOpen {
			continue
		}

		return safeClient, nil
	}

	return nil, errors.New("all clients are unavailable (circuit breakers open)")
}

type SafeClient struct {
	Client   *openai.Client
	CB       *gobreaker.CircuitBreaker[*openai.ChatCompletion]
	Name     string // Used for logging differentiation (e.g., the first few characters of the API key).
	ModelMap map[string]string
	BaseURL  string // Used for testing and logging.
}

// Client is the outermost layer, mimicking openai.Client.
type Client struct {
	Chat *LBChatService
}

// LBChatService mimics openai.ChatService.
type LBChatService struct {
	Completions *LBCompletionsService // This mimics client.Chat.Completions.
}

// LBCompletionsService mimics openai.ChatCompletionService.
type LBCompletionsService struct {
	lb *LoadBalancer
}

// --- 3. Initialization Function ---
type OpenaiClientConfig struct {
	APIKey   string
	BaseURL  string
	ModelMap map[string]string // Optionally specify model mapping.
}

func NewClient(configs []OpenaiClientConfig, opts ...LBOption) *Client {
	// Initialize default options
	options := lbOptions{
		cbSettings: defaultCBSettings,
	}
	for _, o := range opts {
		o(&options)
	}
	// Initialize all real clients.
	var clients []*SafeClient

	for i, cfg := range configs {
		c := openai.NewClient(
			option.WithAPIKey(cfg.APIKey),
			option.WithBaseURL(cfg.BaseURL),
		)

		// 3. Copy the configuration (Key Point)
		// We must copy the settings because we are modifying the Name.
		// Otherwise, all clients would share the same Name,
		// or the next iteration of the loop would overwrite the previous Name.
		currentSt := options.cbSettings
		currentSt.Name = fmt.Sprintf("Client-%d", i)

		// If the user has defined custom settings but has not set ReadyToTrip,
		// we need to provide a fallback to prevent gobreaker from panicking or not working correctly.
		if currentSt.ReadyToTrip == nil {
			currentSt.ReadyToTrip = defaultCBSettings.ReadyToTrip
		}

		// Create the circuit breaker.
		cb := gobreaker.NewCircuitBreaker[*openai.ChatCompletion](currentSt)

		clients = append(clients, &SafeClient{
			Client:   &c,
			CB:       cb,
			Name:     currentSt.Name,
			ModelMap: cfg.ModelMap,
			BaseURL:  cfg.BaseURL,
		})
	}

	lb := &LoadBalancer{clients: clients}

	completionsSvc := &LBCompletionsService{lb: lb}
	chatSvc := &LBChatService{Completions: completionsSvc}

	return &Client{
		Chat: chatSvc,
	}
}

func applyModelMapping(client *SafeClient, params openai.ChatCompletionNewParams) openai.ChatCompletionNewParams {
	if len(client.ModelMap) == 0 {
		return params
	}

	// Get the requested model name.
	reqModel := params.Model

	// If a mapping exists, replace the model name.
	if targetModel, ok := client.ModelMap[reqModel]; ok {
		newParams := params
		newParams.Model = targetModel
		return newParams
	}

	return params
}

// isFatalError determines whether to trip the circuit (400 errors don't, 401/429/5xx errors do).
func isFatalError(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		// 400 Bad Request is usually due to user parameter errors, not the node's fault.
		if apiErr.StatusCode == 400 {
			return false
		}
		// 401 (Auth), 429 (RateLimit), and 5xx (Server) are all considered fatal errors.
		return true
	}
	// Network errors trip the circuit by default.
	return true
}

// New implementation (integrates circuit breaker + model mapping).
func (s *LBCompletionsService) New(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) (*openai.ChatCompletion, error) {
	// A. Get a healthy node.
	safeClient, err := s.lb.GetNextClient()
	if err != nil {
		return nil, err
	}

	// B. Apply model mapping.
	finalParams := applyModelMapping(safeClient, params)

	// C. Execute the request within the circuit breaker.
	res, err := safeClient.CB.Execute(func() (*openai.ChatCompletion, error) {
		resp, reqErr := safeClient.Client.Chat.Completions.New(ctx, finalParams, opts...)

		if reqErr != nil {
			// If it's a fatal error, return the error to trigger the circuit breaker.
			if isFatalError(reqErr) {
				return nil, reqErr
			}
			// If it's a non-fatal error (like a 400), return (nil, nil) to ignore it.
			// (nil is a valid value for the *openai.ChatCompletion pointer type).
			return nil, nil
		}
		return resp, nil
	})

	// Handle errors returned by the circuit breaker.
	if err != nil {
		return nil, err
	}

	// Handle the "non-fatal error" case (where res is nil and err is nil).
	// This means a 400 error occurred, which the circuit breaker ignored,
	// but we need to return the error to the user.
	if res == nil {
		// Re-run the request directly to get the original error (since it was ignored).
		return safeClient.Client.Chat.Completions.New(ctx, finalParams, opts...)
	}

	return res, nil
}

// NewStreaming implementation (integrates status checking + model mapping).
func (s *LBCompletionsService) NewStreaming(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) *ssestream.Stream[openai.ChatCompletionChunk] {
	// A. Get a node.
	safeClient, err := s.lb.GetNextClient()
	if err != nil {
		// The streaming method signature cannot return an error. In a real scenario,
		// it's recommended to modify the return signature or panic.
		// For demonstration purposes, we can only return nil or an empty stream here.
		return nil
	}

	// B. Manually check the circuit breaker status (streams are hard to wrap with Execute).
	if safeClient.CB.State() == gobreaker.StateOpen {
		// If the current node's circuit is open, recursively try the next one.
		return s.NewStreaming(ctx, params, opts...)
	}

	// C. Apply model mapping.
	finalParams := applyModelMapping(safeClient, params)

	// D. Execute the request.
	return safeClient.Client.Chat.Completions.NewStreaming(ctx, finalParams, opts...)
}
