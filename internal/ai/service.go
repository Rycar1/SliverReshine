package ai

import (
	"context"
	"sync"
)

// Service is the process-wide AI access point.
//
// Features ask the service for a Provider rather than building one, so the
// deployment is configured once -- from the settings file and the environment --
// and every feature, present and future, sees the same endpoint, model and key.
// A new feature therefore needs no configuration of its own and no restart to
// pick up a change.
//
// It is safe for concurrent use. The console serves requests from many
// goroutines, and Configure may run while a request is in flight; readers take a
// snapshot of the current provider, so a request in progress keeps the endpoint
// it started with rather than seeing a half-applied change.
type Service struct {
	mu     sync.RWMutex
	cfg    Config
	client *Client
}

// NewService returns an unconfigured service. Every feature that needs a model
// reports "not configured" until Configure is called.
func NewService() *Service { return &Service{} }

// Configure replaces the endpoint. Passing an unconfigured Config (blank
// BaseURL or Model) disables every feature that needs a model, which is how an
// operator turns the assistant off.
func (s *Service) Configure(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	s.client = NewClient(cfg)
}

// Config returns the current configuration, key included, for callers that need
// to copy it elsewhere. Anything reaching an HTTP response should use Info
// instead: the key must not be disclosed.
func (s *Service) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Configured reports whether a provider is available.
func (s *Service) Configured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client != nil && s.client.Configured()
}

// Info returns the display-safe description of the endpoint.
func (s *Service) Info() Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client == nil {
		return Info{}
	}
	return s.client.Info()
}

// Provider returns the current provider, or nil when nothing is configured.
//
// It returns the interface nil rather than a typed nil pointer, so a feature
// that checks `provider == nil` gets the answer it expects.
func (s *Service) Provider() Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client == nil || !s.client.Configured() {
		return nil
	}
	return s.client
}

// Chat sends a conversation to the current provider.
func (s *Service) Chat(ctx context.Context, messages []Message) (string, error) {
	p := s.Provider()
	if p == nil {
		return "", ErrNotConfigured
	}
	return p.Chat(ctx, messages)
}

// ChatJSON asks the current provider for JSON and decodes it into out.
func (s *Service) ChatJSON(ctx context.Context, messages []Message, out any) error {
	p := s.Provider()
	if p == nil {
		return ErrNotConfigured
	}
	return ChatJSON(ctx, p, messages, out)
}
