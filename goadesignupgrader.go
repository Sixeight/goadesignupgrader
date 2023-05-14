package goadesignupgrader

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"log"
	"regexp"
	"strconv"

	"github.com/iancoleman/strcase"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
)

var Analyzer = &analysis.Analyzer{
	Name: "goadesignupgrader",
	Doc:  Doc,
	Run:  run,
	Requires: []*analysis.Analyzer{
		inspect.Analyzer,
	},
}

const Doc = "upgrade a design definition for Goa from v1 to v3"

var regexpWildcard = regexp.MustCompile(`/:([a-zA-Z0-9_]+)`)

func run(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				switch decl.Tok {
				case token.IMPORT:
					analyzeAndFixImports(pass, decl)
				case token.VAR:
					analyzeAndFixVariables(pass, decl)
				}
			case *ast.FuncDecl:
				analyzeAndFixFuncs(pass, decl)
			}
		}
	}
	return nil, nil
}

func analyzeAPI(pass *analysis.Pass, expr *ast.CallExpr) bool {
	var changed bool
	for _, expr := range expr.Args {
		expr, ok := expr.(*ast.FuncLit)
		if !ok {
			continue
		}
		analyzeGenericDSL(pass, expr)
		var (
			listAPI     []ast.Stmt
			listAPIHTTP []ast.Stmt
		)
		for _, stmt := range expr.Body.List {
			stmt, ok := stmt.(*ast.ExprStmt)
			if !ok {
				continue
			}
			expr, ok := stmt.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			ident, ok := expr.Fun.(*ast.Ident)
			if !ok {
				continue
			}
			switch ident.Name {
			case "BasePath":
				changed = analyzeBasePath(pass, stmt, expr, ident, &listAPIHTTP) || changed
			case "Consumes":
				changed = analyzeConsumes(pass, stmt, &listAPIHTTP) || changed
			case "Params":
				changed = analyzeParams(pass, stmt, &listAPIHTTP) || changed
			case "Produces":
				changed = analyzeProduces(pass, stmt, &listAPIHTTP) || changed
			default:
				listAPI = append(listAPI, stmt)
			}
		}
		if len(listAPIHTTP) > 0 {
			listAPI = append(listAPI, &ast.ExprStmt{
				X: &ast.CallExpr{
					Fun: &ast.Ident{
						Name: "HTTP",
					},
					Args: []ast.Expr{
						&ast.FuncLit{
							Type: &ast.FuncType{},
							Body: &ast.BlockStmt{
								List: listAPIHTTP,
							},
						},
					},
				},
			})
			expr.Body.List = listAPI
		}
	}
	return changed
}

func analyzeAction(pass *analysis.Pass, stmt *ast.ExprStmt, expr *ast.CallExpr, ident *ast.Ident, parent *[]ast.Stmt) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `Action should be replaced with Method`})
	ident.Name = "Method"
	*parent = append(*parent, stmt)
	for _, expr := range expr.Args {
		expr, ok := expr.(*ast.FuncLit)
		if !ok {
			continue
		}
		var (
			listAction     []ast.Stmt
			listActionHTTP []ast.Stmt
		)
		for _, stmt := range expr.Body.List {
			stmt, ok := stmt.(*ast.ExprStmt)
			if !ok {
				continue
			}
			expr, ok := stmt.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			ident, ok := expr.Fun.(*ast.Ident)
			if !ok {
				continue
			}
			switch ident.Name {
			case "Headers":
				analyzeHeaders(pass, stmt, &listActionHTTP)
			case "Params":
				analyzeParams(pass, stmt, &listActionHTTP)
			case "Response":
				analyzeResponse(pass, stmt, expr, &listActionHTTP, &listAction)
			case "Routing":
				analyzeRouting(pass, expr, &listActionHTTP)
			default:
				listAction = append(listAction, stmt)
			}
		}
		if len(listActionHTTP) > 0 {
			listAction = append(listAction, &ast.ExprStmt{
				X: &ast.CallExpr{
					Fun: &ast.Ident{
						Name: "HTTP",
					},
					Args: []ast.Expr{
						&ast.FuncLit{
							Type: &ast.FuncType{},
							Body: &ast.BlockStmt{
								List: listActionHTTP,
							},
						},
					},
				},
			})
			expr.Body.List = listAction
		}
	}
	return true
}

func analyzeAndFixImports(pass *analysis.Pass, decl *ast.GenDecl) {
	var changed bool
	var specs []ast.Spec
	for _, spec := range decl.Specs {
		spec, ok := spec.(*ast.ImportSpec)
		if !ok {
			continue
		}
		changed = analyzeImport(pass, spec) || changed
		if spec.Path.Value != `""` {
			specs = append(specs, spec)
		}
	}
	if changed {
		decl.Specs = specs
		var b []byte
		if len(specs) != 0 {
			b = formatNode(pass.Fset, decl)
		}
		pass.Report(analysis.Diagnostic{
			Pos: decl.Pos(), Message: `import declarations should be fixed`,
			SuggestedFixes: []analysis.SuggestedFix{{Message: "Fix", TextEdits: []analysis.TextEdit{
				{Pos: decl.Pos(), End: decl.End(), NewText: b},
			}}},
		})
	}
}

func analyzeAndFixVariables(pass *analysis.Pass, decl *ast.GenDecl) {
	var changed bool
	for _, spec := range decl.Specs {
		spec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, expr := range spec.Values {
			expr, ok := expr.(*ast.CallExpr)
			if !ok {
				continue
			}
			ident, ok := expr.Fun.(*ast.Ident)
			if !ok {
				continue
			}
			switch ident.Name {
			case "API":
				changed = analyzeAPI(pass, expr) || changed
			case "MediaType":
				changed = analyzeMediaType(pass, expr, ident) || changed
			case "Resource":
				changed = analyzeResource(pass, expr, ident) || changed
			case "Type":
				changed = analyzeType(pass, expr) || changed
			}
		}
	}
	if changed {
		pass.Report(analysis.Diagnostic{
			Pos: decl.Pos(), Message: `variable declarations should be fixed`,
			SuggestedFixes: []analysis.SuggestedFix{{Message: "Fix", TextEdits: []analysis.TextEdit{
				{Pos: decl.Pos(), End: decl.End(), NewText: formatNode(pass.Fset, decl)},
			}}},
		})
	}
}

func analyzeAndFixFuncs(pass *analysis.Pass, decl *ast.FuncDecl) {
	body := decl.Body
	changed := analyzeGenericDSL(pass, body)
	if changed {
		decl.Body = body
		b := formatNode(pass.Fset, decl)
		pass.Report(analysis.Diagnostic{
			Pos: decl.Pos(), Message: `function declarations should be fixed`,
			SuggestedFixes: []analysis.SuggestedFix{{Message: "Fix", TextEdits: []analysis.TextEdit{
				{Pos: decl.Pos(), End: decl.End(), NewText: b},
			}}},
		})
	}
}

func analyzeAttribute(pass *analysis.Pass, expr *ast.CallExpr) bool {
	var changed bool
	for _, e := range expr.Args {
		ident, ok := e.(*ast.Ident)
		if !ok {
			continue
		}
		switch ident.Name {
		case "DateTime":
			changed = analyzeDateTime(pass, expr, ident) || changed
		}
	}
	return changed
}

func analyzeBasePath(pass *analysis.Pass, stmt *ast.ExprStmt, expr *ast.CallExpr, ident *ast.Ident, parent *[]ast.Stmt) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `BasePath should be replaced with Path and wrapped by HTTP`})
	ident.Name = "Path"
	for _, e := range expr.Args {
		e, ok := e.(*ast.BasicLit)
		if !ok {
			continue
		}
		replaced := replaceWildcard(e.Value)
		if replaced != e.Value {
			pass.Report(analysis.Diagnostic{Pos: e.Pos(), Message: `colons in BasePath should be replaced with curly braces`})
			e.Value = replaced
		}
	}
	*parent = append(*parent, stmt)
	return true
}

func analyzeCanonicalActionName(pass *analysis.Pass, stmt *ast.ExprStmt, ident *ast.Ident, parent *[]ast.Stmt) bool {
	pass.Report(analysis.Diagnostic{Pos: stmt.Pos(), Message: `CanonicalActionName should be replaced with CanonicalMethod and wrapped by HTTP`})
	ident.Name = "CanonicalMethod"
	*parent = append(*parent, stmt)
	return true
}

func analyzeConsumes(pass *analysis.Pass, stmt *ast.ExprStmt, parent *[]ast.Stmt) bool {
	pass.Report(analysis.Diagnostic{Pos: stmt.Pos(), Message: `Consumes should be wrapped by HTTP`})
	*parent = append(*parent, stmt)
	return true
}

func analyzeDateTime(pass *analysis.Pass, expr *ast.CallExpr, ident *ast.Ident) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `DateTime should be replaced with String + Format(FormatDateTime)`})
	ident.Name = "String"
	e, ok := expr.Args[len(expr.Args)-1].(*ast.FuncLit)
	if !ok {
		e = &ast.FuncLit{
			Type: &ast.FuncType{},
			Body: &ast.BlockStmt{},
		}
		expr.Args = append(expr.Args, e)
	}
	e.Body.List = append(e.Body.List, &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: &ast.Ident{
				Name: "Format",
			},
			Args: []ast.Expr{
				&ast.Ident{
					Name: "FormatDateTime",
				},
			},
		},
	})
	return true
}

func analyzeDefaultMedia(pass *analysis.Pass, ident *ast.Ident) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `DefaultMedia should be removed`})
	return true
}

func analyzeGenericDSL(pass *analysis.Pass, node ast.Node) bool {
	var changed bool
	ast.Inspect(node, func(n ast.Node) bool {
		switch expr := n.(type) {
		case *ast.Ident:
			switch expr.Name {
			case "Integer":
				changed = analyzeInteger(pass, expr) || changed
			case "Number":
				changed = analyzeNumber(pass, expr) || changed
			case "File":
				changed = analyzeFile(pass, expr) || changed
			}
		case *ast.CallExpr:
			ident, ok := expr.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			switch ident.Name {
			case "Attribute":
				changed = analyzeAttribute(pass, expr) || changed
			case "HashOf":
				changed = analyzeHashOf(pass, expr, ident) || changed
			case "Metadata":
				changed = analyzeMetadata(pass, ident) || changed
			default:
				changed = analyzeAttribute(pass, expr) || changed
			}
		}
		return true
	})
	return changed
}

func analyzeHTTPRoutingDSL(pass *analysis.Pass, expr *ast.CallExpr) bool {
	var changed bool
	for _, e := range expr.Args {
		e, ok := e.(*ast.BasicLit)
		if !ok {
			continue
		}
		replaced := replaceWildcard(e.Value)
		if replaced != e.Value {
			pass.Report(analysis.Diagnostic{Pos: e.Pos(), Message: `colons in HTTP routing DSLs should be replaced with curly braces`})
			e.Value = replaced
			changed = true
		}
	}
	return changed
}

func analyzeHTTPStatusConstant(pass *analysis.Pass, ident *ast.Ident) bool {
	name := "Status" + ident.Name
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: fmt.Sprintf(`%s should be replaced with %s`, ident.Name, name)})
	ident.Name = name
	return true
}

func analyzeHashOf(pass *analysis.Pass, expr *ast.CallExpr, ident *ast.Ident) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `HashOf should be replaced with MapOf`})
	ident.Name = "MapOf"
	var (
		changed bool
		args    []ast.Expr
		list    []ast.Stmt
	)
	for i, expr := range expr.Args {
		switch i {
		case 2:
			pass.Report(analysis.Diagnostic{Pos: expr.Pos(), Message: `optional DSL for key of HashOf should be set by Key`})
			list = append(list, &ast.ExprStmt{
				X: &ast.CallExpr{
					Fun: &ast.Ident{
						Name: "Key",
					},
					Args: []ast.Expr{
						expr,
					},
				},
			})
			changed = true
		case 3:
			pass.Report(analysis.Diagnostic{Pos: expr.Pos(), Message: `optional DSL for value of HashOf should be set by Elem`})
			list = append(list, &ast.ExprStmt{
				X: &ast.CallExpr{
					Fun: &ast.Ident{
						Name: "Elem",
					},
					Args: []ast.Expr{
						expr,
					},
				},
			})
			changed = true
		default:
			args = append(args, expr)
		}
	}
	if len(list) > 0 {
		args = append(args, &ast.FuncLit{
			Type: &ast.FuncType{},
			Body: &ast.BlockStmt{
				List: list,
			},
		})
	}
	if changed {
		expr.Args = args
	}
	return true
}

func analyzeHeaders(pass *analysis.Pass, stmt *ast.ExprStmt, parent *[]ast.Stmt) bool {
	pass.Report(analysis.Diagnostic{Pos: stmt.Pos(), Message: `Headers should be wrapped by HTTP`})
	*parent = append(*parent, stmt)
	return true
}

func analyzeImport(pass *analysis.Pass, spec *ast.ImportSpec) bool {
	var changed bool
	if path, err := strconv.Unquote(spec.Path.Value); err == nil {
		switch path {
		case "github.com/goadesign/goa/design":
			pass.Report(analysis.Diagnostic{Pos: spec.Pos(), Message: `"github.com/goadesign/goa/design" should be removed`})
			path = ""
		case "github.com/goadesign/goa/design/apidsl":
			pass.Report(analysis.Diagnostic{Pos: spec.Pos(), Message: `"github.com/goadesign/goa/design/apidsl" should be replaced with "goa.design/goa/v3/dsl"`})
			path = "goa.design/goa/v3/dsl"
		}
		if path := strconv.Quote(path); spec.Path.Value != path {
			spec.Path.Value = path
			changed = true
		}
	}
	return changed
}

func analyzeInteger(pass *analysis.Pass, ident *ast.Ident) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `Integer should be replaced with Int`})
	ident.Name = "Int"
	return true
}

func analyzeNumber(pass *analysis.Pass, ident *ast.Ident) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `Number should be replaced with Float64`})
	ident.Name = "Float64"
	return true
}

func analyzeFile(pass *analysis.Pass, ident *ast.Ident) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `File should be replaced with Bytes`})
	ident.Name = "Bytes"
	return true
}

func analyzeMedia(pass *analysis.Pass, stmt *ast.ExprStmt, ident *ast.Ident, parent *[]ast.Stmt, errorResponse bool) bool {
	if errorResponse {
		pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `Media for an error response should be removed`})
	} else {
		pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `Media for a non-error response should be replaced with Result and wrapped by HTTP in the parent`})
		ident.Name = "Result"
		*parent = append(*parent, stmt)
	}
	return true
}

func analyzeMediaType(pass *analysis.Pass, expr *ast.CallExpr, ident *ast.Ident) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `MediaType should be replaced with ResultType`})
	ident.Name = "ResultType"
	for _, expr := range expr.Args {
		expr, ok := expr.(*ast.FuncLit)
		if !ok {
			continue
		}
		analyzeGenericDSL(pass, expr)
	}
	return true
}

func analyzeMetadata(pass *analysis.Pass, ident *ast.Ident) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `Metadata should be replaced with Meta`})
	ident.Name = "Meta"
	return true
}

func analyzeParams(pass *analysis.Pass, stmt *ast.ExprStmt, parent *[]ast.Stmt) bool {
	pass.Report(analysis.Diagnostic{Pos: stmt.Pos(), Message: `Params should be wrapped by HTTP`})
	*parent = append(*parent, stmt)
	return true
}

func analyzeParent(pass *analysis.Pass, stmt *ast.ExprStmt, parent *[]ast.Stmt) bool {
	pass.Report(analysis.Diagnostic{Pos: stmt.Pos(), Message: `Parent should be wrapped by HTTP`})
	*parent = append(*parent, stmt)
	return true
}

func analyzeProduces(pass *analysis.Pass, stmt *ast.ExprStmt, parent *[]ast.Stmt) bool {
	pass.Report(analysis.Diagnostic{Pos: stmt.Pos(), Message: `Produces should be wrapped by HTTP`})
	*parent = append(*parent, stmt)
	return true
}

func analyzeResource(pass *analysis.Pass, expr *ast.CallExpr, ident *ast.Ident) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `Resource should be replaced with Service`})
	ident.Name = "Service"
	for _, expr := range expr.Args {
		expr, ok := expr.(*ast.FuncLit)
		if !ok {
			continue
		}
		analyzeGenericDSL(pass, expr)
		var (
			listResource     []ast.Stmt
			listResourceHTTP []ast.Stmt
		)
		for _, stmt := range expr.Body.List {
			stmt, ok := stmt.(*ast.ExprStmt)
			if !ok {
				continue
			}
			expr, ok := stmt.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			ident, ok := expr.Fun.(*ast.Ident)
			if !ok {
				continue
			}
			switch ident.Name {
			case "Action":
				analyzeAction(pass, stmt, expr, ident, &listResource)
			case "BasePath":
				analyzeBasePath(pass, stmt, expr, ident, &listResourceHTTP)
			case "CanonicalActionName":
				analyzeCanonicalActionName(pass, stmt, ident, &listResourceHTTP)
			case "DefaultMedia":
				analyzeDefaultMedia(pass, ident)
			case "Headers":
				analyzeHeaders(pass, stmt, &listResourceHTTP)
			case "Params":
				analyzeParams(pass, stmt, &listResourceHTTP)
			case "Parent":
				analyzeParent(pass, stmt, &listResourceHTTP)
			case "Response":
				analyzeResponse(pass, stmt, expr, &listResourceHTTP, &listResource)
			default:
				listResource = append(listResource, stmt)
			}
		}
		if len(listResourceHTTP) > 0 {
			listResource = append(listResource, &ast.ExprStmt{
				X: &ast.CallExpr{
					Fun: &ast.Ident{
						Name: "HTTP",
					},
					Args: []ast.Expr{
						&ast.FuncLit{
							Type: &ast.FuncType{},
							Body: &ast.BlockStmt{
								List: listResourceHTTP,
							},
						},
					},
				},
			})
			expr.Body.List = listResource
		}
	}
	return true
}

func analyzeResponse(pass *analysis.Pass, stmt *ast.ExprStmt, expr *ast.CallExpr, parent *[]ast.Stmt, grandparent *[]ast.Stmt) bool {
	pass.Report(analysis.Diagnostic{Pos: expr.Pos(), Message: `Response should be wrapped by HTTP`})
	var (
		changed       bool
		errorResponse bool
		args          []ast.Expr
	)
	for _, e := range expr.Args {
		switch t := e.(type) {
		case *ast.Ident:
			switch t.Name {
			case "ErrorMedia":
				pass.Report(analysis.Diagnostic{Pos: t.Pos(), Message: `ErrorMedia should be removed`})
				changed = true
				continue
			case "BadRequest", "Unauthorized", "PaymentRequired", "Forbidden", "NotFound",
				"MethodNotAllowed", "NotAcceptable", "ProxyAuthRequired", "RequestTimeout", "Conflict",
				"Gone", "LengthRequired", "PreconditionFailed", "RequestEntityTooLarge", "RequestURITooLong",
				"UnsupportedMediaType", "RequestedRangeNotSatisfiable", "ExpectationFailed", "Teapot", "UnprocessableEntity",
				"InternalServerError", "NotImplemented", "BadGateway", "ServiceUnavailable", "GatewayTimeout", "HTTPVersionNotSupported":
				errorResponse = true
				errorName := fmt.Sprintf("%q", strcase.ToSnake(t.Name))
				*grandparent = append(*grandparent, &ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.Ident{
							Name: "Error",
						},
						Args: []ast.Expr{
							&ast.BasicLit{
								Kind:  token.STRING,
								Value: errorName,
							},
						},
					},
				})
				args = append(args, &ast.Ident{
					Name: errorName,
				})
				fallthrough
			case "Continue", "SwitchingProtocols",
				"OK", "Created", "Accepted", "NonAuthoritativeInfo", "NoContent", "ResetContent", "PartialContent",
				"MultipleChoices", "MovedPermanently", "Found", "SeeOther", "NotModified", "UseProxy", "TemporaryRedirect":
				changed = analyzeHTTPStatusConstant(pass, t) || changed
			}
			args = append(args, t)
		case *ast.FuncLit:
			var list []ast.Stmt
			for _, s := range t.Body.List {
				s, ok := s.(*ast.ExprStmt)
				if !ok {
					continue
				}
				e, ok := s.X.(*ast.CallExpr)
				if !ok {
					continue
				}
				i, ok := e.Fun.(*ast.Ident)
				if !ok {
					continue
				}
				switch i.Name {
				case "Media":
					changed = analyzeMedia(pass, s, i, grandparent, errorResponse) || changed
				case "Status":
					changed = analyzeStatus(pass, s, i, &list) || changed
				default:
					list = append(list, s)
				}
			}
			if len(list) != len(t.Body.List) {
				t.Body.List = list
			}
			if len(t.Body.List) > 0 {
				args = append(args, t)
			}
		default:
			args = append(args, t)
		}
	}
	if changed {
		expr.Args = args
	}
	*parent = append(*parent, stmt)
	return true
}

func analyzeRouting(pass *analysis.Pass, expr *ast.CallExpr, parent *[]ast.Stmt) bool {
	pass.Report(analysis.Diagnostic{Pos: expr.Pos(), Message: `Routing should be replaced with HTTP`})
	for _, e := range expr.Args {
		e, ok := e.(*ast.CallExpr)
		if !ok {
			continue
		}
		ident, ok := e.Fun.(*ast.Ident)
		if !ok {
			continue
		}
		switch ident.Name {
		case "GET", "HEAD", "POST", "PUT", "DELETE", "CONNECT", "OPTIONS", "TRACE", "PATCH":
			analyzeHTTPRoutingDSL(pass, e)
			*parent = append(*parent, &ast.ExprStmt{X: e})
		}
	}
	return true
}

func analyzeStatus(pass *analysis.Pass, stmt *ast.ExprStmt, ident *ast.Ident, parent *[]ast.Stmt) bool {
	pass.Report(analysis.Diagnostic{Pos: ident.Pos(), Message: `Status should be replaced with Code`})
	ident.Name = "Code"
	*parent = append(*parent, stmt)
	return true
}

func analyzeType(pass *analysis.Pass, expr *ast.CallExpr) bool {
	var changed bool
	for _, expr := range expr.Args {
		expr, ok := expr.(*ast.FuncLit)
		if !ok {
			continue
		}
		changed = analyzeGenericDSL(pass, expr) || changed
	}
	return changed
}

func replaceWildcard(s string) string {
	return regexpWildcard.ReplaceAllString(s, "/{$1}")
}

func formatNode(fset *token.FileSet, node interface{}) []byte {
	var b bytes.Buffer
	if err := format.Node(&b, fset, node); err != nil {
		log.Fatal(err)
	}
	return b.Bytes()
}
