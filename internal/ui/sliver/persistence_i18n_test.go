package sliver

import (
	"strings"
	"testing"
	"unicode"
)

// The catalog and its Chinese side table are two separate declarations, so they
// can drift: a module added to moduleCatalog with no moduleLocalized entry would
// render in English on an otherwise Chinese console, and nothing would fail.
// These tests make that drift loud.

func TestEveryCatalogModuleHasChineseText(t *testing.T) {
	for _, m := range moduleCatalog {
		zh, ok := moduleLocalized[m.ID]
		if !ok {
			t.Errorf("module %q has no moduleLocalized entry", m.ID)
			continue
		}
		if strings.TrimSpace(zh.Name) == "" {
			t.Errorf("module %q has an empty Chinese name", m.ID)
		}
		if strings.TrimSpace(zh.Description) == "" {
			t.Errorf("module %q has an empty Chinese description", m.ID)
		}
	}
}

func TestNoOrphanChineseEntries(t *testing.T) {
	known := map[string]bool{}
	for _, m := range moduleCatalog {
		known[m.ID] = true
	}
	for id := range moduleLocalized {
		if !known[id] {
			t.Errorf("moduleLocalized has %q but no module in the catalog uses that id", id)
		}
	}
}

// A translation that is really just the English string copy-pasted would pass the
// presence checks above while leaving the module untranslated in practice, which
// is exactly the bug this work set out to fix.
func TestChineseTextIsActuallyChinese(t *testing.T) {
	for id, zh := range moduleLocalized {
		if !containsHan(zh.Description) {
			t.Errorf("module %q: Chinese description %q contains no Han characters", id, zh.Description)
		}
		// Name is allowed to be a proper noun ("Winlogon Userinit",
		// "authorized_keys"), so only the description is required to be prose.
	}
}

func containsHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// PersistenceModules is what the HTTP layer serialises. The side table is
// attached there, not in the catalog, so a lookup that bypassed it would ship an
// empty nameZh to the console.
func TestPersistenceModulesAttachesChineseText(t *testing.T) {
	mods := PersistenceModules()
	if len(mods) != len(moduleCatalog) {
		t.Fatalf("got %d modules, want %d", len(mods), len(moduleCatalog))
	}
	for _, m := range mods {
		if m.NameZH == "" {
			t.Errorf("module %q came back with an empty nameZh", m.ID)
		}
		if m.DescriptionZH == "" {
			t.Errorf("module %q came back with an empty descriptionZh", m.ID)
		}
		if m.Name == "" || m.Description == "" {
			t.Errorf("module %q lost its English text", m.ID)
		}
	}
}

// The catalog is a package-level table handed out by value; a caller that
// mutates the returned slice must not be able to reach back into it.
func TestPersistenceModulesDoesNotAliasTheTable(t *testing.T) {
	first := PersistenceModules()
	for i := range first {
		first[i].NameZH = "MUTATED"
		first[i].Name = "MUTATED"
	}
	second := PersistenceModules()
	for _, m := range second {
		if m.NameZH == "MUTATED" || m.Name == "MUTATED" {
			t.Fatalf("module %q was mutated through a returned catalog", m.ID)
		}
	}
}
