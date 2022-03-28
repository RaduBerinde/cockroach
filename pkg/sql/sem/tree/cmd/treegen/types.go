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
	"go/types"
	"log"
	"sort"
	"strings"
)

type typesInfo struct {
	pkg *types.Package

	ifaces struct {
		Statement *types.Interface
		Expr      *types.Interface
		TableExpr *types.Interface
	}

	allTypes []types.Type
}

func makeTypesInfo(pkg *types.Package) typesInfo {
	ti := typesInfo{
		pkg: pkg,
	}

	for _, name := range pkg.Scope().Names() {
		obj := pkg.Scope().Lookup(name)
		if typ, ok := obj.(*types.TypeName); ok {
			ti.allTypes = append(ti.allTypes, typ.Type())
		}
	}
	sortTypes(ti.allTypes)
	ti.ifaces.Statement = ti.lookup("Statement").Underlying().(*types.Interface)
	ti.ifaces.Expr = ti.lookup("Expr").Underlying().(*types.Interface)
	ti.ifaces.TableExpr = ti.lookup("TableExpr").Underlying().(*types.Interface)
	return ti
}

func sortTypes(typs []types.Type) {
	sort.Slice(typs, func(i int, j int) bool {
		return strings.ToLower(typs[i].String()) < strings.ToLower(typs[j].String())
	})
}

func (ti *typesInfo) lookup(name string) types.Type {
	res := ti.pkg.Scope().Lookup(name)
	if res == nil {
		log.Fatalf("type '%s' not defined", name)
	}
	return res.Type()
}

// implementsStatement returns true if *typ implements tree.Statement.
func (ti *typesInfo) implementsStatement(typ types.Type) bool {
	return types.Implements(types.NewPointer(typ), ti.ifaces.Statement)
}

// isStatement returns true if typ is the tree.Statement interface.
func (ti *typesInfo) isStatement(typ types.Type) bool {
	return typ.Underlying().String() == ti.ifaces.Statement.String()
}

// extendsStatement returns true if typ is an interface that is or extends
// tree.Statement.
func (ti *typesInfo) extendsStatement(typ types.Type) bool {
	return isInterface(typ) && types.Implements(typ, ti.ifaces.Statement)
}

// implementsExpr returns true if typ is a type that is supposed to implement
// tree.Expr.
func (ti *typesInfo) implementsExpr(typ types.Type) bool {
	if isInterface(typ) {
		return false
	}
	// We can't check if the type implements Expr because the Walk methods are
	// generatedWalkFns and might not exist yet. Just check if the type implements
	// TypeCheck.
	return ti.hasTypeCheckMethod(typ) || ti.hasTypeCheckMethod(types.NewPointer(typ))
}

func (ti *typesInfo) hasTypeCheckMethod(typ types.Type) bool {
	return types.NewMethodSet(typ).Lookup(ti.pkg, "TypeCheck") != nil
}

// implementsTableExpr returns true if typ is a type that is supposed to
// implement tree.TableExpr.
func (ti *typesInfo) implementsTableExpr(typ types.Type) bool {
	return types.NewMethodSet(types.NewPointer(typ)).Lookup(ti.pkg, "tableExpr") != nil
}

// isTableExprIface returns true if typ is the tree.TableExpr interface.
func (ti *typesInfo) isTableExprIface(typ types.Type) bool {
	return typ.Underlying().String() == ti.ifaces.TableExpr.String()
}

// extendsExpr returns true if typ is the tree.Expr interface or an interface
// that extends tree.Expr.
func (ti *typesInfo) extendsExpr(typ types.Type) bool {
	return isInterface(typ) && types.Implements(typ, ti.ifaces.Expr)
}

// containsExprs returns true if the given type can contain a tree.Expr,
// tree.TableExpr, or tree.Statement. The method recurses through struct fields
// and pointers.
func (ti *typesInfo) containsExprs(t types.Type) bool {
	// Store the visited types to avoid cycles.
	visited := make(map[string]struct{})
	var containsExprsFn func(t types.Type) bool
	containsExprsFn = func(t types.Type) bool {
		if _, ok := visited[t.String()]; ok {
			return false
		}
		visited[t.String()] = struct{}{}
		if ti.extendsExpr(t) || ti.extendsStatement(t) || ti.isTableExprIface(t) {
			return true
		}
		switch t := t.Underlying().(type) {
		case *types.Slice:
			return containsExprsFn(t.Elem())

		case *types.Pointer:
			return containsExprsFn(t.Elem())

		case *types.Struct:
			for i := 0; i < t.NumFields(); i++ {
				if containsExprsFn(t.Field(i).Type()) {
					return true
				}
			}
			return false

		default:
			return false
		}
	}
	return containsExprsFn(t)
}

func isPointer(typ types.Type) bool {
	_, ok := typ.Underlying().(*types.Pointer)
	return ok
}

func isInterface(typ types.Type) bool {
	_, ok := typ.Underlying().(*types.Interface)
	return ok
}

func toPointer(typ types.Type) *types.Pointer {
	res, ok := typ.Underlying().(*types.Pointer)
	if !ok {
		log.Fatalf("type '%s' not a pointer", typ)
	}
	return res
}

// outType prints out the type name as it should appear in Go code, inside the
// tree package.
func outType(t types.Type) string {
	return types.TypeString(t, func(pkg *types.Package) string {
		if pkg.Name() == "tree" {
			// Types from the tree package are not qualified.
			return ""
		}
		return pkg.Name()
	})
}
