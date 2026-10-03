package launch

import (
	"os"
	"path/filepath"
	"testing"
)

func envHas(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

// env() must create the build temp directory it points GOTMPDIR/TMPDIR/TMP/TEMP
// at. The failure it prevents is a compiler error that names neither the
// directory nor the permission problem, so a directory that is merely named but
// not created is not good enough.
func TestEnvCreatesAndPointsAtTheBuildTempDirectory(t *testing.T) {
	state := t.TempDir()
	s := &Server{opts: Options{StateDir: state}}
	env := s.env()

	tmp := filepath.Join(state, "tmp")
	st, err := os.Stat(tmp)
	if err != nil || !st.IsDir() {
		t.Fatalf("build temp directory %s was not created: %v", tmp, err)
	}
	for _, want := range []string{
		"SLIVER_ROOT_DIR=" + state,
		"GOTMPDIR=" + tmp,
		"TMPDIR=" + tmp,
		"TMP=" + tmp,
		"TEMP=" + tmp,
	} {
		if !envHas(env, want) {
			t.Errorf("env is missing %q", want)
		}
	}
}

// A state directory that cannot hold the temp dir must not stop the launcher:
// env() cannot return an error, so it reports the problem and carries on. The
// variables must still be present -- a caller that dropped them would fail
// implant builds with a bare compiler message, which is the bug this fixes.
func TestEnvSurvivesAnUncreatableTempDirectory(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(blocker, "state") // its parent is a file, so MkdirAll fails
	s := &Server{opts: Options{StateDir: state}}
	env := s.env()

	if len(env) == 0 {
		t.Fatal("env() must still return the ambient environment")
	}
	if !envHas(env, "GOTMPDIR="+filepath.Join(state, "tmp")) {
		t.Error("GOTMPDIR must still point at the intended directory")
	}
}
