// extract copies named top-level declarations of a Go package, with every declaration of the
// same package they reference (transitively), into one file.
//
//	extract -dir SRC/pkg -pkg name -out file.go Name1 Name2 ...
//
// Methods of a kept type are kept too. Imports are copied as the source files had them;
// run goimports afterwards to drop the unused ones.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type decl struct {
	node  ast.Decl
	spec  ast.Spec // for a grouped GenDecl: the one spec
	file  *ast.File
	pos   token.Pos
	names []string
}

func main() {
	dir := flag.String("dir", "", "package directory")
	pkg := flag.String("pkg", "", "package name of the output")
	out := flag.String("out", "", "output file")
	skip := flag.String("skip", "", "comma-separated names never to pull in")
	flag.Parse()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, *dir, func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	byName := map[string][]*decl{}
	methods := map[string][]*decl{}
	var files []*ast.File
	for _, p := range pkgs {
		for _, f := range p.Files {
			files = append(files, f)
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					x := &decl{node: d, file: f, pos: d.Pos()}
					if d.Recv != nil {
						t := recvType(d.Recv.List[0].Type)
						methods[t] = append(methods[t], x)
						continue
					}
					x.names = []string{d.Name.Name}
					byName[d.Name.Name] = append(byName[d.Name.Name], x)
				case *ast.GenDecl:
					if d.Tok == token.IMPORT {
						continue
					}
					// const/var groups with iota stay whole; others split per spec.
					whole := d.Tok == token.CONST && d.Lparen.IsValid()
					if whole {
						x := &decl{node: d, file: f, pos: d.Pos()}
						for _, s := range d.Specs {
							for _, n := range s.(*ast.ValueSpec).Names {
								x.names = append(x.names, n.Name)
							}
						}
						for _, n := range x.names {
							byName[n] = append(byName[n], x)
						}
						continue
					}
					for _, s := range d.Specs {
						x := &decl{node: d, spec: s, file: f, pos: s.Pos()}
						switch s := s.(type) {
						case *ast.TypeSpec:
							x.names = []string{s.Name.Name}
						case *ast.ValueSpec:
							for _, n := range s.Names {
								x.names = append(x.names, n.Name)
							}
						}
						for _, n := range x.names {
							byName[n] = append(byName[n], x)
						}
					}
				}
			}
		}
	}
	skipped := map[string]bool{}
	for _, s := range strings.Split(*skip, ",") {
		if s != "" {
			skipped[s] = true
		}
	}
	kept := map[*decl]bool{}
	seen := map[string]bool{}
	queue := append([]string(nil), flag.Args()...)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if seen[n] || skipped[n] {
			continue
		}
		seen[n] = true
		ds, ok := byName[n]
		if !ok {
			if len(flag.Args()) > 0 && contains(flag.Args(), n) {
				fmt.Fprintln(os.Stderr, "not found:", n)
			}
			continue
		}
		for _, d := range ds {
			if kept[d] {
				continue
			}
			kept[d] = true
			queue = append(queue, idents(d)...)
			for _, m := range methods[n] {
				if !kept[m] {
					kept[m] = true
					queue = append(queue, idents(m)...)
				}
			}
		}
	}
	var list []*decl
	for d := range kept {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool {
		fi, fj := fset.File(list[i].pos).Name(), fset.File(list[j].pos).Name()
		if fi != fj {
			return fi < fj
		}
		return list[i].pos < list[j].pos
	})
	imports := map[string]string{}
	for _, d := range list {
		for _, im := range d.file.Imports {
			k := im.Path.Value
			if im.Name != nil {
				k = im.Name.Name + " " + k
			}
			imports[k] = ""
		}
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code copied from mikan (%s) by tools/extract: the declarations the bot needs.\n\npackage %s\n\n", filepath.Base(*dir), *pkg)
	if len(imports) > 0 {
		b.WriteString("import (\n")
		var ks []string
		for k := range imports {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			b.WriteString("\t" + k + "\n")
		}
		b.WriteString(")\n\n")
	}
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
	for _, d := range list {
		if g, ok := d.node.(*ast.GenDecl); ok && d.spec != nil {
			doc := g.Doc
			if len(g.Specs) > 1 {
				doc = nil
			}
			if ts, ok := d.spec.(*ast.TypeSpec); ok && ts.Doc != nil {
				doc = ts.Doc
			}
			if vs, ok := d.spec.(*ast.ValueSpec); ok && vs.Doc != nil {
				doc = vs.Doc
			}
			if doc != nil {
				for _, c := range doc.List {
					b.WriteString(c.Text + "\n")
				}
			}
			b.WriteString(g.Tok.String() + " ")
			if err := cfg.Fprint(&b, fset, stripDoc(d.spec)); err != nil {
				panic(err)
			}
			b.WriteString("\n\n")
			continue
		}
		if err := cfg.Fprint(&b, fset, &printer.CommentedNode{Node: d.node, Comments: d.file.Comments}); err != nil {
			panic(err)
		}
		b.WriteString("\n\n")
	}
	src, err := format.Source(b.Bytes())
	if err != nil {
		os.WriteFile(*out, b.Bytes(), 0o644)
		panic(err)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		panic(err)
	}
}

func stripDoc(s ast.Spec) ast.Spec {
	switch s := s.(type) {
	case *ast.TypeSpec:
		c := *s
		c.Doc = nil
		return &c
	case *ast.ValueSpec:
		c := *s
		c.Doc = nil
		return &c
	}
	return s
}

func recvType(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvType(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvType(t.X)
	case *ast.IndexListExpr:
		return recvType(t.X)
	}
	return ""
}

// idents: every bare identifier in d (selectors' right sides are left out).
func idents(d *decl) []string {
	var out []string
	var n ast.Node = d.node
	if d.spec != nil {
		n = d.spec
	}
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		ast.Inspect(n, func(x ast.Node) bool { return visit(x, walk, &out) })
	}
	if f, ok := n.(*ast.FuncDecl); ok {
		// The function's own name, a method's above all, is no reference.
		if f.Recv != nil {
			walk(f.Recv)
		}
		walk(f.Type)
		if f.Body != nil {
			walk(f.Body)
		}
		return out
	}
	walk(n)
	return out
}

func visit(x ast.Node, walk func(ast.Node), out *[]string) bool {
	switch x := x.(type) {
	case *ast.Field:
		// Field and parameter names are no references.
		if x.Type != nil {
			walk(x.Type)
		}
		return false
	case *ast.KeyValueExpr:
		// A struct literal's keys are field names; a map's are expressions.
		if _, ok := x.Key.(*ast.Ident); !ok {
			walk(x.Key)
		}
		walk(x.Value)
		return false
	}
	switch x := x.(type) {
	case *ast.SelectorExpr:
		// The right side is a field, a method or another package's name.
		walk(x.X)
		return false
	case *ast.Ident:
		*out = append(*out, x.Name)
	}
	return true
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
