// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package explain

import (
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/sql/opt/exec"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/sqlbase"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/cockroach/pkg/util/treeprinter"
	"github.com/stretchr/testify/require"
)

// TestFactory is a general API test for Factory. It is not intended as an
// exhaustive test of all factory Construct methods.
func TestFactory(t *testing.T) {
	f := NewFactory(exec.StubFactory{})

	v, err := f.ConstructValues(
		[][]tree.TypedExpr{
			{tree.NewDInt(1), tree.NewDString("one")},
			{tree.NewDInt(2), tree.NewDString("two")},
			{tree.NewDInt(3), tree.NewDString("three")},
		},
		sqlbase.ResultColumns{
			{Name: "number", Typ: types.Int},
			{Name: "word", Typ: types.String},
		},
	)
	require.NoError(t, err)
	f.AnnotateNode(v, "extra property", "1.0")
	f.AnnotateNode(v, "another property", "10.0")

	plan, err := f.ConstructPlan(v, nil /* subqueries */, nil /* cascades */, nil /* checks */)
	require.NoError(t, err)
	p := plan.(*Plan)

	tp := treeprinter.New()
	printTree(p.Root, tp)
	exp := `
values
 ├── columns: (number int, word string)
 ├── ordering: 
 ├── size: 2 columns, 3 rows
 ├── extra property: 1.0
 └── another property: 10.0
`
	require.Equal(t, strings.TrimLeft(exp, "\n"), tp.String())
}

func printTree(n *Node, tp treeprinter.Node) {
	tp = tp.Child(n.Name())
	tp.Childf("columns: %s", n.Columns().String(true /* printTyeps */))
	tp.Childf("ordering: %v", n.Ordering().String(n.Columns()))
	for i := 0; i < n.AttributeCount(); i++ {
		key, value := n.Attribute(i)
		tp.Childf("%s: %s", key, value)
	}
	for i := 0; i < n.ChildCount(); i++ {
		printTree(n.Child(i), tp)
	}
}
