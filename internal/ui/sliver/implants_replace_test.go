package sliver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"google.golang.org/grpc"
)

// Rebuilding under a name that already exists used to delete the previous build
// before generating the new one. The operator reported the symptom directly --
// "为什么我重新构建一个载荷先前的就消失了" (why did the previous one disappear
// when I rebuilt the payload) -- and it is exactly what the ordering causes: a
// build that fails for any reason (garble, a missing cross-compiler, a bad
// setting) leaves no build at all, because the previous one was removed before
// anything had shown the new one could be produced.
//
// The fix moves the delete behind a successful compile. The server makes that
// possible by compiling *before* it writes the build row, so a Generate under a
// taken name reaches the UNIQUE constraint with the payload already compiled --
// which is the signal the replacement is built on.
//
// These tests pin what that ordering has to guarantee, against a stub that
// models the server's real behaviour (including the unique name) rather than
// the network:
//
//  1. a build that fails does NOT remove the previous build;
//  2. a build that succeeds reports that it replaced one;
//  3. no delete ever precedes the compile that authorises it -- the defect
//     itself, and the thing a future refactor is most likely to undo.

// replaceStub models the parts of the Sliver server that a rebuild touches.
//
// It keeps the builds in a map, so "the name is already taken" is a real
// property of the stub rather than something each test has to assert. The
// embedded rpcStub leaves every other RPC a nil interface, so a call this test
// does not expect panics instead of returning a zero value.
type replaceStub struct {
	rpcStub

	mu sync.Mutex

	// builds is the server's implant_builds table, keyed by name. The name is
	// the unique key, which is why a second row under the same name is refused.
	builds map[string]*clientpb.ImplantConfig

	// events is one interleaved log of what the console asked the server to do,
	// in the order it asked. A single log rather than one per RPC: the ordering
	// between a delete and a build is the whole subject of the fix, and it
	// cannot be recovered from two separate logs.
	events []string

	// generateCount is how many Generate calls have been made, which is what
	// failGenerate keys on.
	generateCount int

	// failGenerate, when set, fails that attempt with the returned error. The
	// attempt number is 1-based, so a test can make the first Generate succeed
	// and the second fail, or the reverse.
	failGenerate func(attempt int, name string) error

	// implantBuildsErr makes the list leg of the console's work fail.
	implantBuildsErr error

	// deleteErr makes the delete leg of the console's work fail.
	deleteErr error
}

func (s *replaceStub) Generate(_ context.Context, in *clientpb.GenerateReq, _ ...grpc.CallOption) (*clientpb.Generate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.generateCount++
	s.events = append(s.events, "generate:"+in.Name)

	if s.failGenerate != nil {
		if err := s.failGenerate(s.generateCount, in.Name); err != nil {
			return nil, err
		}
	}

	// The server writes the build row after the compiler has run, so the
	// duplicate is reported last -- with the payload already built.
	if _, exists := s.builds[in.Name]; exists {
		return nil, errors.New("UNIQUE constraint failed: implant_builds.name")
	}
	s.builds[in.Name] = in.Config

	return &clientpb.Generate{
		File:           &commonpb.File{Name: in.Name, Data: []byte("MZ-fake-payload")},
		ImplantName:    in.Name,
		ImplantBuildID: "build-id",
	}, nil
}

func (s *replaceStub) DeleteImplantBuild(_ context.Context, in *clientpb.DeleteReq, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(s.events, "delete:"+in.Name)
	if s.deleteErr != nil {
		return nil, s.deleteErr
	}
	if _, exists := s.builds[in.Name]; !exists {
		return nil, fmt.Errorf("implant build %q not found", in.Name)
	}
	delete(s.builds, in.Name)
	return &commonpb.Empty{}, nil
}

func (s *replaceStub) ImplantBuilds(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.ImplantBuilds, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.implantBuildsErr != nil {
		return nil, s.implantBuildsErr
	}
	out := &clientpb.ImplantBuilds{Configs: map[string]*clientpb.ImplantConfig{}}
	for name, cfg := range s.builds {
		out.Configs[name] = cfg
	}
	return out, nil
}

// names returns the builds currently held, for failure messages.
func (s *replaceStub) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.builds))
	for name := range s.builds {
		out = append(out, name)
	}
	return out
}

// history returns the interleaved call log, in the order the RPCs were made.
func (s *replaceStub) history() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

// count returns how many times an RPC prefix appears in the log.
func (s *replaceStub) count(prefix string) int {
	n := 0
	for _, e := range s.history() {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

// newReplaceStub returns a stub holding an existing build under name, which is
// the situation the operator was in: a profile was built once, and building it
// again has to replace that build rather than destroy it.
func newReplaceStub(name string) *replaceStub {
	return &replaceStub{
		builds: map[string]*clientpb.ImplantConfig{
			name: {GOOS: "windows", GOARCH: "amd64"},
		},
	}
}

func buildReq(name string) *GenerateRequest {
	return &GenerateRequest{Name: name, OS: "windows", Arch: "amd64", Format: "exe"}
}

// A build that fails must leave the previous build alone. This is the defect:
// the old ordering deleted first, so any failure -- here a compiler error --
// left the operator with no build at all.
func TestGenerateImplantFailedBuildKeepsThePreviousBuild(t *testing.T) {
	const name = "my-implant"
	stub := newReplaceStub(name)
	stub.failGenerate = func(int, string) error {
		return errors.New("garble: exit status 1")
	}
	c := &Client{RPC: stub}

	_, err := c.GenerateImplant(buildReq(name))
	if err == nil {
		t.Fatal("a failing build reported success")
	}
	if !strings.Contains(err.Error(), "garble") {
		t.Errorf("the build error was not surfaced: %v", err)
	}

	if n := stub.count("delete:"); n != 0 {
		t.Errorf("a failed build deleted %d build(s); the previous build must survive a failure", n)
	}
	if got := stub.names(); len(got) != 1 || got[0] != name {
		t.Errorf("builds after a failed build = %v, want the previous %q intact", got, name)
	}
}

// A build that succeeds under a taken name has replaced the previous one, and
// has to say so: the operator's complaint was precisely that a replacement was
// indistinguishable from a fresh build.
func TestGenerateImplantReplacedBuildIsReported(t *testing.T) {
	const name = "my-implant"
	stub := newReplaceStub(name)
	c := &Client{RPC: stub}

	res, err := c.GenerateImplant(buildReq(name))
	if err != nil {
		t.Fatalf("GenerateImplant: %v", err)
	}

	replaced, ok := res["replaced"]
	if !ok {
		t.Fatal("the response has no `replaced` field, so the console cannot tell the operator a build was replaced")
	}
	if replaced != true {
		t.Errorf("replaced = %v, want true: a build of this name already existed", replaced)
	}
	if res["success"] != true {
		t.Errorf("success = %v, want true", res["success"])
	}

	// The replacement is the build now stored under the name.
	if got := stub.names(); len(got) != 1 || got[0] != name {
		t.Errorf("builds after a successful replacement = %v, want just %q", got, name)
	}

	if got := stub.history(); len(got) != 3 {
		t.Errorf("history = %v, want 3 calls: attempt, delete, retry", got)
	} else {
		want := []string{"generate:" + name, "delete:" + name, "generate:" + name}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("history[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
			}
		}
	}
}

// A name that is not in use is a fresh build, and must not claim otherwise --
// otherwise every build would report a replacement and the flag would carry no
// information.
func TestGenerateImplantFreshBuildIsNotReportedAsReplaced(t *testing.T) {
	stub := &replaceStub{builds: map[string]*clientpb.ImplantConfig{}}
	c := &Client{RPC: stub}

	res, err := c.GenerateImplant(buildReq("brand-new"))
	if err != nil {
		t.Fatalf("GenerateImplant: %v", err)
	}
	if res["replaced"] != false {
		t.Errorf("replaced = %v, want false for a name that was not in use", res["replaced"])
	}
	if n := stub.count("delete:"); n != 0 {
		t.Errorf("a fresh build deleted %d build(s); nothing should have been removed", n)
	}
	if got := stub.history(); len(got) != 1 {
		t.Errorf("history = %v, want 1 call: a free name needs no retry", got)
	}
}

// The ordering itself, stated as an assertion: no delete may precede the
// compile that authorises it. This is the regression guard -- the fix is
// exactly the move of the delete from before the build to after it, and a
// future change that moves it back would satisfy every other test here while
// reintroducing the data loss.
func TestGenerateImplantNeverDeletesBeforeASuccessfulBuild(t *testing.T) {
	const name = "my-implant"
	stub := newReplaceStub(name)
	c := &Client{RPC: stub}

	if _, err := c.GenerateImplant(buildReq(name)); err != nil {
		t.Fatalf("GenerateImplant: %v", err)
	}

	history := stub.history()
	if len(history) == 0 {
		t.Fatal("nothing was sent to the server")
	}
	if history[0] != "generate:"+name {
		t.Fatalf("the first call was %q, want the build attempt at %q -- a delete before any build is the data loss",
			history[0], "generate:"+name)
	}

	// Every delete must be preceded by at least one Generate: the delete is
	// only legitimate once a build has compiled and reported the collision.
	seenGenerate := false
	for i, e := range history {
		switch {
		case strings.HasPrefix(e, "generate:"):
			seenGenerate = true
		case strings.HasPrefix(e, "delete:"):
			if !seenGenerate {
				t.Fatalf("delete at position %d has no preceding build: %v", i, history)
			}
		}
	}
	if !seenGenerate {
		t.Fatalf("no build was attempted: %v", history)
	}

	// And the replacement was actually built after the delete.
	if last := history[len(history)-1]; last != "generate:"+name {
		t.Errorf("the last call was %q, want the replacement build at %q", last, "generate:"+name)
	}
}

// The delete is only ever reached because a build compiled. When the server
// reports a duplicate for a name the console's own list did not hold -- a
// concurrent build, or a list that was stale -- it must be reported, not acted
// on: the name is not known to be replaceable.
func TestGenerateImplantDuplicateWithoutAKnownPriorBuildIsReported(t *testing.T) {
	stub := &replaceStub{
		builds: map[string]*clientpb.ImplantConfig{},
		failGenerate: func(int, string) error {
			return errors.New("UNIQUE constraint failed: implant_builds.name")
		},
	}
	c := &Client{RPC: stub}

	_, err := c.GenerateImplant(buildReq("raced"))
	if err == nil {
		t.Fatal("a duplicate-name failure was reported as success")
	}
	if !strings.Contains(err.Error(), "implant_builds.name") {
		t.Errorf("the duplicate error was not surfaced: %v", err)
	}
	if n := stub.count("delete:"); n != 0 {
		t.Errorf("deleted %d build(s) although no prior build was known; a race must not remove anything", n)
	}
}

// A replacement whose retry fails after the name was freed is the one window
// this ordering cannot close. The operator must be told that the previous build
// is gone, rather than reading a bare build error and assuming it is still
// there -- which would be the same silent loss in a different disguise.
func TestGenerateImplantReplacementFailureSaysTheBuildWasRemoved(t *testing.T) {
	const name = "my-implant"
	stub := newReplaceStub(name)
	stub.failGenerate = func(attempt int, _ string) error {
		if attempt == 1 {
			return nil // reaches the duplicate, so the name is freed and the retry runs
		}
		return errors.New("build failed: exit status 1")
	}
	c := &Client{RPC: stub}

	_, err := c.GenerateImplant(buildReq(name))
	if err == nil {
		t.Fatal("a failed replacement was reported as success")
	}
	msg := err.Error()
	if !strings.Contains(msg, name) {
		t.Errorf("the error does not name the build that was removed: %v", err)
	}
	if !strings.Contains(msg, "removed") {
		t.Errorf("the error does not say the previous build was removed: %v", err)
	}
	if !strings.Contains(msg, "exit status 1") {
		t.Errorf("the underlying build error was swallowed: %v", err)
	}
}

// Not being able to list the builds must not become a reason to refuse to
// build, and must not authorise a delete either: the name is simply unknown, so
// the build proceeds and a collision surfaces as the server's own error.
func TestGenerateImplantToleratesABuildListThatCannotBeRead(t *testing.T) {
	stub := &replaceStub{
		builds:           map[string]*clientpb.ImplantConfig{},
		implantBuildsErr: errors.New("server unavailable"),
	}
	c := &Client{RPC: stub}

	res, err := c.GenerateImplant(buildReq("fresh"))
	if err != nil {
		t.Fatalf("a failed build list made the build impossible: %v", err)
	}
	if res["success"] != true {
		t.Errorf("success = %v, want true", res["success"])
	}
	if res["replaced"] != false {
		t.Errorf("replaced = %v, want false when the list could not be read", res["replaced"])
	}
	if n := stub.count("delete:"); n != 0 {
		t.Errorf("deleted %d build(s) on the strength of a list that could not be read", n)
	}
}

// The response has to keep carrying the fields the console already reads, or
// the download and the message break while the fix looks green.
func TestGenerateImplantResponseKeepsItsExistingFields(t *testing.T) {
	const name = "my-implant"
	stub := newReplaceStub(name)
	c := &Client{RPC: stub}

	res, err := c.GenerateImplant(buildReq(name))
	if err != nil {
		t.Fatalf("GenerateImplant: %v", err)
	}

	for _, key := range []string{"success", "message", "name", "data", "replaced"} {
		if _, ok := res[key]; !ok {
			t.Errorf("the response has no %q field", key)
		}
	}
	// The name keeps its platform extension so the downloaded file is not
	// nameless, and the payload is still base64 for JSON.
	if res["name"] != name+".exe" {
		t.Errorf("name = %v, want %q", res["name"], name+".exe")
	}
	if res["data"] == "" {
		t.Error("data is empty; the built payload was dropped from the response")
	}
}

// The duplicate-name detection is what makes the delayed delete safe, and it
// reads the error's text, so the shapes it has to recognise are pinned here --
// including the near-misses that must not be treated as a duplicate, because a
// false positive frees a name that was never taken.
func TestIsDuplicateBuildName(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"sqlite unique", errors.New("UNIQUE constraint failed: implant_builds.name"), true},
		{"postgres duplicate key", errors.New(`pq: duplicate key value violates unique constraint "implant_builds_name_key"`), true},
		{"mysql duplicate entry", errors.New("Error 1062: Duplicate entry 'x' for key 'implant_builds.name'"), true},
		{"wrapped in gRPC status", errors.New("rpc error: code = Internal desc = UNIQUE constraint failed: implant_builds.name"), true},

		{"nil", nil, false},
		{"unrelated failure", errors.New("garble: exit status 1"), false},
		{"unique on another table", errors.New("UNIQUE constraint failed: implant_configs.id"), false},
		{"mentions the table without a duplicate", errors.New("implant_builds.name is not a valid column"), false},
		{"empty", errors.New(""), false},
	}

	for _, tc := range cases {
		if got := isDuplicateBuildName(tc.err); got != tc.want {
			t.Errorf("%s: isDuplicateBuildName(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

// Validation still short-circuits: a request that cannot be built must not
// reach the server at all, so it cannot delete anything either.
func TestGenerateImplantValidatesBeforeTouchingTheServer(t *testing.T) {
	const name = "my-implant"
	stub := newReplaceStub(name)
	c := &Client{RPC: stub}

	if _, err := c.GenerateImplant(buildReq("")); err == nil {
		t.Error("an empty name was accepted")
	}
	targets := buildReq(name)
	targets.OS = "plan9"
	if _, err := c.GenerateImplant(targets); err == nil {
		t.Error("an unsupported os was accepted")
	}

	if got := stub.history(); len(got) != 0 {
		t.Errorf("an invalid request reached the server: %v", got)
	}
	if got := stub.names(); len(got) != 1 || got[0] != name {
		t.Errorf("builds = %v, want the previous %q untouched", got, name)
	}
}
