package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// routesFixture is the committed copy of the API contract that the frontend
// contract test (frontend/src/lib/__tests__/routes.test.ts) imports and
// compares against every path api.ts calls. Keep it in sync with
// RoutePatterns(): on mismatch this test regenerates it, so the failure
// message doubles as the fix.
var routesFixture = filepath.Join("..", "..", "..", "frontend", "src", "lib", "__fixtures__", "routes.json")

func TestRouteTableFixture(t *testing.T) {
	want, err := json.MarshalIndent(New().RoutePatterns(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')

	if got, err := os.ReadFile(routesFixture); err == nil && string(got) == string(want) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(routesFixture), 0o755); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	if err := os.WriteFile(routesFixture, want, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Fatalf("route table fixture is out of date; regenerated %s. Run the test again to confirm.", routesFixture)
}
