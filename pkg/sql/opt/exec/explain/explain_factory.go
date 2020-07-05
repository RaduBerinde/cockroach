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
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/sql/opt"
	"github.com/cockroachdb/cockroach/pkg/sql/opt/cat"
	"github.com/cockroachdb/cockroach/pkg/sql/opt/constraint"
	"github.com/cockroachdb/cockroach/pkg/sql/opt/exec"
	"github.com/cockroachdb/cockroach/pkg/sql/opt/invertedexpr"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/sqlbase"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/cockroach/pkg/util"
	"github.com/cockroachdb/errors"
)

// Factory implements exec.ExplainFactory.
type Factory struct {
	flags          Flags
	wrappedFactory exec.Factory
}

var _ exec.ExplainFactory = &Factory{}

type Node struct {
	f        *Factory
	name     string
	columns  sqlbase.ResultColumns
	ordering sqlbase.ColumnOrdering
	attrs    []keyValue

	children []*Node

	wrappedNode exec.Node
}

var _ exec.Node = &Node{}

type keyValue struct {
	key   string
	value string
}

func (n *Node) ChildCount() int {
	return len(n.children)
}

func (n *Node) Child(idx int) *Node {
	return n.children[idx]
}

func (n *Node) AttributeCount() int {
	return len(n.attrs)
}

func (n *Node) Attribute(idx int) (key, value string) {
	return n.attrs[idx].key, n.attrs[idx].value
}

func (n *Node) Name() string {
	return n.name
}

func (n *Node) Columns() sqlbase.ResultColumns {
	return n.columns
}

func (n *Node) Ordering() sqlbase.ColumnOrdering {
	return n.ordering
}

func (n *Node) WrappedNode() exec.Node {
	return n.wrappedNode
}

func (f *Factory) newNode(
	name string, columns sqlbase.ResultColumns, ordering exec.OutputOrdering, children ...*Node,
) *Node {
	return &Node{
		f:        f,
		name:     name,
		columns:  columns,
		ordering: sqlbase.ColumnOrdering(ordering),
		children: children,
	}
}

func (n *Node) attr(key, value string) {
	n.attrs = append(n.attrs, keyValue{key: key, value: value})
}

func (n *Node) attrf(key, format string, args ...interface{}) {
	n.attrs = append(n.attrs, keyValue{key: key, value: fmt.Sprintf(format, args...)})
}

func (n *Node) vattr(key, value string) {
	if n.f.flags.Verbose {
		n.attrs = append(n.attrs, keyValue{key: key, value: value})
	}
}
func (n *Node) vattrf(key, format string, args ...interface{}) {
	if n.f.flags.Verbose {
		n.attrs = append(n.attrs, keyValue{key: key, value: fmt.Sprintf(format, args...)})
	}
}

type Plan struct {
	Root        *Node
	Subqueries  []exec.Subquery
	Cascades    []exec.Cascade
	Checks      []*Node
	WrappedPlan exec.Plan
}

var _ exec.Plan = &Plan{}

func NewFactory(wrappedFactory exec.Factory, flags Flags) *Factory {
	return &Factory{
		flags:          flags,
		wrappedFactory: wrappedFactory,
	}
}

func (f *Factory) AnnotateNode(n exec.Node, key, value string) {
	n.(*Node).attr(key, value)
}

func (n *Node) setWrapped(w exec.Node, err error) (exec.Node, error) {
	if err != nil {
		return nil, err
	}
	n.wrappedNode = w
	return n, nil
}

func (f *Factory) ConstructValues(
	rows [][]tree.TypedExpr, cols sqlbase.ResultColumns,
) (exec.Node, error) {
	n := f.newNode("values", cols, nil /* ordering */)
	n.attrf(
		"size", "%d column%s, %d row%s",
		len(cols), util.Pluralize(int64(len(cols))),
		len(rows), util.Pluralize(int64(len(rows))),
	)

	return n.setWrapped(f.wrappedFactory.ConstructValues(rows, cols))
}

func (f *Factory) ConstructScan(
	table cat.Table, index cat.Index, params exec.ScanParams, reqOrdering exec.OutputOrdering,
) (exec.Node, error) {
	name := "scan"
	if params.Reverse {
		name = "revscan"
	}
	n := f.newNode(name, tableColumns(table, params.NeededCols), reqOrdering)
	partial := ""
	if _, isPartial := index.Predicate(); isPartial {
		partial = " (partial index)"
	}
	n.attrf("table", "%s@%s%s", table.Name(), index.Name(), partial)

	switch {
	case params.InvertedConstraint != nil:
		// TODO(radu): show the actual spans in verbose mode?
		num := len(params.InvertedConstraint)
		n.attrf("spans", "%d span%s", num, util.Pluralize(int64(num)))

	case params.IndexConstraint != nil:
		if f.flags.Verbose {
			n.attr("spans", params.IndexConstraint.Spans.String())
		} else {
			// TODO(radu): maybe say "single-key spans" when that is the case.
			num := params.IndexConstraint.Spans.Count()
			n.attrf("spans", "%d span%s", num, util.Pluralize(int64(num)))
		}

	case params.HardLimit > 0:
		n.attr("spans", "LIMITED SCAN")

	default:
		n.attr("spans", "FULL SCAN")
	}

	if params.HardLimit > 0 {
		n.attrf("limit", "%d", params.HardLimit)
	}

	if params.Parallelize {
		// TODO(radu): should be vattr.
		n.attr("parallel", "")
	}

	if params.Locking != nil {
		strength := sqlbase.ToScanLockingStrength(params.Locking.Strength)
		waitPolicy := sqlbase.ToScanLockingWaitPolicy(params.Locking.WaitPolicy)
		if strength != sqlbase.ScanLockingStrength_FOR_NONE {
			// TODO(radu): should be vattr.
			n.attr("locking strength", strength.PrettyString())
		}
		if waitPolicy != sqlbase.ScanLockingWaitPolicy_BLOCK {
			// TODO(radu): should be vattr.
			n.attr("locking wait policy", waitPolicy.PrettyString())
		}
	}

	return n.setWrapped(f.wrappedFactory.ConstructScan(table, index, params, reqOrdering))
}

func (f *Factory) ConstructFilter(
	input exec.Node, filter tree.TypedExpr, reqOrdering exec.OutputOrdering,
) (exec.Node, error) {
	inputNode := input.(*Node)
	n := f.newNode("filter", inputNode.Columns(), reqOrdering, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructFilter(
		inputNode.WrappedNode(), filter, reqOrdering,
	))
}

func (f *Factory) ConstructInvertedFilter(
	input exec.Node, invFilter *invertedexpr.SpanExpression, invColumn exec.NodeColumnOrdinal,
) (exec.Node, error) {
	inputNode := input.(*Node)
	n := f.newNode("inverted-filter", inputNode.Columns(), nil /* ordering */, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructInvertedFilter(
		inputNode.WrappedNode(), invFilter, invColumn,
	))
}

func (f *Factory) ConstructSimpleProject(
	input exec.Node,
	cols []exec.NodeColumnOrdinal,
	colNames []string,
	reqOrdering exec.OutputOrdering,
) (exec.Node, error) {
	inputNode := input.(*Node)
	// TODO(radu): name this "project"?
	n := f.newNode("render", projectCols(inputNode.Columns(), cols, colNames), reqOrdering, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructSimpleProject(
		inputNode.WrappedNode(), cols, colNames, reqOrdering,
	))
}

func projectCols(
	input sqlbase.ResultColumns, ordinals []exec.NodeColumnOrdinal, colNames []string,
) sqlbase.ResultColumns {
	columns := make(sqlbase.ResultColumns, len(ordinals))
	for i, ord := range ordinals {
		columns[i] = input[ord]
		if colNames != nil {
			columns[i].Name = colNames[i]
		}
	}
	return columns
}

func (f *Factory) ConstructRender(
	input exec.Node,
	columns sqlbase.ResultColumns,
	exprs tree.TypedExprs,
	reqOrdering exec.OutputOrdering,
) (exec.Node, error) {
	inputNode := input.(*Node)
	n := f.newNode("render", columns, reqOrdering, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructRender(
		inputNode.WrappedNode(), columns, exprs, reqOrdering,
	))
}

func (f *Factory) ConstructHashJoin(
	joinType sqlbase.JoinType,
	left, right exec.Node,
	leftEqCols, rightEqCols []exec.NodeColumnOrdinal,
	leftEqColsAreKey, rightEqColsAreKey bool,
	extraOnCond tree.TypedExpr,
) (exec.Node, error) {
	leftNode := left.(*Node)
	rightNode := right.(*Node)
	name := "hash-join"
	if len(leftEqCols) == 0 {
		name = "cross-join"
	}
	columns := joinColumns(joinType, leftNode.Columns(), rightNode.Columns())
	n := f.newNode(name, columns, nil /* ordering */, leftNode, rightNode)
	return n.setWrapped(f.wrappedFactory.ConstructHashJoin(
		joinType,
		leftNode.WrappedNode(), rightNode.WrappedNode(),
		leftEqCols, rightEqCols,
		leftEqColsAreKey, rightEqColsAreKey,
		extraOnCond,
	))
}

func (f *Factory) ConstructApplyJoin(
	joinType sqlbase.JoinType,
	left exec.Node,
	rightColumns sqlbase.ResultColumns,
	onCond tree.TypedExpr,
	planRightSideFn exec.ApplyJoinPlanRightSideFn,
) (exec.Node, error) {
	leftNode := left.(*Node)
	columns := joinColumns(joinType, leftNode.Columns(), rightColumns)
	n := f.newNode("apply-join", columns, nil /* ordering */, leftNode)
	return n.setWrapped(f.wrappedFactory.ConstructApplyJoin(
		joinType, leftNode.WrappedNode(), rightColumns, onCond, planRightSideFn,
	))
}

func (f *Factory) ConstructMergeJoin(
	joinType sqlbase.JoinType,
	left, right exec.Node,
	onCond tree.TypedExpr,
	leftOrdering, rightOrdering sqlbase.ColumnOrdering,
	reqOrdering exec.OutputOrdering,
	leftEqColsAreKey, rightEqColsAreKey bool,
) (exec.Node, error) {
	leftNode := left.(*Node)
	rightNode := right.(*Node)
	columns := joinColumns(joinType, leftNode.Columns(), rightNode.Columns())
	n := f.newNode("merge-join", columns, nil /* ordering */, leftNode, rightNode)
	return n.setWrapped(f.wrappedFactory.ConstructMergeJoin(
		joinType, leftNode.WrappedNode(), rightNode.WrappedNode(),
		onCond, leftOrdering, rightOrdering,
		reqOrdering,
		leftEqColsAreKey, rightEqColsAreKey,
	))
}

func (f *Factory) ConstructInterleavedJoin(
	joinType sqlbase.JoinType,
	leftTable cat.Table,
	leftIndex cat.Index,
	leftParams exec.ScanParams,
	leftFilter tree.TypedExpr,
	rightTable cat.Table,
	rightIndex cat.Index,
	rightParams exec.ScanParams,
	rightFilter tree.TypedExpr,
	leftIsAncestor bool,
	onCond tree.TypedExpr,
	reqOrdering exec.OutputOrdering,
) (exec.Node, error) {
	columns := joinColumns(
		joinType,
		tableColumns(leftTable, leftParams.NeededCols),
		tableColumns(rightTable, rightParams.NeededCols),
	)
	n := f.newNode("interleaved-join", columns, reqOrdering)
	return n.setWrapped(f.wrappedFactory.ConstructInterleavedJoin(
		joinType,
		leftTable, leftIndex, leftParams, leftFilter,
		rightTable, rightIndex, rightParams, rightFilter,
		leftIsAncestor, onCond, reqOrdering,
	))
}

func (f *Factory) ConstructGroupBy(
	input exec.Node,
	groupCols []exec.NodeColumnOrdinal,
	groupColOrdering sqlbase.ColumnOrdering,
	aggregations []exec.AggInfo,
	reqOrdering exec.OutputOrdering,
) (exec.Node, error) {
	inputNode := input.(*Node)
	columns := projectCols(inputNode.Columns(), groupCols, nil /* colNames */)
	for i := range aggregations {
		columns = append(columns, sqlbase.ResultColumn{
			Name: aggregations[i].FuncName,
			Typ:  aggregations[i].ResultType,
		})
	}
	n := f.newNode("group", columns, reqOrdering, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructGroupBy(
		inputNode.WrappedNode(), groupCols, groupColOrdering, aggregations, reqOrdering,
	))
}

func (f *Factory) ConstructScalarGroupBy(
	input exec.Node, aggregations []exec.AggInfo,
) (exec.Node, error) {
	inputNode := input.(*Node)
	columns := make(sqlbase.ResultColumns, len(aggregations))
	for i := range aggregations {
		columns[i].Name = aggregations[i].FuncName
		columns[i].Typ = aggregations[i].ResultType
	}
	n := f.newNode("group", columns, nil /* reqOrdering */, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructScalarGroupBy(
		inputNode.WrappedNode(), aggregations,
	))
}

func (f *Factory) ConstructDistinct(
	input exec.Node,
	distinctCols, orderedCols exec.NodeColumnOrdinalSet,
	reqOrdering exec.OutputOrdering,
	nullsAreDistinct bool,
	errorOnDup string,
) (exec.Node, error) {
	inputNode := input.(*Node)
	n := f.newNode("distinct", inputNode.Columns(), reqOrdering, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructDistinct(
		inputNode.WrappedNode(), distinctCols, orderedCols,
		reqOrdering, nullsAreDistinct, errorOnDup,
	))
}

func (f *Factory) ConstructSetOp(
	typ tree.UnionType, all bool, left, right exec.Node,
) (exec.Node, error) {
	leftNode := left.(*Node)
	rightNode := right.(*Node)
	n := f.newNode("set-op", leftNode.Columns(), nil /* reqOrdering */)
	return n.setWrapped(f.wrappedFactory.ConstructSetOp(
		typ, all, leftNode.WrappedNode(), rightNode.WrappedNode(),
	))
}

func (f *Factory) ConstructSort(
	input exec.Node, ordering sqlbase.ColumnOrdering, alreadyOrderedPrefix int,
) (exec.Node, error) {
	inputNode := input.(*Node)
	n := f.newNode("sort", inputNode.Columns(), exec.OutputOrdering(ordering), inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructSort(
		inputNode.WrappedNode(), ordering, alreadyOrderedPrefix,
	))
}

func (f *Factory) ConstructOrdinality(input exec.Node, colName string) (exec.Node, error) {
	inputNode := input.(*Node)
	columns := append(sqlbase.ResultColumns(nil), inputNode.Columns()...)
	columns = append(columns, sqlbase.ResultColumn{
		Name: colName,
		Typ:  types.Int,
	})
	n := f.newNode("ordinality", columns, nil /* reqOrdering */)
	return n.setWrapped(f.wrappedFactory.ConstructOrdinality(
		inputNode.WrappedNode(), colName,
	))
}

func (f *Factory) ConstructIndexJoin(
	input exec.Node,
	table cat.Table,
	keyCols []exec.NodeColumnOrdinal,
	tableCols exec.TableColumnOrdinalSet,
	reqOrdering exec.OutputOrdering,
) (exec.Node, error) {
	inputNode := input.(*Node)
	n := f.newNode("index-join", tableColumns(table, tableCols), reqOrdering, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructIndexJoin(
		inputNode.WrappedNode(), table, keyCols, tableCols, reqOrdering,
	))
}

func (f *Factory) ConstructLookupJoin(
	joinType sqlbase.JoinType,
	input exec.Node,
	table cat.Table,
	index cat.Index,
	eqCols []exec.NodeColumnOrdinal,
	eqColsAreKey bool,
	lookupCols exec.TableColumnOrdinalSet,
	onCond tree.TypedExpr,
	reqOrdering exec.OutputOrdering,
) (exec.Node, error) {
	inputNode := input.(*Node)
	columns := joinColumns(joinType, inputNode.Columns(), tableColumns(table, lookupCols))
	n := f.newNode("lookup-join", columns, reqOrdering, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructLookupJoin(
		joinType, inputNode.WrappedNode(), table, index,
		eqCols, eqColsAreKey, lookupCols, onCond, reqOrdering,
	))
}

func (f *Factory) ConstructInvertedJoin(
	joinType sqlbase.JoinType,
	invertedExpr tree.TypedExpr,
	input exec.Node,
	table cat.Table,
	index cat.Index,
	inputCol exec.NodeColumnOrdinal,
	lookupCols exec.TableColumnOrdinalSet,
	onCond tree.TypedExpr,
	reqOrdering exec.OutputOrdering,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructZigzagJoin(
	leftTable cat.Table,
	leftIndex cat.Index,
	rightTable cat.Table,
	rightIndex cat.Index,
	leftEqCols []exec.NodeColumnOrdinal,
	rightEqCols []exec.NodeColumnOrdinal,
	leftCols exec.NodeColumnOrdinalSet,
	rightCols exec.NodeColumnOrdinalSet,
	onCond tree.TypedExpr,
	fixedVals []exec.Node,
	reqOrdering exec.OutputOrdering,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructLimit(input exec.Node, limit, offset tree.TypedExpr) (exec.Node, error) {
	inputNode := input.(*Node)
	n := f.newNode("limit", inputNode.Columns(), nil /* reqOrdering */, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructLimit(inputNode.WrappedNode(), limit, offset))
}

func (f *Factory) ConstructMax1Row(input exec.Node, errorText string) (exec.Node, error) {
	inputNode := input.(*Node)
	n := f.newNode("max1row", inputNode.Columns(), nil /* reqOrdering */, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructMax1Row(input, errorText))
}

func (f *Factory) ConstructProjectSet(
	input exec.Node, exprs tree.TypedExprs, zipCols sqlbase.ResultColumns, numColsPerGen []int,
) (exec.Node, error) {
	inputNode := input.(*Node)
	cols := append(inputNode.Columns(), zipCols...)
	n := f.newNode("project-set", cols, nil /* reqOrdering */, inputNode)
	return n.setWrapped(f.wrappedFactory.ConstructProjectSet(
		inputNode.WrappedNode(), exprs, zipCols, numColsPerGen,
	))
}

func (f *Factory) ConstructWindow(n exec.Node, wi exec.WindowInfo) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) RenameColumns(input exec.Node, colNames []string) (exec.Node, error) {
	n := input.(*Node)
	if len(n.columns) != len(colNames) {
		return nil, errors.AssertionFailedf("column mismatch")
	}
	for i := range n.columns {
		n.columns[i].Name = colNames[i]
	}
	return n.setWrapped(f.wrappedFactory.RenameColumns(n.wrappedNode, colNames))
}

func (f *Factory) ConstructPlan(
	root exec.Node, subqueries []exec.Subquery, cascades []exec.Cascade, checks []exec.Node,
) (exec.Plan, error) {
	p := &Plan{
		Root:       root.(*Node),
		Subqueries: subqueries,
		Cascades:   cascades,
		Checks:     make([]*Node, len(checks)),
	}
	for i := range checks {
		p.Checks[i] = checks[i].(*Node)
	}

	wrappedSubqueries := append([]exec.Subquery(nil), subqueries...)
	for i := range wrappedSubqueries {
		wrappedSubqueries[i].Root = wrappedSubqueries[i].Root.(*Node).WrappedNode()
	}
	wrappedChecks := make([]exec.Node, len(checks))
	for i := range wrappedChecks {
		wrappedChecks[i] = checks[i].(*Node).WrappedNode()
	}
	var err error
	p.WrappedPlan, err = f.wrappedFactory.ConstructPlan(
		p.Root.WrappedNode(), wrappedSubqueries, cascades, wrappedChecks,
	)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (f *Factory) ConstructExplainOpt(plan string, envOpts exec.ExplainEnvData) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructExplain(
	options *tree.ExplainOptions, stmtType tree.StatementType, plan exec.Plan,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructShowTrace(typ tree.ShowTraceType, compact bool) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructInsert(
	input exec.Node,
	table cat.Table,
	insertCols exec.TableColumnOrdinalSet,
	returnCols exec.TableColumnOrdinalSet,
	checks exec.CheckOrdinalSet,
	allowAutoCommit bool,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructInsertFastPath(
	rows [][]tree.TypedExpr,
	table cat.Table,
	insertCols exec.TableColumnOrdinalSet,
	returnCols exec.TableColumnOrdinalSet,
	checkCols exec.CheckOrdinalSet,
	fkChecks []exec.InsertFastPathFKCheck,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructUpdate(
	input exec.Node,
	table cat.Table,
	fetchCols exec.TableColumnOrdinalSet,
	updateCols exec.TableColumnOrdinalSet,
	returnCols exec.TableColumnOrdinalSet,
	checks exec.CheckOrdinalSet,
	passthrough sqlbase.ResultColumns,
	allowAutoCommit bool,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructUpsert(
	input exec.Node,
	table cat.Table,
	canaryCol exec.NodeColumnOrdinal,
	insertCols exec.TableColumnOrdinalSet,
	fetchCols exec.TableColumnOrdinalSet,
	updateCols exec.TableColumnOrdinalSet,
	returnCols exec.TableColumnOrdinalSet,
	checks exec.CheckOrdinalSet,
	allowAutoCommit bool,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructDelete(
	input exec.Node,
	table cat.Table,
	fetchCols exec.TableColumnOrdinalSet,
	returnCols exec.TableColumnOrdinalSet,
	allowAutoCommit bool,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructDeleteRange(
	table cat.Table,
	needed exec.TableColumnOrdinalSet,
	indexConstraint *constraint.Constraint,
	interleavedTables []cat.Table,
	maxReturnedKeys int,
	allowAutoCommit bool,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructCreateTable(
	input exec.Node, schema cat.Schema, ct *tree.CreateTable,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructSequenceSelect(seq cat.Sequence) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructSaveTable(
	input exec.Node, table *cat.DataSourceName, colNames []string,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructErrorIfRows(
	input exec.Node, mkErr func(tree.Datums) error,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructOpaque(metadata opt.OpaqueMetadata) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructAlterTableSplit(
	index cat.Index, input exec.Node, expiration tree.TypedExpr,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructAlterTableUnsplit(index cat.Index, input exec.Node) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructAlterTableUnsplitAll(index cat.Index) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructAlterTableRelocate(
	index cat.Index, input exec.Node, relocateLease bool,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructBuffer(value exec.Node, label string) (exec.BufferNode, error) {
	return struct{ exec.BufferNode }{}, nil
}

func (f *Factory) ConstructScanBuffer(ref exec.BufferNode, label string) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructRecursiveCTE(
	initial exec.Node, fn exec.RecursiveCTEIterationFn, label string,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructControlJobs(
	command tree.JobCommand, input exec.Node,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructCancelQueries(input exec.Node, ifExists bool) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructCancelSessions(input exec.Node, ifExists bool) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructCreateView(
	schema cat.Schema,
	viewName string,
	ifNotExists bool,
	replace bool,
	temporary bool,
	viewQuery string,
	columns sqlbase.ResultColumns,
	deps opt.ViewDeps,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructExport(
	input exec.Node, fileName tree.TypedExpr, fileFormat string, options []exec.KVOption,
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) ConstructExplainPlan(
	options *tree.ExplainOptions, buildFn func(ef exec.ExplainFactory) (exec.Plan, error),
) (exec.Node, error) {
	panic("unimplemented")
}

func (f *Factory) NewExplainFactory(options *tree.ExplainOptions) exec.ExplainFactory { return nil }

func tableColumns(table cat.Table, ordinals exec.TableColumnOrdinalSet) sqlbase.ResultColumns {
	cols := make(sqlbase.ResultColumns, 0, ordinals.Len())
	for i, ok := ordinals.Next(0); ok; i, ok = ordinals.Next(i + 1) {
		col := table.Column(i)
		cols = append(cols, sqlbase.ResultColumn{
			Name: string(col.ColName()),
			Typ:  col.DatumType(),
		})
	}
	return cols
}

func joinColumns(
	joinType sqlbase.JoinType, left, right sqlbase.ResultColumns,
) sqlbase.ResultColumns {
	if joinType == sqlbase.LeftSemiJoin || joinType == sqlbase.LeftAntiJoin {
		return left
	}
	res := make(sqlbase.ResultColumns, 0, len(left)+len(right))
	res = append(res, left...)
	res = append(res, right...)
	return res
}
