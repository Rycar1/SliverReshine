package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"sliverreshine/internal/ai"
	"sliverreshine/internal/config"
	"sliverreshine/internal/ui/sliver"
)

// The HTTP surface of the model-backed assistant: whether it is configured,
// where its endpoint is configured from, and a way to run it against a session.

// handleAIStatus reports whether the assistant is available and what it may do.
//
// It never returns the API key. It reports hasKey instead, which is enough for
// the UI to tell an operator that a key is missing without the key ever
// reaching a browser or a log. readOnly is the live policy -- the operator can
// turn it off in the settings -- and allowlist is what the policy permits, so
// the two always describe the same console state.
func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	svc := s.aiService()
	info := svc.Info()
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": svc.Configured(),
		"baseURL":    info.BaseURL,
		"model":      info.Model,
		"hasKey":     info.HasKey,
		"readOnly":   s.AIReadOnly(),
		"allowlist":  sliver.ReadOnlyAllowlist(),
	})
}

// handleAIReadOnlyCheck runs one command through the read-only policy.
//
// A refusal is a 200 with allowed=false rather than an error status: the policy
// working is a successful check, not a failed request. Answering 4xx would make
// a correct refusal look like a broken console in every client's error path.
// It reports what the policy would do even when the policy is turned off, so an
// operator can see what turning it back on would refuse.
func (s *Server) handleAIReadOnlyCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string `json:"command"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	path, args, err := sliver.CheckReadOnlyCommand(req.Command)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"allowed": false, "reason": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"allowed": true,
		"path":    path,
		"args":    args,
	})
}

// handleAIModels lists the models an OpenAI-compatible endpoint advertises.
//
// The panel can call it with an endpoint and key the operator has typed but
// not saved, so a model can be chosen before anything is written; blank fields
// fall back to the stored configuration. The stored key is used server-side and
// never leaves this process.
func (s *Server) handleAIModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL string `json:"baseURL"`
		APIKey  string `json:"apiKey"`
	}
	if !decodeBody(w, r, &req) {
		return
	}

	baseURL := strings.TrimSpace(req.BaseURL)
	apiKey := strings.TrimSpace(req.APIKey)
	if home, ok := s.settingsHomePath(); ok {
		cfg, err := config.Load(home)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "read settings: "+err.Error())
			return
		}
		if baseURL == "" {
			baseURL = cfg.AI.BaseURL
		}
		if apiKey == "" {
			apiKey = cfg.AI.ResolveKey()
		}
	}
	if baseURL == "" {
		writeErr(w, http.StatusNotImplemented,
			"no AI endpoint is configured; enter a base URL first")
		return
	}

	// The listing is a third-party call, so it gets its own bound rather than
	// inheriting the operator's chat timeout, which can be minutes long.
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	models, err := ai.ListModels(ctx, ai.Config{BaseURL: baseURL, APIKey: apiKey})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "list models: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"baseURL": baseURL,
		"models":  models,
	})
}

// aiSettingsView is what the AI configuration panel renders. The key is never
// echoed back: the panel needs to know only whether one is set, and a value in
// the response would end up in browser history, logs and screenshots for no
// benefit.
type aiSettingsView struct {
	BaseURL        string `json:"baseURL"`
	Model          string `json:"model"`
	APIKeyEnv      string `json:"apiKeyEnv"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	ReadOnly       bool   `json:"readOnly"`
	Thinking       bool   `json:"thinking"`
	HasKey         bool   `json:"hasKey"`
	Managed        bool   `json:"managed"`
}

type aiSettingsChangeRequest struct {
	// BaseURL, Model and APIKeyEnv are pointers for the same reason as the
	// booleans below: the read-only toggle lives on its own control and sends
	// only {readOnly}. With plain strings those requests decoded the absent
	// fields as "" and blanked the model configuration the operator had just
	// saved. nil leaves the stored value alone; an explicit empty string still
	// clears it.
	BaseURL        *string `json:"baseURL"`
	Model          *string `json:"model"`
	APIKey         string  `json:"apiKey"`
	APIKeyEnv      *string `json:"apiKeyEnv"`
	TimeoutSeconds int     `json:"timeoutSeconds"`
	// ReadOnly and Thinking are pointers so a request that only changes one
	// setting does not have to restate the others. nil leaves the stored value
	// alone.
	ReadOnly *bool `json:"readOnly"`
	Thinking *bool `json:"thinking"`
}

// handleAISettingsGet reports the stored model configuration.
func (s *Server) handleAISettingsGet(w http.ResponseWriter, r *http.Request) {
	home, ok := s.settingsHomePath()
	if !ok {
		writeErr(w, http.StatusNotImplemented,
			"this console does not manage a settings file; configure the model with the settings file or the environment")
		return
	}
	cfg, err := config.Load(home)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read settings: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, aiSettingsView{
		BaseURL:        cfg.AI.BaseURL,
		Model:          cfg.AI.Model,
		APIKeyEnv:      cfg.AI.APIKeyEnv,
		TimeoutSeconds: cfg.AI.TimeoutSeconds,
		ReadOnly:       cfg.AI.ReadOnly,
		Thinking:       cfg.AI.Thinking,
		HasKey:         cfg.AI.ResolveKey() != "",
		Managed:        true,
	})
}

// handleAISettingsPut changes the model endpoint, model and key at runtime.
//
// The change is written through to the settings file and applied to the live
// service, so the endpoint the operator just chose is the one the next request
// and the next restart both use. An empty apiKey leaves the stored key in
// place: the panel never shows the key, so it must be able to change the model
// without the operator having to re-type a secret they cannot read back.
func (s *Server) handleAISettingsPut(w http.ResponseWriter, r *http.Request) {
	home, ok := s.settingsHomePath()
	if !ok {
		writeErr(w, http.StatusNotImplemented,
			"this console does not manage a settings file; configure the model with the settings file or the environment")
		return
	}

	var req aiSettingsChangeRequest
	if !decodeBody(w, r, &req) {
		return
	}

	cfg, err := config.Load(home)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read settings: "+err.Error())
		return
	}

	if req.BaseURL != nil {
		cfg.AI.BaseURL = strings.TrimSpace(*req.BaseURL)
	}
	if req.Model != nil {
		cfg.AI.Model = strings.TrimSpace(*req.Model)
	}
	if req.APIKeyEnv != nil {
		cfg.AI.APIKeyEnv = strings.TrimSpace(*req.APIKeyEnv)
	}
	if req.TimeoutSeconds > 0 {
		cfg.AI.TimeoutSeconds = req.TimeoutSeconds
	}
	if key := strings.TrimSpace(req.APIKey); key != "" {
		cfg.AI.APIKey = key
	}
	if req.ReadOnly != nil {
		cfg.AI.ReadOnly = *req.ReadOnly
	}
	if req.Thinking != nil {
		cfg.AI.Thinking = *req.Thinking
	}

	if err := config.Save(home, cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, "save settings: "+err.Error())
		return
	}

	// Apply to the running service so the change takes effect without a
	// restart, exactly as it would after one.
	s.SetAIConfig(ai.Config{
		BaseURL:  cfg.AI.BaseURL,
		Model:    cfg.AI.Model,
		APIKey:   cfg.AI.ResolveKey(),
		Timeout:  cfg.AI.Timeout(),
		Thinking: cfg.AI.Thinking,
	})
	s.SetAIReadOnly(cfg.AI.ReadOnly)

	writeJSON(w, http.StatusOK, aiSettingsView{
		BaseURL:        cfg.AI.BaseURL,
		Model:          cfg.AI.Model,
		APIKeyEnv:      cfg.AI.APIKeyEnv,
		TimeoutSeconds: cfg.AI.TimeoutSeconds,
		ReadOnly:       cfg.AI.ReadOnly,
		Thinking:       cfg.AI.Thinking,
		HasKey:         cfg.AI.ResolveKey() != "",
		Managed:        true,
	})
}

// handleAICollect runs one collection pass over a session.
//
// The session ID comes from the path rather than the body, so a body cannot
// point the run at a different target than the one the operator selected. A
// deployment with no model configured answers 501: the request was understood
// and simply cannot be served here, which is not the same as a console fault.
func (s *Server) handleAICollect(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req sliver.AICollectRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.SessionID = id
	// The policy is a deployment setting, so the body cannot turn it off: the
	// handler stamps the resolved value onto the request before the collector
	// sees it.
	readOnly := s.AIReadOnly()
	req.ReadOnly = &readOnly

	provider := s.aiService().Provider()
	if provider == nil {
		writeErr(w, http.StatusNotImplemented, ai.ErrNotConfigured.Error())
		return
	}

	// The request context travels into the collector, so a browser that
	// navigates away stops the run instead of leaving it to spend model calls
	// nobody is reading.
	if !wantsEventStream(r) {
		result, err := c.AICollect(r.Context(), provider, req)
		writeResult(w, result, err)
		return
	}

	// A streaming caller gets the same run with the model's reasoning and each
	// command published as they happen, so the operator can watch the run
	// instead of a spinner. The result still arrives, as the stream's last
	// event, so a client that reads to the end has everything the JSON
	// response carried.
	stream, err := newEventStream(w)
	if err != nil {
		writeErr(w, http.StatusNotImplemented, err.Error())
		return
	}
	req.Progress = stream.send
	result, err := c.AICollect(r.Context(), provider, req)
	if err != nil {
		stream.sendError(err)
		return
	}
	stream.sendResult(result)
}
