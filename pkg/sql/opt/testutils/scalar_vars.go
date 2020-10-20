// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package testutils

import (
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/sql/opt"
	"github.com/cockroachdb/cockroach/pkg/sql/parser"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/errors"
)

// ScalarVars is a helper used to populate the metadata with specified columns,
// useful for tests involving scalar expressions.
type ScalarVars struct {
	cols []colInfo
}

type colInfo struct {
	name    string
	typ     *types.T
	notNull bool
}

// MakeScalarVars parses a variables definition string and returns a ScalarVars
// that stores the information. AddToMetadata can then be used to add the
// columns to opt.Metadata.
//
// The definition string is of the form:
//   "var1 type1 [not null], var2 type2 [not null], ..."
//
func MakeScalarVars(vars string) (ScalarVars, error) {
	var cols []colInfo
	// We use the same syntax that is used with CREATE TABLE, so reuse the parsing
	// logic.
	stmt, err := parser.ParseOne(fmt.Sprintf("CREATE TABLE foo (%s)", vars))
	if err != nil {
		return ScalarVars{}, errors.Wrapf(err, "invalid vars definition '%s'", vars)
	}
	ct := stmt.AST.(*tree.CreateTable)
	for _, d := range ct.Defs {
		cd, ok := d.(*tree.ColumnTableDef)
		if !ok {
			return ScalarVars{}, errors.Newf("invalid vars definition '%s'", vars)
		}
		if cd.PrimaryKey.IsPrimaryKey || cd.Unique || cd.DefaultExpr.Expr != nil ||
			len(cd.CheckExprs) > 0 || cd.References.Table != nil || cd.Computed.Computed ||
			cd.Family.Name != "" {
			return ScalarVars{}, errors.Newf("invalid vars definition '%s'", vars)
		}
		typ := tree.MustBeStaticallyKnownType(cd.Type)
		cols = append(cols, colInfo{
			name:    string(cd.Name),
			typ:     typ,
			notNull: cd.Nullable.Nullability == tree.NotNull,
		})
	}
	return ScalarVars{cols: cols}, nil
}

// AddToMetadata adds the columns to the metadata. Returns the set of not-null
// columns.
func (sc ScalarVars) AddToMetadata(md *opt.Metadata) (notNullCols opt.ColSet) {
	for _, c := range sc.cols {
		id := md.AddColumn(c.name, c.typ)
		if c.notNull {
			notNullCols.Add(id)
		}
	}
	return notNullCols
}
