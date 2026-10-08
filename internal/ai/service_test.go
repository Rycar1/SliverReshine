package ai

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestServiceUnconfigured(t *testing.T) {
	s := NewService()
	if s.Configured() {
		t.Error("a new service reports Configured() = true")
	}
	if p := s.Provider(); p != nil {
		t.Errorf("Provider() = %v, want nil when unconfigured", p)
	}
	if info := s.Info(); info != (Info{}) {
		t.Errorf("Info() = %+v, want the zero value", info)
	}
	if _, err := s.Chat(context.Background(), []Message{UserMessage("hi")}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Chat = %v, want ErrNotConfigured", err)
	}
	var out map[string]any
	if err := s.ChatJSON(context.Background(), []Message{UserMessage("hi")}, &out); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("ChatJSON = %v, want ErrNotConfigured", err)
	}
}

func TestServiceConfigureAndDisable(t *testing.T) {
	s := NewService()
	s.Configure(Config{BaseURL: "http://x/v1", Model: "m", APIKey: "k"})
	if !s.Configured() {
		t.Fatal("Configure with a full config left the service unconfigured")
	}
	if s.Provider() == nil {
		t.Fatal("Provider() = nil after Configure")
	}
	info := s.Info()
	if info.BaseURL != "http://x/v1" || info.Model != "m" || !info.HasKey {
		t.Fatalf("Info() = %+v, want the configured endpoint", info)
	}

	// An empty config is how an operator turns the assistant off.
	s.Configure(Config{})
	if s.Configured() {
		t.Error("Configure with an empty config left the service configured")
	}
	if s.Provider() != nil {
		t.Error("Provider() != nil after the service was disabled")
	}
}

// The console serves requests from many goroutines and may reload settings
// while a request is in flight, so Configure and the readers must not race.
func TestServiceConcurrentConfigureAndRead(t *testing.T) {
	s := NewService()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if (i+j)%2 == 0 {
					s.Configure(Config{BaseURL: "http://x/v1", Model: "m"})
				} else {
					s.Configure(Config{})
				}
				_ = s.Configured()
				_ = s.Info()
				_ = s.Provider()
			}
		}(i)
	}
	wg.Wait()
}
