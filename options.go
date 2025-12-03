package openaigolb

import (
	"time"

	"github.com/sony/gobreaker/v2"
)

type LBOption func(*lbOptions)

type lbOptions struct {
	cbSettings gobreaker.Settings
}

// defaultCBSettings default settings for circuit breaker
var defaultCBSettings = gobreaker.Settings{
	Name:    "OpenAI-LB",
	Timeout: 30 * time.Second,
	ReadyToTrip: func(counts gobreaker.Counts) bool {
		return counts.ConsecutiveFailures >= 3
	},
}

// WithCBSettings allows customization of the circuit breaker settings.
func WithCBSettings(settings gobreaker.Settings) LBOption {
	return func(o *lbOptions) {
		o.cbSettings = settings
	}
}
