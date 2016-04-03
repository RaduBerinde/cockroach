- Feature Name: Distributing SQL queries
- Status: draft
- Start Date: 2015/02/12
- Authors: andreimatei, knz, RaduBerinde
- RFC PR: (PR # after acceptance of initial draft)
- Cockroach Issue: (one or more # from the issue tracker)

# Table of Contents

  * [Table of Contents](#table-of-contents)
  * [Summary](#summary)
    * [Vocabulary](#vocabulary)
  * [Motivation](#motivation)
  * [Detailed design](#detailed-design)
    * [Overview](#overview)
    * [Logical plan](#logical-plan)
    * [Physical plan](#physical-plan)
  * [KV Layer requirements](#kv-layer-requirements)
  * [Implementation strategy](#implementation-strategy)
  * [Alternatives](#alternatives)
    * [More logic in the KV layer](#more-logic-in-the-kv-layer)
      * [Complexity](#complexity)
      * [Applicability](#applicability)
    * [SQL2SQL: Distributed SQL layer](#sql2sql-distributed-sql-layer)
      * [Sample high-level query flows](#sample-high-level-query-flows)
      * [Complexity](#complexity-1)
      * [Applicability](#applicability-1)
    * [Spark: Compiling SQL into a data-parallel language running on top of a distributed-execution runtime](#spark-compiling-sql-into-a-data-parallel-language-running-on-top-of-a-distributed-execution-runtime)
      * [Sample program](#sample-program)
      * [Complexity](#complexity-2)
  * [Unresolved questions](#unresolved-questions)

# Summary

In this RFC we propose a general approach for distributing SQL processing and
moving computation closer to the data. The goal is to trigger an initial
discussion and not a complete detailed design.

## Vocabulary

- KV - the KV system in cockroach, defined by its key-value, range and batch API
- k/v - a key-value pair, usually used to refer to an entry in KV
- Client / Client-side - the SQL client
- SQL gateway / Gateway-side - the cluster node to which the client SQL query is delivered first
- Leader node / Leader-side - the cluster node which resolves a KV operation
- Remote node / Remote-side - the cluster node(s) where the data actually lies

Most of the following text reads from the entry-side perspective, where the query parsing and planning currently runs.

# Motivation

The desired improvements are listed below.

1. Remote-side filtering

  When querying for a set of rows that match a filtering expression, we
  currently query all the keys in certain ranges and process the filters after
  receiving the data on the gateway node over the network. Instead, we want the
  filtering expression to be processed by the leader or remote node, saving on
  network traffic and related processing.

  The remote-side filtering does not need to support full SQL expressions - it
  can support a subset that includes common expressions (e.g. everything that
  can be translated into expressions operating on strings) with the requesting
  node applying a "final" filter.

2. Remote-side updates and deletes

  For statements like `UPDATE .. WHERE` and `DELETE .. WHERE` we currently
  perform a query, receive results at the gateway over the network, and then
  perform the update or deletion there.  This involves too many round-trips;
  instead, we want the query and updates to happen on the node which has access
  to the data.

  Again, this does not need to cover all possible SQL expressions (we can keep
  a working "slow" path for some cases). However, to cover the most important
  queries we still need more than simple filtering expressions (`UPDATE`
  commonly uses expressions and functions for calculating new values).

3. Distributed SQL operations

  Currently SQL operations are processed by the entry node and thus their
performance does not scale with the size of the cluster. We want to be able to
distribute the processing on multiple nodes (parallelization for performance).

  1. Distributed joins

    In joins, we produce results by matching the values of certain columns
    among multiple tables. One strategy for distributing this computation is
    based on hashing: `K` nodes are chosen and each of the nodes with fast
    access to the table data sends the results to the `K` nodes according to a
    hash function computed on the join columns (guaranteeing that all results
    with the same values on these columns go to the same node). Hash-joins are
    employed e.g. by F1.


    Distributed joins and remote-side filtering can be needed together: 
    ```sql
    -- find all orders placed around the customer's birthday. Notice the
    -- filtering needs to happen on the results. I've complicated the filtering
    -- condition because a simple equality check could have been made part of
    -- the join.
    SELECT * FROM Customers c INNER JOIN Orders o ON c.ID = i.CustomerID
      WHERE DayOfYear(c.birthday) - DayOfYear(o.date) < 7
    ```
    
  2. Distributed aggregation 

    When using `GROUP BY` we aggregate results according to a set of columns or
    expressions and compute a function on each group of results. A strategy
    similar to hash-joins can be employed to distribute the aggregation.

  3. Distributed sorting

    When ordering results, we want to be able to distribute the sorting effort.
    Nodes would sort their own data sets and one ore more nodes would merge the
    results.

# Detailed design

## Overview

The proposed approach is inspired by [Sawzall][1] - a project by Rob Pike et al.
that proposes a "shell" (high-level language interpreter) to ease the
exploitation of MapReduce. Its main innovation is a concise syntax to define
“local” processes that take a piece of local data and emit zero or more results
(these get translated to Map logic); then another syntax which takes results
from the local transformations and aggregates them in different ways (this gets
translated to Reduce logic). In a nutshell: Sawzall = MapReduce + high-level
syntax + new terminology ("local filtering" instead of "map", "aggregation"
instead of "reduce").

We propose something similar:

1. a simple language that operates on one small range of k/v at a time -
   corresponding to a SQL row or maybe a SQL parent row + nested rows. The
   language has no loops, no stack, no local variables. The processing it can do
   on a row is pretty limited - mostly filtering and extracting columns.

2. a routing of these results to multiple aggregator processes. 

The aggregators are distributed across the cluster and they implement all the
possible SQL aggregation functions (counting, grouping, statistics, no
aggregation, etc). 

Besides accumulating or aggregating data, the aggregators can feed their results
to another node or set of nodes, possibly as input for other programs. Finally,
aggregators with special functionality for batching up results and performing KV
commands are used to make updates to the database.

The key idea that makes this approach tractable for us is that we only have a
small number of map/reduce scenarios possible in SQL (those dictated by
relational algebra). So we can have them in a predetermined library and only
need to transform SQL queries to a very high-level schedule of map and reduce
operations from our library. With care, the language that we need to introduce
stays simple by being restricted to operate on what constitutes a single SQL
record (row or index entry).

[1]: http://research.google.com/archive/sawzall.html

## Logical plan

To introduce the main concepts we will make use of a sample query:

```sql
TABLE Orders (
   OId INT PRIMARY KEY,
   CId INT,
   Value DECIMAL,
   Date DATE
)

SELECT CID, SUM(VALUE) FROM Orders
  WHERE DATE > 2015
  GROUP BY CID
  ORDER BY 1 - SUM(Value)
```

Here is a representation of the aggregators and programs that together can
execute this query. Note that this is not a language we are introducing, it is
for conceptualizing.

```
AGGREGATOR summer[Cid:INT] SUM OF (Value:DECIMAL)

AGGREGATOR ORDERED sorter[OrderVal:DECIMAL] COLLECTION OF (CId:INT, Value:DECIMAL)

PROGRAM1(OId:INT, CId:INT, Value:DECIMAL, Date:DATE) {
  if Date > 2015 {
    EMIT INTO summer[CId] Value
  }
}

PROGRAM2(CId:INT, ValueSum:DECIMAL) {
  EMIT INTO sorter[1 - ValueSum] CId, ValueSum
}
```

Conceptually, an aggregator is a map indexed by a tuple; for each tuple the
aggregator receives a collection of tuples and t can optionally perform an
operation on this collection. Aggregators can be unordered or ordered - the
latter output the tuples in the order of the index tuple.  The `summer`
aggregators is indexed by a single element (`Cid`); it collects `Value` elements
and for each index these elements are reduced by a sum operation. The `sorter`
aggregator is a "no-op" aggregator that simply returns the collection of values
for each value of the `OrderVal`. Because the results of aggregators are always
ordered by index, this implicitly results in sorting the input.

These programs and aggregators come together as described by a *logical plan*.
The logical plan does not prescribe how the computation is distributed; it is
roughly analogous with the plans we have in our current, non-distributed SQL
implementation.

![Logical plan](distributed_sql_logical_plan.png?raw=true "Logical Plan")

`TableReader` is a built-in facility that reads KV entries and outputs row
tuples (very close to what `scanNode` does today).

## Physical plan

The logical plan is used to instantiate the *physical plan*. At this planning
step, we take into account where the leader of each range is located; we divide
the `TableScanner` work among the nodes that have data for that table.

It is important to note that correctly distributing the work is not necessary
for correctness - if a range gets split or moved while we are planning the
query, it will not cause incorrect results. Some key reads might be slower
because they actually happen remotely, but as long as *most of the time, most of
the keys* are read locally this should not be a problem.

Assume that we run the query above on a **Gateway** node and the table has data
that on two nodes **A** and **B** (i.e. these two nodes are leaders for all the
relevant range). The logical plan above could be instantiated as the following
physical plan:

![Physical plan](distributed_sql_physical_plan.png?raw=true "Physical Plan")

Each box is a *processor*:
 - `TableReader` performs KV Get operations and forms rows; it is programmed to
   read the spans that belong to the respective node.
 - Each instance of `PROGRAM1` evaluates the `Date > 2015` expression and
   conditionally emits a value.
 - `summer-stage1` is the first stage of the `summer` aggregator; its purpose is
   to do the aggregation it can do locally and distribute the partial results to
   the `summer-stage2` processes, such that all values for a certain index
   (`CId`) reach the same process (by hashing `CId` to one of two "buckets").
 - `summer-stage2` performs the actual sum and outputs the index (`CId`) and
   corresponding sum.
 - `PROGRAM2` emits each `<CId, ValueSum>` pair into the index with the result
   of the `1 - ValueSum` expression.
 - `sorter-stage1` sorts the input locally and emits it in sorted order.
 - `sorter-stage2` merges the two input streams of data to produce the final
   sorted result.

Note that the second stage of the summer aggregator doesn't need to run on the
same nodes; for example, an alternate physical plan could use a single stage 2
processor for `summer`:

![Alternate physical plan](distributed_sql_physical_plan_2.png?raw=true "Alternate physical Plan")

Note that the processors always form a directed acyclic graph.

## Processors

Processors are made up of three components:

![Processor](distributed_sql_processor.png?raw=true "Processor")

1. The *input synchronizer* merges the input streams into a single stream of
   data. Types:
   * single-input (pass-through)
   * unsynchronized: passes data elements from all input streams, arbitrarily
     interleaved.
   * ordered: input streams are sorted according to some part of the data; the
     synchronizer is careful to interleave the streams so that the output is
     sorted.

2. The *data processor* core implements the data transformation or aggregation
   logic.

3. The *output router* splits the data processor's output to multiple streams;
   types:
   * single-output (pass-through)
   * mirror: every data element is sent to all output streams
   * hashing: each data element goes to a single output stream, chosen according
     to a hash function applied on certain elements of the data tuples.

# Implementation strategy

# KV Layer requirements

distributed txn coord (read and writes)
figuring out how ranges are distributed

# Alternatives

We outline a set of alternative approaches that were considered.

## More logic in the KV layer

In this approach we would build more intelligence in the KV layer. It would
understand rows, and it would be able to process expressions (either SQL
expressions, or some kind of simplified language, e.g. string based).

### Complexity

Most of the complexity of this approach is around building APIs and support for
expressions. For full SQL expressions, we would need a KV-level language that
is able to read and modify SQL values without being part of the SQL layer. This
would mean a compiler able to translate SQL expressions to programs in a
KV-level VM that perform the SQL-to-bytes and bytes-to-SQL translations
explicitly (i.e. translate/migrate our data encoding routines from Go to that
KV-level VM's instructions).

### Applicability

The applicability of this technique is limited: it would work well for
filtering and possibly for remote-side updates, but it is hard to imagine
building the logic necessary for distributed SQL operations (joins,
aggregation) into the KV layer.

It seems that if we want to meet all described goals, we need to make use of a
smarter approach. With this in mind, expending any effort toward this approach
seems wasteful at this point in time. We may want to implement some of these
ideas in the future if it helps make things more efficient, but for now we
should focus on initial steps towards a more encompassing solution.


## SQL2SQL: Distributed SQL layer

In this approach we would build a distributed SQL layer, where the SQL layer of
a node can make requests to the SQL layer of any other node. The SQL layer
would "peek" into the range information in the KV layer to decide how to split
the workload so that data is processed by the respective raft range leaders.
Achieving a correct distribution to range leaders would not be necessary for
correctness; thus we wouldn't need to build extra coordination with the KV
layer to synchronize with range splits/merges or leadership changes during an
SQL operation.


### Sample high-level query flows

Sample flow of a “simple” query (select or update with filter):

| **Node A**                                 |  **Node B**  | **Node C** | **Node D** |
|--------------------------------------------|--------------|------------|------------|
| Receives statement                         |              |            |            |
| Finds that the table data spans three ranges on **B**, **C**, **D** |  |            |
| Sends scan requests to **B**, **C**, **D** |              |            |            |
|     | Starts scan (w/filtering, updates) | Starts scan (w/filtering, updates) | Starts scan (w/filtering, updates) |
|     | Sends results back to **A** | Sends results back to **A** | Sends results back to **A** |
| Aggregates and returns results.            |              |            |            |

Sample flow for a hash-join:

| **Node A**                                 |  **Node B**  | **Node C** | **Node D** |
|--------------------------------------------|--------------|------------|------------|
| Receives statement                         |              |            |            |
| Finds that the table data spans three ranges on **B**, **C**, **D** |  |            |
| Sets up 3 join buckets on B, C, D          |              |            |            |
|     | Expects join data for bucket 0 | Expects join data for bucket 1 | Expects join data for bucket 2 |
| Sends scan requests to **B**, **C**, **D** |              |            |            |
|     | Starts scan (w/ filtering). Results are sent to the three buckets in batches | Starts scan (w/ filtering) Results are sent to the three buckets in batches | Starts scan (w/ filtering). Results are sent to the three buckets in batches |
|     | Tells **A** scan is finished | Tells **A** scan is finished | Tells **A** scan is finished |
| Sends finalize requests to the buckets     |              |            |            |
|     | Sends bucket data to **A**  | Sends bucket data to **A** | Sends bucket data to **A**  |
| Returns results                            |              |            |            |

### Complexity

We would need to build new infrastructure and APIs for SQL-to-SQL. The APIs
would need to support SQL expressions, either as SQL strings (which requires
each node to re-parse expressions) or a more efficient serialization of ASTs.

The APIs also need to include information about what key ranges the request
should be restricted to (so that a node processes the keys that it is leader
for - or at least was, at the time when we started the operation). Since tables
can span many raft ranges, this information can include a large number of
disjoint key ranges.

The design should not be rigid on the assumption that for any key there is a
single node with "fast" access to that key. In the future we may implement
consensus algorithms like EPaxos which allow operations to happen directly on
the replicas, giving us multiple choices for how to distribute an operation.

Finally, the APIs must be designed to allow overlap between processing, network
transfer, and storage operations - it should be possible to stream results
before all of them are available (F1 goes as far as streaming results
out-of-order as they become available from storage).

### Applicability

This general approach can be used for distributed SQL operations as well as
remote-side filtering and updates. The main drawback of this approach is that it
is very general and not prescriptive on how to build reusable pieces of
functionality. It is not clear how we could break apart the work in modular
piece, and it has the potential of evolving into a monster of unmanageable
complexity.


## Spark: Compiling SQL into a data-parallel language running on top of a distributed-execution runtime

The idea here is to introduce a new system - an execution environment for
distributed computation. The computations use a programming model like M/R, or
more pipeline stuff - Spark, or Google's [Dataflow][1] (parts of it are an
Apache project that can run on top of other execution environments - e.g.
Spark). 

In these models, you think about arrays of data, or maps on which you can
operate in parallel. The storage for these is distributed. And all you do is
operation on these arrays or maps - sort them, group them by key, transform
them, filter them. You can also operate on pairs of these datasets to do joins.

These models try to have *a)* smart compilers that do symbolic execution, e.g.
fuse as many operations together as possible - `map(f, map(g, dataset)) == map(f
● g, dataset)` and *b)* dynamic runtimes. The runtimes probably look at operations
after their input have been at least partially computed and decide which nodes
participate in this current operation based on who has the input and who needs
the output. And maybe some of this work has already been done for us in one of
these open source projects.

The idea would be to compile SQL into this sort of language, considering that we
start execution with one big sorted map as a dataset, and run it.If the
execution environment is good, it takes advantage of the data topology. This is
different from "distributed sql" because *a)* the execution environment is
dynamic, so you don't need to come up with an execution plan up front that says
what node is gonna issues what command to what other node and *b)* data can be
pushed from one node to another, not just pulled.

We can start small - no distributed runtime, just filtering for `SELECTS` and
filtering with side effects for `UPDATE, DELETE, INSERT FROM SELECT`. But we
build this outside of KV; we build it on top of KV (these programs call into KV,
as opposed to KV calling a filtering callback for every k/v or row).

[1]: https://cloud.google.com/dataflow/model/programming-model

### Sample program

Here's a quick sketch of a program that does remote-side filtering and deletion
for a table with an index.

Written in a language for (what I imagine to be) Spark-like parallel operations.
The code is pretty tailored to this particular table and this particular query
(which is a good thing). The idea of the exercise is to see if it'd be feasible
at all to generate such a thing, assuming we had a way to execute it.

The language has some data types, notably maps and tuples, besides the
distributed maps that the computation is based on. It interfaces with KV through
some `builtin::` functions.

It starts with a map corresponding to the KV map, and then munges and aggregates
the keys to form a map of "rows", and then generates the delete KV commands.

The structure of the computation would stay the same and the code would be a lot
shorter if it weren't tailored to this particular table, and instead it used
generic built-in functions to encode and decode primary key keys and index keys.

```sql
TABLE t (
  id1 int
  id2 string
  a string
  b int DEFAULT NULL

  PRIMARY KEY id1, id2
  INDEX foo (id1, b)
)

DELETE FROM t WHERE id1 >= 100 AND id2 < 200 AND len(id2) == 5 and b == 77
```

```go
func runQuery() {
  // raw => Map<string,string>. The key is a primary key string - table id, id1,
  // id2, col id. This map is also sorted.
  raw = builtin::readRange("t/primary_key/100", "t/primary_key/200")

  // m1 => Map<(int, string), (int, string)>. This map is also sorted because the
  // input is sorted and the function maintains sorting.
  m1 = Map(raw, transformPK).

  // Now build something resembling SQL rows. Since m1 is sorted, ReduceByKey is
  // a simple sequential scan of m1.
  // m2 => Map<(int, string), Map<colId, val>>. These are the rows.
  m2 = ReduceByKey(m1, buildColMap)  

  // afterFilter => Map<(int, string), Map<colId, val>>. Like m2, but only the rows that passed the filter
  afterFilter = Map(m2, filter)

  // now we batch up all delete commands, for the primary key (one KV command
  // per SQL column), and for the indexes (one KV command per SQL row)
  Map(afterFilter, deletePK)
  Map(afterFilter, deleteIndexFoo)

  // return the number of rows affected
  return len(afterFilter)
}

func transformPK(k, v) {
  #pragma maintainsSort  // important, keys remain sorted. So future
                         // reduce-by-key operations are efficient
  id1, id2, colId = breakPrimaryKey(k)
  return (key: {id1, id2}, value: (colId, v))
}

func breakPrimaryKey(k) {
  // remove table id and the col_id
  tableId, remaining = consumeInt(k)
  id1, remaining = consumeInt(remaining)
  id2, remaining = consumeInt(remaining)
  colId = consumeInt(remaining)
  returns (id1, id2, colId)
}

func BuildColMap(k, val) {
  colId, originalVal = val  // unpack
  a, remaining = consumeInt(originalVal)
  b, remaining = consumeString(remaining)
  // output produces a result. Can appear 0, 1 or more times.
  output (k, {'colId': colId, 'a': a, 'b': b})
}

func Filter(k, v) {
  // id1 >= 100 AND id2 < 200 AND len(id2) == 5 and b == 77
  id1, id2 = k
  if len(id2) == 5 && v.getWithDefault('b', NULL) == 77 {
    output (k, v)
  }
  // if filter doesn't pass, we don't output anything
}


func deletePK(k, v) {
  id1, id2 = k
  // delete KV row for column a
  builtIn::delete(makePK(id1, id2, 'a'))
  // delete KV row for column b, if it exists
  if v.hasKey('b') {
    builtIn::delete(makePK(id1, id2, 'b'))
  }
}

func deleteIndexFoo(k, v) {
  id1, id2 = k
  b = v.getWithDefault('b', NULL)
  
  builtIn::delete(makeIndex(id1, b))
}
```

### Complexity

This approach involves building the most machinery; it is probably overkill
unless we want to use that machinery in other ways than SQL.

# Unresolved questions
