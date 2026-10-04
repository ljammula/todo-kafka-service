package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// maxComplexity is the team limit on decision points in one function
// (cyclomatic complexity, counted the way gocyclo counts it).
const maxComplexity = 25

// knownOver lists the functions that were already over the limit when this
// check was added, with their complexity then. Each may shrink but not grow;
// delete an entry once its function is back under the limit. A new function
// never belongs here: split it.
var knownOver = map[string]int{
	"main.go:handleTodos": 36,
}

// TestFunctionComplexity fails when a function in this repository has more
// than maxComplexity decision points. It needs no tool beyond the Go
// toolchain, so it runs wherever `go test` does.
func TestFunctionComplexity(t *testing.T) {
	seen := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := filepath.ToSlash(path) + ":" + funcName(fn)
			seen[key] = true
			checkComplexity(t, key, complexity(fn))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key := range knownOver {
		if !seen[key] {
			t.Errorf("knownOver lists %s, which no longer exists: delete the entry", key)
		}
	}
}

func checkComplexity(t *testing.T, key string, got int) {
	t.Helper()
	was, known := knownOver[key]
	switch {
	case known && got > was:
		t.Errorf("%s: %d decision points, up from %d; it was already over the limit of %d and must not grow", key, got, was, maxComplexity)
	case known && got <= maxComplexity:
		t.Errorf("%s: now %d decision points, within the limit: delete its knownOver entry", key, got)
	case !known && got > maxComplexity:
		t.Errorf("%s: %d decision points, over the limit of %d; split it into smaller functions", key, got, maxComplexity)
	}
}

// funcName is the function's name, with its receiver type for a method.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if ident, ok := recv.(*ast.Ident); ok {
		return ident.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// complexity is 1 plus one for each if, for, range, non-default case and
// select clause, and each && or || in the function.
func complexity(fn *ast.FuncDecl) int {
	count := 1
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			count++
		case *ast.CaseClause:
			if n.List != nil { // a default clause adds no path
				count++
			}
		case *ast.CommClause:
			if n.Comm != nil {
				count++
			}
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				count++
			}
		}
		return true
	})
	return count
}
