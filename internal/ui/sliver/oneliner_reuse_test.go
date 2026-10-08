package sliver

import "testing"

// Every input that changes the stage or the command has to change the key, or a
// request that differs from a cached one is answered with the wrong stage -- a
// command that fetches an implant calling back somewhere else, which looks fine
// on the console and fails on the target.
func TestStageFingerprintCoversEveryInputThatChangesTheStage(t *testing.T) {
	base := stageFingerprintInput{
		jobID:     7,
		port:      8888,
		platform:  OneLinerWindows,
		c2Address: "http://10.0.0.1:8888",
		stageHost: "10.0.0.1",
		stagePath: "/stage-windows.woff",
		name:      "stage-windows-7",
		delivery:  WebDeliveryPSH,
	}

	if stageFingerprint(base) != stageFingerprint(base) {
		t.Fatal("the fingerprint is not stable for identical input; nothing would ever be reused")
	}

	mutations := map[string]func(*stageFingerprintInput){
		"job id":     func(in *stageFingerprintInput) { in.jobID = 8 },
		"port":       func(in *stageFingerprintInput) { in.port = 8889 },
		"platform":   func(in *stageFingerprintInput) { in.platform = OneLinerLinux },
		"c2 address": func(in *stageFingerprintInput) { in.c2Address = "http://10.0.0.2:8888" },
		"stage host": func(in *stageFingerprintInput) { in.stageHost = "10.0.0.2" },
		"stage path": func(in *stageFingerprintInput) { in.stagePath = "/stage-linux.woff" },
		"name":       func(in *stageFingerprintInput) { in.name = "stage-windows-8" },
		"delivery":   func(in *stageFingerprintInput) { in.delivery = WebDeliveryCertutil },
		"obfuscate":  func(in *stageFingerprintInput) { in.obfuscate = true },
		"evasion":    func(in *stageFingerprintInput) { in.evasion = true },
	}
	want := stageFingerprint(base)
	for label, mutate := range mutations {
		in := base
		mutate(&in)
		if got := stageFingerprint(in); got == want {
			t.Errorf("changing the %s did not change the key; a stale stage would be reused", label)
		}
	}

	// The separator must not be a character an operator can put in a host or a
	// name, or two different stages could join into the same key.
	sepName := stageFingerprintInput{jobID: 1, name: "a\x1fb"}
	shortName := stageFingerprintInput{jobID: 1, name: "a"}
	if stageFingerprint(sepName) == stageFingerprint(shortName) {
		t.Error("a name containing the separator collides with a shorter name")
	}
}

func TestCachedStageReturnsAReusedCopy(t *testing.T) {
	c := &Client{}
	const key = "stage"

	if _, ok := c.cachedStage(key); ok {
		t.Fatal("an empty cache reported a hit")
	}

	stored := &OneLinerResult{Command: "first", Platform: "windows"}
	c.rememberStage(key, stored)

	got, ok := c.cachedStage(key)
	if !ok {
		t.Fatal("the stage that was just remembered was not found")
	}
	if !got.Reused {
		t.Error("a cached stage is not marked as reused")
	}
	if got.Command != "first" {
		t.Errorf("command = %q, want %q", got.Command, "first")
	}
	if stored.Reused {
		t.Error("rememberStage mutated the caller's result")
	}

	// A rebuild replaces the entry rather than adding a second one.
	c.rememberStage(key, &OneLinerResult{Command: "second"})
	got, _ = c.cachedStage(key)
	if got.Command != "second" {
		t.Errorf("command = %q after a rebuild, want %q", got.Command, "second")
	}
	if got.Reused != true {
		t.Error("a rebuilt entry is not marked as reused when read back")
	}
}

// A per-request view shares the console-wide cache, so the listener staged in
// one request is reused by the next one instead of being rebuilt.
func TestStageCacheIsSharedWithRequestViews(t *testing.T) {
	root := &Client{}
	view := &Client{root: root}

	view.rememberStage("k", &OneLinerResult{Command: "c"})
	if _, ok := root.cachedStage("k"); !ok {
		t.Fatal("the view stored the stage on itself instead of the console-wide client")
	}
	if _, ok := view.cachedStage("k"); !ok {
		t.Fatal("the view cannot read the console-wide cache")
	}
}
