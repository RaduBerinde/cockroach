// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package sql

import (
	"context"
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/sql/opt/exec/explain"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/sqlbase"
)

type explainNewPlanNode struct {
	flags explain.Flags
	plan  *explain.Plan
	run   explainNewPlanNodeRun

	columns sqlbase.ResultColumns
}

type explainNewPlanNodeRun struct {
	results *valuesNode
}

func (e *explainNewPlanNode) startExec(params runParams) error {
	ob := explain.NewOutputBuilder(e.flags)

	realPlan := e.plan.WrappedPlan.(*planTop)
	distribution, willVectorize := explainGetDistributedAndVectorized(params, &realPlan.planComponents)
	ob.AddField("distribution", distribution.String())
	ob.AddField("vectorized", fmt.Sprintf("%t", willVectorize))

	var walk func(n *explain.Node)
	walk = func(n *explain.Node) {
		ob.EnterNode(n.Name(), n.Columns(), n.Ordering())
		for i := 0; i < n.AttributeCount(); i++ {
			field, val := n.Attribute(i)
			ob.AddField(field, val)
		}

		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
		ob.LeaveNode()
	}
	walk(e.plan.Root)

	v := params.p.newContainerValuesNode(e.columns, 0)
	for _, row := range ob.BuildExplainRows() {
		if _, err := v.rows.AddRow(params.ctx, row); err != nil {
			return err
		}
	}
	e.run.results = v

	return nil
}

func (e *explainNewPlanNode) Next(params runParams) (bool, error) { return e.run.results.Next(params) }
func (e *explainNewPlanNode) Values() tree.Datums                 { return e.run.results.Values() }

func (e *explainNewPlanNode) Close(ctx context.Context) {
	//e.plan.Root.WrappedNode().(*planNode).Close(ctx)
	//for i := range e.plan.subqueryPlans {
	//	e.plan.subqueryPlans[i].plan.Close(ctx)
	//}
	//for i := range e.plan.checkPlans {
	//	e.plan.checkPlans[i].plan.Close(ctx)
	//}
	if e.run.results != nil {
		e.run.results.Close(ctx)
	}
}
