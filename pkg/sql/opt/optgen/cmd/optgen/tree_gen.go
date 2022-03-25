// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/sql/opt/optgen/lang"
)

type treeDefsGen struct {
	compiled *lang.CompiledExpr
	w        *matchWriter
}

func (g *treeDefsGen) generate(compiled *lang.CompiledExpr, w io.Writer) {
	g.compiled = compiled
	g.w = &matchWriter{writer: w}

	g.w.write("package tree\n\n")

	g.w.nestIndent("import (\n")
	//g.w.writeIndent("\"github.com/cockroachdb/cockroach/pkg/sql/catalog/descpb\"\n")
	g.w.unnest(")\n\n")

	for _, define := range g.compiled.Defines {
		g.genStruct(define)
	}
}

func (g *treeDefsGen) genStruct(define *lang.DefineExpr) {
	generateComments(g.w.writer, define.Comments, string(define.Name), string(define.Name))
	g.w.write("type %s struct {\n", define.Name)

	// Generate child fields.
	for i, field := range define.Fields {
		// Generate comment for the struct field.
		if len(field.Comments) != 0 {
			if i != 0 {
				g.w.write("\n")
			}
			generateComments(g.w.writer, field.Comments, string(field.Name), string(field.Name))
		}

		// If field's name is "_", then use Go embedding syntax.
		if isEmbeddedField(field) {
			g.w.write("  %s\n", field.Type)
		} else {
			g.w.write("  %s %s\n", field.Name, field.Type)
		}
	}
	g.w.write("}\n\n")
}

type treeTypeInfo struct {
	name        string
	isExpr      bool
	isStatement bool
	isSlice     bool
	isExprSlice bool
}

var specialTypes = []treeTypeInfo{
	{
		name:   "Expr",
		isExpr: true,
	},
	{
		name:        "Exprs",
		isExprSlice: true,
	},
}

type treeWalkGen struct {
	compiled *lang.CompiledExpr
	w        *matchWriter
	types    map[string]treeTypeInfo
}

func (g *treeWalkGen) generate(compiled *lang.CompiledExpr, w io.Writer) {
	g.compiled = compiled
	g.w = &matchWriter{writer: w}

	g.types = make(map[string]treeTypeInfo)
	for _, info := range specialTypes {
		g.types[info.name] = info
	}
	for _, define := range compiled.Defines {
		info := treeTypeInfo{
			name: string(define.Name),
		}
		switch {
		case define.Tags.Contains("Statement"):
			info.isStatement = true
		case define.Tags.Contains("Expr"):
			info.isExpr = true
		}
		if _, ok := g.types[info.name]; ok {
			panic(fmt.Sprintf("info for %s already populated", info.name))
		}
		g.types[info.name] = info
	}

	g.w.write("package tree\n\n")

	g.w.nestIndent("import (\n")
	//g.w.writeIndent("\"github.com/cockroachdb/cockroach/pkg/sql/catalog/descpb\"\n")
	g.w.unnest(")\n\n")

	for _, define := range g.compiled.Defines.WithTag("Statement") {
		g.w.write("var _ walkableStmt = (*%s)(nil)\n\n", define.Name)
		g.genCopyNode(define)
		g.genWalkStmt(define)
	}
}

func (g *treeWalkGen) genCopyNode(define *lang.DefineExpr) {
	name := define.Name
	g.w.write("// copyNode makes a copy of this node without recursing in any child nodes.\n")
	g.w.nest("func (stmt *%s) copyNode() *%s {", name, name)
	g.w.writeIndent("stmtCopy := *stmt\n")
	for _, field := range define.Fields {
		if g.isSlice(field) {
			g.w.writeIndent("stmtCopy.%s = append(%s(nil), stmt.%s...)\n", field.Name, field.Type, field.Name)
		}
	}
	g.w.writeIndent("return &stmtCopy\n")
	g.w.unnest("}\n\n")
}

func (g *treeWalkGen) genWalkStmt(define *lang.DefineExpr) {
	g.w.write("// walkStmt is part of the walkableStmt interface.\n")
	g.w.nest("func (stmt *%s) walkStmt(v Visitor) Statement {\n", define.Name)
	g.w.writeIndent("ret := stmt\n")
	for _, field := range define.Fields {
		switch {
		case g.isEpxr(field):
			g.w.nestIndent("if e, changed := WalkExpr(v, stmt.%s); changed {\n", field.Name)
			g.w.nestIndent("if ret == stmt {\n")
			g.w.writeIndent("ret = stmt.copyNode()\n")
			g.w.unnest("}\n")
			g.w.writeIndent("ret.%s = e\n", field.Name)
			g.w.unnest("}\n")

		case g.isExprSlice(field):
			g.w.nestIndent("for i, expr := range stmt.%s {\n", field.Name)
			g.w.writeIndent("e, changed := WalkExpr(v, expr)\n")
			g.w.nestIndent("if changed {\n")
			g.w.nestIndent("if ret == stmt {\n")
			g.w.writeIndent("ret = stmt.copyNode()\n")
			g.w.unnest("}\n")
			g.w.writeIndent("ret.%s[i] = e\n", field.Name)
			g.w.unnest("}\n")
			g.w.unnest("}\n")
		}
	}
	g.w.writeIndent("return ret\n")
	g.w.unnest("}\n\n")
}

func (g *treeWalkGen) typeInfo(field *lang.DefineFieldExpr) treeTypeInfo {
	return g.types[string(field.Type)]
}

func (g *treeWalkGen) isEpxr(field *lang.DefineFieldExpr) bool {
	return g.typeInfo(field).isExpr
}

// isExprSlice returns true if the given field is a slice of expressions.
func (g *treeWalkGen) isExprSlice(field *lang.DefineFieldExpr) bool {
	if g.typeInfo(field).isExprSlice {
		return true
	}
	if strings.HasPrefix(string(field.Type), "[]") {
		return g.types[strings.TrimPrefix(string(field.Type), "[]")].isExprSlice
	}
	return false
}

// isExprList returns true if the given field is any kind of slice.
func (g *treeWalkGen) isSlice(field *lang.DefineFieldExpr) bool {
	return strings.HasPrefix(string(field.Type), "[]") || g.isExprSlice(field)
}
