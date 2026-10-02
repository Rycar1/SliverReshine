// Command rpcerrcheck finds Sliver RPC calls whose in-band error is dropped.
//
// Sliver has two error channels. A gRPC call can fail, which arrives as the
// usual `error`. It can also succeed while the implant reports a failure, which
// arrives inside the reply as commonpb.Response.Err. Only the first is caught by
// checking `err`; the second is invisible unless the reply is inspected.
//
// Whether the second channel is reachable depends on the server-side handler,
// so this tool reads the server source to decide:
//
//   - A handler built on GenericHandler ends by calling getError(resp), which
//     converts Response.Err into a gRPC status error. For those methods the
//     reply cannot carry a failure that `err` missed, so checking `err` is
//     sufficient and discarding the reply is correct.
//
//   - A handler that talks to the session directly (Shell, Portfwd) returns the
//     implant's reply verbatim. Response.Err then survives into the console, and
//     a caller that does not read it reports success for a failed operation.
//
// That distinction is the whole point. Flagging every Err-bearing reply would
// report dozens of sites that are provably fine and bury the few that are not.
//
// Each call site is classified:
//
//	OK        the reply carries no Err, or it is inspected, or the server
//	          already promoted Err to a gRPC error
//	DISCARDED the reply was thrown away with `_` and its Err is reachable
//	UNCHECKED the reply was captured but its Err is never read
//
// The check may be delegated: passing a reply to a package-local helper that
// reads its Err counts as inspecting it, and transitively so. Without that,
// every wrapper function would be reported and the signal would drown.
//
// A call that genuinely intends to ignore the reply can say so:
//
//	//rpcerrcheck:ok best-effort; a failure here must not fail the caller
//
// Exit status is 1 when any DISCARDED or UNCHECKED finding is present, so it
// can be wired into CI. Usage:
//
//	go run ./tools/rpcerrcheck [-root .] [-json] [-v]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Finding is one classified call site.
type Finding struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Method string `json:"method"`
	RType  string `json:"reply_type"`
	Class  string `json:"class"`
	Var    string `json:"var,omitempty"`
	Note   string `json:"note,omitempty"`
}

// funcInfo describes one package-local function, enough to answer "does this
// function inspect the Err of its Nth parameter".
type funcInfo struct {
	name string

	// params is the flattened parameter name list, receiver excluded.
	params []string

	// checked[i] is true once params[i]'s Err is known to be read, either
	// directly in this body or by one of its callees.
	checked map[int]bool

	body *ast.BlockStmt
}

// pkg is one directory of Go source in the module under analysis.
type pkg struct {
	dir   string
	files []*ast.File

	// funcs is keyed by function or method name. Two types with a method of
	// the same name collapse into one entry, which can only make the analysis
	// more permissive; see resolveCall.
	funcs map[string]*funcInfo
}

// analyzer holds the tables the classification needs: which reply types carry
// an Err, which RPC methods return them, and which of those promote Err into a
// gRPC status on the server side.
type analyzer struct {
	fset *token.FileSet

	// errTypes maps a fully qualified protobuf type name, written the way the
	// generated code refers to it ("sliverpb.Shell"), to whether its reply can
	// carry an in-band error.
	errTypes map[string]bool

	// methods maps an RPC method name to the qualified type it returns.
	methods map[string]string

	// promoted holds the RPC method names whose server handler routes through
	// GenericHandler, which converts Response.Err into a gRPC status error. For
	// these, checking the returned `error` is enough.
	promoted map[string]bool

	// directives holds the file:line positions of //rpcerrcheck:ok comments.
	directives map[string]bool

	verbose bool
}

func main() {
	root := flag.String("root", ".", "repository root to scan")
	asJSON := flag.Bool("json", false, "emit findings as JSON")
	verbose := flag.Bool("v", false, "report every call site, including OK ones")
	flag.Parse()

	a, err := newAnalyzer(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rpcerrcheck: %v\n", err)
		os.Exit(2)
	}
	a.verbose = *verbose

	findings, err := a.scan(filepath.Join(*root, "internal"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rpcerrcheck: %v\n", err)
		os.Exit(2)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(findings); err != nil {
			fmt.Fprintf(os.Stderr, "rpcerrcheck: %v\n", err)
			os.Exit(2)
		}
	} else {
		a.report(findings, *root)
	}

	for _, f := range findings {
		if f.Class != "OK" {
			os.Exit(1)
		}
	}
}

func (a *analyzer) report(findings []Finding, root string) {
	var discarded, unchecked, ok int
	for _, f := range findings {
		switch f.Class {
		case "DISCARDED":
			discarded++
		case "UNCHECKED":
			unchecked++
		default:
			ok++
		}
	}

	if a.verbose {
		for _, f := range findings {
			fmt.Printf("%s:%d: %s: %s returns %s (%s)\n",
				a.rel(root, f.File), f.Line, f.Class, f.Method, f.RType, f.Note)
		}
		fmt.Println()
	}

	for _, f := range findings {
		if f.Class == "OK" {
			continue
		}
		fmt.Printf("%s:%d: %s: %s returns %s\n",
			a.rel(root, f.File), f.Line, f.Class, f.Method, f.RType)
		if f.Var != "" {
			fmt.Printf("    captured as %q but its Err is never read\n", f.Var)
		}
		fmt.Printf("    %s\n", f.Note)
	}

	fmt.Printf("\n%d call sites: %d OK, %d DISCARDED, %d UNCHECKED\n",
		len(findings), ok, discarded, unchecked)
	if discarded+unchecked > 0 {
		fmt.Println("\nAdd a check such as:")
		fmt.Println("    if errMsg := resp.GetResponse().GetErr(); errMsg != \"\" {")
		fmt.Println("        return errors.New(errMsg)")
		fmt.Println("    }")
		fmt.Println("\nIf the reply is intentionally ignored, mark the call:")
		fmt.Println("    //rpcerrcheck:ok <reason>")
	}
}

func (a *analyzer) rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return r
}

// newAnalyzer reads the generated protobuf, the gRPC interface, and the server
// handlers to learn which replies can carry an error the console might miss.
func newAnalyzer(root string) (*analyzer, error) {
	a := &analyzer{
		fset:       token.NewFileSet(),
		errTypes:   map[string]bool{},
		methods:    map[string]string{},
		promoted:   map[string]bool{},
		directives: map[string]bool{},
	}

	// commonpb.Response is the field that actually holds the message.
	a.errTypes["commonpb.Response"] = true

	protoDir := filepath.Join(root, "sliver", "protobuf")
	for _, p := range []string{"commonpb", "sliverpb", "clientpb"} {
		matches, err := filepath.Glob(filepath.Join(protoDir, p, "*.pb.go"))
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no generated protobuf found under %s", filepath.Join(protoDir, p))
		}
		for _, m := range matches {
			if strings.HasSuffix(m, "_grpc.pb.go") {
				continue
			}
			if err := a.learnReplyTypes(m, p); err != nil {
				return nil, err
			}
		}
	}

	grpcFile := filepath.Join(protoDir, "rpcpb", "services_grpc.pb.go")
	if err := a.learnMethods(grpcFile); err != nil {
		return nil, err
	}
	if len(a.methods) == 0 {
		return nil, fmt.Errorf("no RPC methods found in %s", grpcFile)
	}

	serverDir := filepath.Join(root, "sliver", "server", "rpc")
	if err := a.learnServerRouting(serverDir); err != nil {
		return nil, err
	}
	if len(a.promoted) == 0 {
		return nil, fmt.Errorf("no GenericHandler-routed RPC found under %s", serverDir)
	}
	return a, nil
}

// learnReplyTypes records, for every message in one generated file, whether it
// can carry an in-band error.
//
// A message qualifies when it has an `Err string` field directly (that is
// commonpb.Response itself) or when it has a `Response *commonpb.Response`
// field, which is how every implant-facing reply reports failure.
func (a *analyzer) learnReplyTypes(path, pkg string) error {
	file, err := parser.ParseFile(a.fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return err
	}

	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			return true
		}
		for _, field := range st.Fields.List {
			if len(field.Names) != 1 {
				continue
			}
			switch name := field.Names[0].Name; {
			case name == "Err" && isStringType(field.Type):
				a.errTypes[pkg+"."+ts.Name.Name] = true
			case name == "Response" && isCommonResponse(field.Type):
				a.errTypes[pkg+"."+ts.Name.Name] = true
			}
		}
		return true
	})
	return nil
}

func isStringType(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "string"
}

// isCommonResponse matches both `*commonpb.Response` and the value form.
func isCommonResponse(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Response" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "commonpb"
}

// learnMethods reads the SliverRPCClient interface. Only unary methods are
// recorded; a streaming method has no single reply struct to inspect.
func (a *analyzer) learnMethods(path string) error {
	file, err := parser.ParseFile(a.fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return err
	}

	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "SliverRPCClient" {
			return true
		}
		it, ok := ts.Type.(*ast.InterfaceType)
		if !ok || it.Methods == nil {
			return true
		}
		for _, field := range it.Methods.List {
			ft, ok := field.Type.(*ast.FuncType)
			if !ok || ft.Results == nil {
				continue
			}
			// Unary methods end with `error`; the reply is the result before it.
			results := ft.Results.List
			if len(results) < 2 {
				continue
			}
			if !isErrorType(results[len(results)-1].Type) {
				continue
			}
			qualified := qualifiedTypeName(results[len(results)-2].Type)
			if qualified == "" {
				continue
			}
			for _, name := range field.Names {
				a.methods[name.Name] = qualified
			}
		}
		return true
	})
	return nil
}

func isErrorType(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "error"
}

// qualifiedTypeName renders a reply type as "pkg.Message".
func qualifiedTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name + "." + sel.Sel.Name
}

// learnServerRouting records which RPC handlers route through GenericHandler.
//
// That matters because GenericHandler finishes with getError(resp), turning
// Response.Err into a gRPC status error. A handler that instead calls
// session.Request directly returns the implant's reply untouched, so its Err
// only reaches the console if the console reads it.
func (a *analyzer) learnServerRouting(dir string) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no server RPC sources found under %s", dir)
	}

	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(a.fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !isServerMethod(fn) {
				continue
			}
			if callsGenericHandler(fn.Body) {
				a.promoted[fn.Name.Name] = true
			}
		}
	}
	return nil
}

// isServerMethod reports whether fn is a method on the RPC *Server.
func isServerMethod(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "Server"
}

// callsGenericHandler reports whether body invokes GenericHandler anywhere.
func callsGenericHandler(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "GenericHandler" {
			found = true
			return false
		}
		return true
	})
	return found
}

// scan walks every package under dir and classifies each RPC call.
func (a *analyzer) scan(dir string) ([]Finding, error) {
	byDir := map[string][]string{}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		dir := filepath.Dir(path)
		byDir[dir] = append(byDir[dir], path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var findings []Finding
	for _, d := range dirs {
		pkgFindings, err := a.scanPackage(d, byDir[d])
		if err != nil {
			return nil, err
		}
		findings = append(findings, pkgFindings...)
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

// scanPackage parses one directory, resolves which local helpers inspect a
// reply's Err, then classifies every RPC call site.
func (a *analyzer) scanPackage(dir string, paths []string) ([]Finding, error) {
	p := &pkg{dir: dir, funcs: map[string]*funcInfo{}}

	for _, path := range paths {
		file, err := parser.ParseFile(a.fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		p.files = append(p.files, file)
		a.collectDirectives(file)
		a.collectFuncs(p, file)
	}

	// Iterate to a fixpoint: a helper counts as checking its parameter if it
	// reads it, or forwards it to another helper that does.
	for changed := true; changed; {
		changed = false
		for _, fi := range p.funcs {
			if a.propagate(p, fi) {
				changed = true
			}
		}
	}

	var findings []Finding
	for _, file := range p.files {
		path := a.fset.Position(file.Pos()).Filename
		ast.Inspect(file, func(n ast.Node) bool {
			var body *ast.BlockStmt
			switch fn := n.(type) {
			case *ast.FuncDecl:
				body = fn.Body
			case *ast.FuncLit:
				body = fn.Body
			default:
				return true
			}
			if body == nil {
				return false
			}
			findings = append(findings, a.scanBody(p, path, body)...)
			// Do not descend: a nested literal is classified as part of its
			// enclosing function, where its captured variables live.
			return false
		})
	}
	return findings, nil
}

// collectDirectives indexes every //rpcerrcheck:ok comment by file:line.
func (a *analyzer) collectDirectives(file *ast.File) {
	path := a.fset.Position(file.Pos()).Filename
	for _, group := range file.Comments {
		for _, c := range group.List {
			if !strings.HasPrefix(c.Text, "//rpcerrcheck:ok") {
				continue
			}
			a.directives[key(path, a.fset.Position(c.Pos()).Line)] = true
		}
	}
}

// suppressed reports whether a call on line is covered by a directive, either
// on its own line or on the line immediately above.
func (a *analyzer) suppressed(path string, line int) bool {
	return a.directives[key(path, line)] || a.directives[key(path, line-1)]
}

func key(path string, line int) string {
	return path + ":" + strconv.Itoa(line)
}

// collectFuncs records each function's flattened parameter names.
func (a *analyzer) collectFuncs(p *pkg, file *ast.File) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Type.Params == nil {
			continue
		}
		p.funcs[fn.Name.Name] = &funcInfo{
			name:    fn.Name.Name,
			params:  flattenParams(fn.Type.Params),
			checked: map[int]bool{},
			body:    fn.Body,
		}
	}
}

// flattenParams returns every parameter name in declaration order.
func flattenParams(params *ast.FieldList) []string {
	var names []string
	for _, field := range params.List {
		if len(field.Names) == 0 {
			// An unnamed parameter cannot be referenced, so it can never be
			// checked by name.
			names = append(names, "")
			continue
		}
		for _, n := range field.Names {
			names = append(names, n.Name)
		}
	}
	return names
}

// propagate marks parameters of fi as checked when fi reads their Err or hands
// them to another local helper that does. Reports whether anything changed.
func (a *analyzer) propagate(p *pkg, fi *funcInfo) bool {
	changed := false

	ast.Inspect(fi.body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			// Do not follow into nested literals: they are scanned separately
			// when the enclosing function is classified.
			return false
		case *ast.CallExpr:
			callee := p.resolveCall(node)
			if callee == nil {
				return true
			}
			for i, arg := range node.Args {
				if !callee.checked[i] {
					continue
				}
				idx, ok := indexOf(fi.params, identName(arg))
				if !ok || fi.checked[idx] {
					continue
				}
				fi.checked[idx] = true
				changed = true
			}
		}
		return true
	})

	// A parameter read directly also counts.
	for i, name := range fi.params {
		if name == "" || fi.checked[i] {
			continue
		}
		if a.errIsRead(fi.body, name) {
			fi.checked[i] = true
			changed = true
		}
	}

	return changed
}

// resolveCall maps a call expression to a package-local function, or nil.
//
// Methods are resolved by bare name. Two receiver types with a method of the
// same name therefore collide; the effect is limited to suppressing a finding
// for a call whose name matches a helper that checks that argument, which is
// the safe direction for a linter.
func (p *pkg) resolveCall(call *ast.CallExpr) *funcInfo {
	var name string
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		name = fun.Name
	case *ast.SelectorExpr:
		name = fun.Sel.Name
	default:
		return nil
	}
	return p.funcs[name]
}

func identName(expr ast.Expr) string {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func indexOf(names []string, name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	for i, n := range names {
		if n == name {
			return i, true
		}
	}
	return 0, false
}

// rpcCall describes one `x.RPC.Method(...)` call site.
type rpcCall struct {
	pos    token.Pos
	method string
}

// scanBody classifies every RPC call inside one function body.
func (a *analyzer) scanBody(p *pkg, path string, body *ast.BlockStmt) []Finding {
	var findings []Finding

	// Calls captured into a variable, so the Err check can be found by name.
	captured := map[string]rpcCall{}

	// An `if` initialiser is also reached as an ordinary assignment, because
	// ast.Inspect descends into it. Track what has been classified so a call is
	// reported once rather than twice.
	seen := map[token.Pos]bool{}

	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}

		// Three shapes can carry an RPC call, and all three matter. The `if`
		// initialiser drops the reply exactly as a plain `_, err :=` does, and a
		// return hands the reply upward instead of losing it.
		var call *ast.CallExpr
		var target ast.Expr

		switch node := n.(type) {
		case *ast.AssignStmt:
			if len(node.Lhs) == 0 || len(node.Rhs) == 0 {
				return true
			}
			c, ok := node.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			call, target = c, node.Lhs[0]
		case *ast.IfStmt:
			assign, ok := node.Init.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
				return true
			}
			c, ok := assign.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			call, target = c, assign.Lhs[0]
		case *ast.ReturnStmt:
			if len(node.Results) == 0 {
				return true
			}
			c, ok := node.Results[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			call = c
		default:
			return true
		}

		method, ok := rpcMethodName(call)
		if !ok {
			return true
		}
		if seen[call.Pos()] {
			return true
		}
		seen[call.Pos()] = true
		replyType, known := a.methods[method]
		if !known {
			return true
		}
		line := a.line(call.Pos())

		switch {
		case a.suppressed(path, line):
			findings = append(findings, Finding{
				File: path, Line: line, Method: method, RType: replyType,
				Class: "OK", Note: "suppressed by //rpcerrcheck:ok",
			})
			return true
		case !a.errTypes[replyType]:
			findings = append(findings, Finding{
				File: path, Line: line, Method: method, RType: replyType,
				Class: "OK", Note: "reply carries no Err field",
			})
			return true
		case a.promoted[method]:
			// The server's GenericHandler already turned Response.Err into a
			// gRPC status error, so the caller's `err` check covers it.
			findings = append(findings, Finding{
				File: path, Line: line, Method: method, RType: replyType,
				Class: "OK", Note: "server promotes Err to a gRPC error",
			})
			return true
		}

		if id, isIdent := target.(*ast.Ident); isIdent && id.Name != "_" {
			captured[id.Name] = rpcCall{pos: call.Pos(), method: method}
			return true
		}

		if target == nil {
			// The reply is returned to the caller, so whether its Err is read is
			// decided there. Flagging it here would report every wrapper.
			findings = append(findings, Finding{
				File: path, Line: line, Method: method, RType: replyType,
				Class: "OK", Note: "reply returned to the caller",
			})
			return true
		}

		// The reply was dropped on the floor. The server does not promote this
		// method's Err, so whatever the implant reported is gone.
		findings = append(findings, Finding{
			File: path, Line: line, Method: method, RType: replyType,
			Class: "DISCARDED",
			Note:  "reply discarded; the server does not promote this method's Err",
		})
		return true
	})

	for name, call := range captured {
		line := a.line(call.pos)
		if a.errIsRead(body, name) || a.delegatedToChecker(p, body, name) {
			continue
		}
		findings = append(findings, Finding{
			File: path, Line: line, Method: call.method,
			RType: a.methods[call.method], Class: "UNCHECKED", Var: name,
			Note: "the server does not promote this method's Err",
		})
	}

	return findings
}

// delegatedToChecker reports whether name is passed to a package-local helper
// that inspects the Err of the corresponding parameter. This is what lets a
// wrapper such as execResultFrom(resp) satisfy the check.
func (a *analyzer) delegatedToChecker(p *pkg, body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch node := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			callee := p.resolveCall(node)
			if callee == nil {
				return true
			}
			for i, arg := range node.Args {
				if callee.checked[i] && identName(arg) == name {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

// errIsRead reports whether the in-band error of the captured reply named
// name is read anywhere in body.
//
// Both spellings the codebase uses are recognised: the generated getters
// (name.GetErr(), name.GetResponse().GetErr()) and direct field access
// (name.Err, name.Response.Err). No nil check on Response is required for this
// to count, because the surrounding code has to dereference the field anyway.
func (a *analyzer) errIsRead(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "Err", "GetErr":
		default:
			return true
		}
		// Walk the receiver chain looking for the captured variable. This
		// covers name.Err, name.Response.Err, name.GetErr() and
		// name.GetResponse().GetErr() alike.
		ast.Inspect(sel.X, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && id.Name == name {
				found = true
				return false
			}
			return true
		})
		return true
	})
	return found
}

// rpcMethodName recognises a call of the form x.RPC.Method(...) and returns
// the method name.
func rpcMethodName(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	recv, ok := sel.X.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if recv.Sel.Name != "RPC" {
		return "", false
	}
	return sel.Sel.Name, true
}

func (a *analyzer) line(pos token.Pos) int {
	return a.fset.Position(pos).Line
}
