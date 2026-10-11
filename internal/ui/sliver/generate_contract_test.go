package sliver

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// These tests exist because the request body is written twice -- once as the
// TypeScript ImplantConfig and once as this package's GenerateRequest -- and the
// server decodes it with DisallowUnknownFields. A field on the client that the
// struct does not have is therefore not a harmless extra: it fails the whole
// request with "json: unknown field ...".
//
// That is exactly what happened. ImplantConfig carried seven fields this struct
// never had (target, mtls, http, dns, wireguard, limitDomainJoined, transport)
// from the first commit, so "generate" and "save profile" answered 400 for
// three releases while every Go test passed: the Go tests build their bodies
// from the Go struct, so they can only ever prove the struct agrees with
// itself. The pairing has to be checked against the callers instead.

// generateJSONTags returns every JSON key GenerateRequest accepts, including the
// keys of a nested c2 entry.
func generateJSONTags(t *testing.T) map[string]bool {
	t.Helper()
	tags := map[string]bool{}
	var walk func(reflect.Type)
	walk = func(rt reflect.Type) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			tags[name] = true
			if f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() == reflect.Struct {
				walk(f.Type.Elem())
			}
		}
	}
	walk(reflect.TypeOf(GenerateRequest{}))
	if len(tags) == 0 {
		t.Fatal("GenerateRequest has no JSON tags to compare against")
	}
	return tags
}

// TestGenerateContractMatchesTheFrontend reads the frontend declaration from
// source, so a field added on either side fails here rather than in the browser.
func TestGenerateContractMatchesTheFrontend(t *testing.T) {
	tags := generateJSONTags(t)

	b, err := os.ReadFile("../../../frontend/src/lib/types.ts")
	if err != nil {
		t.Skipf("cannot read the frontend types: %v", err)
	}
	fields := implantConfigFields(t, string(b))
	if len(fields) == 0 {
		t.Fatal("parsed no fields out of ImplantConfig")
	}
	for _, f := range fields {
		if !tags[f] {
			t.Errorf("ImplantConfig.%s is not a field of GenerateRequest; the server "+
				"rejects unknown fields, so every request carrying it fails with "+
				"`json: unknown field %q`", f, f)
		}
	}
}

// TestGenerateContractMatchesTheE2EScripts covers the other in-repo caller. The
// scripts under build/ post the same body, and they were sending the same seven
// illegal fields -- so the harness meant to catch a broken build was itself
// broken, and had been since DisallowUnknownFields landed.
func TestGenerateContractMatchesTheE2EScripts(t *testing.T) {
	tags := generateJSONTags(t)
	key := regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)":`)

	for _, path := range []string{
		"../../../build/sliverreshine_e2e.ps1",
		"../../../build/sliverreshine_callback_test.ps1",
	} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Skipf("cannot read %s: %v", path, err)
		}
		body, ok := scriptGenerateBody(string(b))
		if !ok {
			t.Fatalf("%s: the /api/generate body no longer has the expected shape, so "+
				"this test can no longer check it", path)
		}
		for _, m := range key.FindAllStringSubmatch(body, -1) {
			if !tags[m[1]] {
				t.Errorf("%s: the generate body sends %q, which GenerateRequest does "+
					"not accept; the script would fail with `json: unknown field %q`",
					path, m[1], m[1])
			}
		}
	}
}

// implantConfigFields returns the field names of the ImplantConfig interface.
func implantConfigFields(t *testing.T, src string) []string {
	t.Helper()
	const decl = "export interface ImplantConfig {"
	idx := strings.Index(src, decl)
	if idx < 0 {
		t.Fatal("ImplantConfig is no longer declared in frontend/src/lib/types.ts")
	}
	rest := src[idx+len(decl):]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		t.Fatal("the ImplantConfig declaration is not closed at column zero")
	}
	var fields []string
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") ||
			strings.HasPrefix(line, "*") || strings.HasPrefix(line, "/*") {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields = append(fields, strings.TrimSpace(name))
	}
	return fields
}

// scriptGenerateBody returns the JSON literal a PowerShell helper builds before
// it posts to /api/generate: the text between the "$json = " assignment and the
// request that sends it.
func scriptGenerateBody(src string) (string, bool) {
	call := strings.Index(src, "$r = Api -Path '/api/generate'")
	if call < 0 {
		return "", false
	}
	start := strings.LastIndex(src[:call], "$json = ")
	if start < 0 {
		return "", false
	}
	return src[start+len("$json = ") : call], true
}
