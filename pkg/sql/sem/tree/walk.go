// Copyright 2015 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package tree

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
)

// Visitor defines methods that are called for nodes during an expression or statement walk.
type Visitor interface {
	// VisitPre is called for each node before recursing into that subtree. Upon return, if recurse
	// is false, the visit will not recurse into the subtree (and VisitPost will not be called for
	// this node).
	//
	// The returned Expr replaces the visited expression and can be used for rewriting expressions.
	// The function should NOT modify nodes in-place; it should make copies of nodes. The Walk
	// infrastructure will automatically make copies of parents as needed.
	VisitPre(expr Expr) (recurse bool, newExpr Expr)

	// VisitPost is called for each node after recursing into the subtree. The returned Expr
	// replaces the visited expression and can be used for rewriting expressions.
	//
	// The returned Expr replaces the visited expression and can be used for rewriting expressions.
	// The function should NOT modify nodes in-place; it should make and return copies of nodes. The
	// Walk infrastructure will automatically make copies of parents as needed.
	VisitPost(expr Expr) (newNode Expr)
}

// WalkExpr traverses the nodes in an expression.
//
// NOTE: Do not count on the walkStmt/WalkExpr machinery to visit all
// expressions contained in a query. Only a sub-set of all expressions are
// found by walkStmt and subsequently traversed. See the comment below on
// walkStmt for details.
func WalkExpr(v Visitor, expr Expr) (newExpr Expr, changed bool) {
	recurse, newExpr := v.VisitPre(expr)

	if recurse {
		newExpr = newExpr.Walk(v)
		newExpr = v.VisitPost(newExpr)
	}

	// We cannot use == because some Expr implementations are not comparable (e.g. DTuple)
	return newExpr, (reflect.ValueOf(expr) != reflect.ValueOf(newExpr))
}

func walkTableExpr(v Visitor, expr TableExpr) (newExpr TableExpr, changed bool) {
	newExpr = expr.WalkTableExpr(v)
	return newExpr, (reflect.ValueOf(expr) != reflect.ValueOf(newExpr))
}

// WalkExprConst is a variant of WalkExpr for visitors that do not modify the expression.
func WalkExprConst(v Visitor, expr Expr) {
	WalkExpr(v, expr)
	// TODO(radu): we should verify that WalkExpr returns changed == false. Unfortunately that
	// is not the case today because walking through non-pointer implementations of Expr (like
	// DBool, DTuple) causes new nodes to be created. We should make all Expr implementations be
	// pointers (which will also remove the need for using reflect.ValueOf above).
}

// walkableStmt is implemented by statements that are not "leaves" in a walk
// (i.e. can have child expressions or statements).
type walkableStmt interface {
	Statement
	walkStmt(Visitor) Statement
}

// walkStmt walks the entire parsed stmt calling WalkExpr on each
// expression, and replacing each expression with the one returned
// by WalkExpr.
//
// NOTE: Beware that walkStmt does not necessarily traverse all parts of a
// statement by itself. For example, it will not walk into Subquery nodes
// within a FROM clause or into a JoinCond. Walk's logic is pretty
// interdependent with the logic for constructing a query plan.
func walkStmt(v Visitor, stmt Statement) (newStmt Statement, changed bool) {
	walkable, ok := stmt.(walkableStmt)
	if !ok {
		return stmt, false
	}
	newStmt = walkable.walkStmt(v)
	return newStmt, (stmt != newStmt)
}

type simpleVisitor struct {
	fn  SimpleVisitFn
	err error
}

var _ Visitor = &simpleVisitor{}

func (v *simpleVisitor) VisitPre(expr Expr) (recurse bool, newExpr Expr) {
	if v.err != nil {
		return false, expr
	}
	recurse, newExpr, v.err = v.fn(expr)
	if v.err != nil {
		return false, expr
	}
	return recurse, newExpr
}

func (*simpleVisitor) VisitPost(expr Expr) Expr { return expr }

// SimpleVisitFn is a function that is run for every node in the VisitPre stage;
// see SimpleVisit.
type SimpleVisitFn func(expr Expr) (recurse bool, newExpr Expr, err error)

// SimpleVisit is a convenience wrapper for visitors that only have VisitPre
// code and don't return any results except an error. The given function is
// called in VisitPre for every node. The visitor stops as soon as an error is
// returned.
func SimpleVisit(expr Expr, preFn SimpleVisitFn) (Expr, error) {
	v := simpleVisitor{fn: preFn}
	newExpr, _ := WalkExpr(&v, expr)
	if v.err != nil {
		return nil, v.err
	}
	return newExpr, nil
}

// SimpleStmtVisit is a convenience wrapper for visitors that want to visit
// all part of a statement, only have VisitPre code and don't return
// any results except an error. The given function is called in VisitPre
// for every node. The visitor stops as soon as an error is returned.
func SimpleStmtVisit(stmt Statement, preFn SimpleVisitFn) (Statement, error) {
	v := simpleVisitor{fn: preFn}
	newStmt, changed := walkStmt(&v, stmt)
	if changed {
		return newStmt, nil
	}
	return stmt, nil
}

type debugVisitor struct {
	buf   bytes.Buffer
	level int
}

var _ Visitor = &debugVisitor{}

func (v *debugVisitor) VisitPre(expr Expr) (recurse bool, newExpr Expr) {
	v.level++
	fmt.Fprintf(&v.buf, "%*s", 2*v.level, " ")
	str := fmt.Sprintf("%#v\n", expr)
	// Remove "parser." to make the string more compact.
	str = strings.Replace(str, "parser.", "", -1)
	v.buf.WriteString(str)
	return true, expr
}

func (v *debugVisitor) VisitPost(expr Expr) Expr {
	v.level--
	return expr
}

// ExprDebugString generates a multi-line debug string with one node per line in
// Go format.
func ExprDebugString(expr Expr) string {
	v := debugVisitor{}
	WalkExprConst(&v, expr)
	return v.buf.String()
}

// StmtDebugString generates multi-line debug strings in Go format for the
// expressions that are part of the given statement.
func StmtDebugString(stmt Statement) string {
	v := debugVisitor{}
	walkStmt(&v, stmt)
	return v.buf.String()
}

// Silence any warnings if these functions are not used.
var _ = ExprDebugString
var _ = StmtDebugString
