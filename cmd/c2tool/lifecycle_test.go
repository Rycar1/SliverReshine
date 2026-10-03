package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestEmbeddedServerLifecycleBelongsToRun pins the regression that made every
// launch report "server unreachable".
//
// startEmbeddedServer used to end with `defer srv.Stop()`. A defer fires when
// its function returns -- immediately after the daemon was started -- so the
// launcher SIGKILLed the embedded server it had just brought up. The console
// then served its UI, auto-connect timed out ten seconds later, and nothing in
// the launcher output or the daemon log said why.
//
// The check is structural because the failure is structural: the server's
// lifetime is owned by run(), which holds the matching defer. A Stop call inside
// startEmbeddedServer is that bug in any shape it takes, and no unit test that
// does not actually spawn a daemon can see it.
func TestEmbeddedServerLifecycleBelongsToRun(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	starter := findFuncDecl(file, "startEmbeddedServer")
	if starter == nil {
		t.Fatal("startEmbeddedServer not found in main.go")
	}
	var stops []token.Position
	ast.Inspect(starter, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Stop" {
			stops = append(stops, fset.Position(call.Pos()))
		}
		return true
	})
	if len(stops) > 0 {
		t.Errorf("startEmbeddedServer calls Stop at %v: the daemon dies the moment the "+
			"launcher returns, which surfaces only as \"server unreachable\"", stops)
	}

	runner := findFuncDecl(file, "run")
	if runner == nil {
		t.Fatal("run not found in main.go")
	}
	if !defersServerStop(runner) {
		t.Error("run no longer defers srv.Stop(): the daemon outlives the process that owns it")
	}
}

func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

// defersServerStop reports whether fn contains a `defer srv.Stop()`-shaped
// statement. The receiver name is not pinned; the deferred call to a method
// named Stop is.
func defersServerStop(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		def, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		call, ok := def.Call.Fun.(*ast.SelectorExpr)
		if ok && call.Sel.Name == "Stop" {
			found = true
		}
		return true
	})
	return found
}
