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
- Node - machine in the cluster
- Client / Client-side - the SQL client
- Gateway node / Gateway-side - the cluster node to which the client SQL query is delivered first
- Leader node / Leader-side - the cluster node which resolves a KV operation and
                              has local access to the respective KV data

Most of the following text reads from the entry-side perspective, where the query parsing and planning currently runs.

# Motivation

The desired improvements are listed below.

1. Remote-side filtering

  When querying for a set of rows that match a filtering expression, we
  currently query all the keys in certain ranges and process the filters after
  receiving the data on the ingress node over the network. Instead, we want the
  filtering expression to be processed by the leader or remote node, saving on
  network traffic and related processing.

  The remote-side filtering does not need to support full SQL expressions - it
  can support a subset that includes common expressions (e.g. everything that
  can be translated into expressions operating on strings) with the requesting
  node applying a "final" filter.

2. Remote-side updates and deletes

  For statements like `UPDATE .. WHERE` and `DELETE .. WHERE` we currently
  perform a query, receive results at the ingress over the network, and then
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

!!! Needs revising !!!

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

## Logical model and logical plans


This is a description of a logical framework for describing logical plans.  It
is a conceptual model and *not* a description of what actually happens in our
system.

The logical plan is made up **aggregators**. Each aggregator consumes an **input
stream** of rows (or more streams for joins, but let's leave that aside for now)
and produces an **output stream** of rows. Each row is a tuple of column values;
both the input and the output streams have a set schema.  The schema is a set of
columns and types, with each row having a datum for each column. Again, we
emphasize that the streams are a logical concept and might not map to a single
data stream in the actual computation.

We introduce the concept of **grouping** to characterize a specific aspect of the
computation that happens inside an aggregator. The groups are defined based on a
**group key**, which is a subset of columns in the input stream. The computation
that happens for each group is independent of the data in the other groups and
the aggregator emits the results for each group. The ordering between group
results in the output stream is not fixed - some aggregators may guarantee a
certain ordering, others may not.

More precisely, we can define the computation in an aggregator using a function
`agg` that takes a sequence of input rows that are in a single group (same group
key) and produces a set of output rows. The output of an aggregator is
the concatenation of the outputs of `agg` on all the groups, in some order.

The grouping characteristic will be useful when we later decide how to
distribute the computation that is represented by an aggregator: since results
for each group are independent, different groups can be processed on different
nodes. The more groups we have, the better. At one end of the spectrum there are
single-group aggregators (group key is the empty set of columns, meaning
everything is in the same group) which cannot be distributed. At the other end
there are no-grouping aggregators which can be parallelized arbitrarily. Note
that no-grouping aggregators are different than aggregators where the group key
is the full set of columns - the latter still requires rows that are equal to be
processed on a single node (this would be useful for an aggregator implementing
`UNIQUE` for example). An aggregator with no grouping is a special but important
case in which we are not aggregating multiple pieces of data, but we may be
filtering, transforming, or reordering individual pieces of data.

Aggregators can make use of SQL expressions, evaluating them with various inputs
as part of their work. In particular, all aggregators can optionally use an
**output filter** expression - a boolean function that is used to discard
elements that would have otherwise been part of the output stream.

(Note: the alternative of restricting use of SQL expressions to only certain
aggregators was considered; that approach makes it much harder to support outer
joins, where the `ON` expression evaluation must be part of the internal join
logic and not just a filter on the output.)

A special type of aggregator is the **program** aggregator which is a
"programmable" aggregator which processes the input stream sequentially (one
element at a time), potentially emitting output elements. This is an aggregator
with no grouping (group key is the full set of columns); the processing of each
data element is independent. A program can be used, for example, to generate new
values from arbitrary expressions (like the `a+b` in `SELECT a+b FROM ..`).

Special **table reader** aggregators with no inputs are used as data sources; a
table reader can be configured to output only certain columns, as needed.
A special **final** aggregator with no outputs is used for the results of the
query/statement.

Some aggregators (final, limit) have an **ordering requirement** on the input
stream (a list of columns with corresponding ascending/descending requirements).
Some aggregators (like table readers) can guarantee a certain ordering on their output
stream, called an **ordering guarantee** (same as the `orderingInfo` in the
current code). All aggregators have an associated **ordering
characterization** function that maps an ordering guarantee on the input stream
into an ordering guarantee for the output stream - meaning that if the input is
ordered according to the input guarantee, the output guarantee will hold.

The ordering guarantee of the table readers along with the characterization
functions can be used to propagate ordering information across the logical plan.
When there is a mismatch (an aggregator has an ordering requirement that is not
matched by a guarantee), we insert a **sorting aggregator** - this is a
non-grouping aggregator with output schema identical to the input schema that
reorders the elements in the input stream providing a certain output order
guarantee regardless of the input ordering. We can perform optimizations wrt
sorting at the logical plan level - we could potentially put the sorting
aggregator earlier in the pipeline, or split it into multiple nodes (one of
which performs preliminary sorting in an earlier stage).

To introduce the main types of aggregators we use of a simple query.

### Example 1

```sql
TABLE Orders (OId INT PRIMARY KEY, CId INT, Value DECIMAL, Date DATE)

SELECT CID, SUM(VALUE) FROM Orders
  WHERE DATE > 2015
  GROUP BY CID
  ORDER BY 1 - SUM(Value)
```

This is a potential description of the aggregators and streams:

```
TABLE-READER src
  Table: Orders
  Table schema: Oid:INT, Cid:INT, Value:DECIMAL, Date:DATE
  Output filter: (Date > 2015)
  Output schema: Cid:INT, Value:DECIMAL
  Ordering guarantee: Oid

AGGREGATOR summer
  Input schema: Cid:INT, Value:DECIMAL
  Output schema: Cid:INT, ValueSum:DECIMAL
  Group Key: Cid
  Ordering characterization: if input ordered by Cid, output ordered by Cid

PROGRAM sortval
  Input schema: Cid:INT, ValueSum:DECIMAL
  Output schema: SortVal:DECIMAL, Cid:INT, ValueSum:DECIMAL
  Ordering characterization: if input ordered by [Cid,]ValueSum[,Cid], output ordered by [Cid,]-ValueSum[,Cid]
  SQL Expressions: E(x:INT) INT = (1 - x)
  Code {
    EMIT E(ValueSum), CId, ValueSum
  }

AGGREGATOR final:
  Input schema: SortVal:DECIMAL, Cid:INT, ValueSum:DECIMAL
  Input ordering requirement: SortVal
  Group Key: none

Composition: src -> summer -> sortval -> final
```

Note that the logical description does not include sorting aggregators. This
preliminary plan will lead to a full logical plan when we propagate ordering
information. We will have to insert a sorting aggregator before `final`:
```
src -> summer -> sortval -> sort(OrderSum) -> final
```
Each arrow is a logical stream. This is the complete logical plan.

In this example we only had one option for the sorting aggregator. Let's look at
another example.


### Example 2

```sql
TABLE People (Age INT, NetWorth DECIMAL, ...)

SELECT Age, Sum(NetWorth) FROM v GROUP BY AGE ORDER BY AGE
```

Preliminary logical plan description:
```
TABLE-READER src
  Table: People
  Table schema: Age:INT, NetWorth:DECIMAL
  Output schema: Age:INT, NetWorth:DECIMAL
  Ordering guarantee: XXX  // will consider different cases later

AGGREGATOR summer
  Input schema: Age:INT, NetWorth:DECIMAL
  Output schema: Age:INT, NetWorthSum:DECIMAL
  Group Key: Age
  Ordering characterization: if input ordered by Age, output ordered by Age

AGGREGATOR final:
  Input schema: Age:INT, NetWorthSum:DECIMAL
  Input ordering requirement: Age
  Group Key: none

Composition: src -> summer -> final
```

The `summer` aggregator can perform aggregation in two ways - if the input is
not ordered by Age it will use an unordered map with one entry per `Age` and the
results will be output in arbitrary order; if the input is ordered by `Age` it can
aggregate on one age at a time and it will emit the results in age order.

Let's take two cases:

1. src is ordered by `Age` (we use a covering index on `Age`)

   In this case, when we propagate the ordering
   information we will notice that `summer` preserves ordering by age and we
   won't need to add sorting aggregators.

2. src is not ordered by anything

   In this case, summer will not have any output ordering guarantees and we will
   need to add a sorting aggregator before `final`:
   ```
   src -> summer -> sort(Age) -> final
   ```
   We could also use the fact that `summer` would preserve the order by `Age`
   and put the sorting aggregator before `summer`:
   ```
   src -> sort(Age) -> summer -> final
   ```   
   We would choose between these two logical plans.

There is also the possibility that `summer` uses an ordered map, in which case
it will always output the results in age order; that would mean we are always in
case 1 above, regardless of the ordering of `src`.


### Example 3

```sql
TABLE v (Name STRING, Age INT, Account INT)

SELECT COUNT(DISTINCT(account)) FROM v
  WHERE age > 10 and age < 30
  GROUP BY age HAVING MIN(Name) > 'k'
```

```
TABLE-READER src
  Table: v
  Table schema: Name:STRING, Age:INT, Account:INT
  Filter: (Age > 10 AND Age < 30)
  Output schema: Name:STRING, Age:INT, Account:INT
  Ordering guarantee: Name

AGGREGATOR countdistinctmin
  Input schema: Name:String, Age:INT, Account:INT
  Group Key: Age
  Group results: distinct count as AcctCount:INT
                 MIN(Name) as MinName:STRING
  Output filter: (MinName > 'k')
  Output schema: AcctCount:INT
  Ordering characterization: if input ordered by Age, output ordered by Age

AGGREGATOR final:
  Input schema: AcctCount:INT
  Input ordering requirement: none
  Group Key: none

Composition: src -> countdistinctmin -> final
```

## From logical to physical

To distribute the computation that was described in terms of aggregators and
logical streams, we use the following facts:

 - for any aggregator, groups can be partitioned into subsets and processed in
   parallel, as long as all processing for a group happens on a single node.

 - the ordering characterization of an aggregator applies to *any* input stream
   with a certain ordering; it is useful even when we have multiple parallel
   instances of computation for that logical node: if the physical input streams
   in all the parallel instances are ordered according to the logical input
   stream guarantee (in the logical plan), the physical output streams in all
   instances will have the output guarantee of the logical output stream. If at
   some later stage these streams are merged into a single stream (merge-sorted,
   i.e. with the ordering properly maintained), that physical stream will have
   the correct ordering - that of the corresponding logical stream.

 - aggregators with empty group keys (`limit`, `final`) must have their final
   processing on a single node (they can however have preliminary distributed
   stages).

So each logical aggregator can correspond to multiple distributed instances, and
each logical stream can correspond to multiple physical streams **with the same
ordering guarantees**.

We can distribute using a few simple rules:

 - table readers have multiple instances, split according to the ranges; each
   instance is processed by the raft leader of the relevant ranges and is the
   start of a physical stream.

 - streams continue in parallel through programs. When an aggregator is reached,
   the streams can be redistributed to an arbitrary number of instances using
   hashing on the group key. Aggregators with empty group keys will have a
   single physical instance, and the input streams are merged according to the
   desired ordering. As mentioned above, each physical stream will be already
   ordered (because they all correspond to an ordered logical stream).

 - sorting aggregators apply to each physical stream corresponding to the
   logical stream it is sorting. A sort aggregator by itself will *not* result
   in coalescing results into a single node. This is implicit from the fact that
   (like programs) it requires no grouping.

It is important to note that correctly distributing the work along range
boundaries is not necessary for correctness - if a range gets split or moved
while we are planning the query, it will not cause incorrect results. Some key
reads might be slower because they actually happen remotely, but as long as
*most of the time, most of the keys* are read locally this should not be a
problem.

Assume that we run the Example 1 query on a **Gateway** node and the table has
data that on two nodes **A** and **B** (i.e. these two nodes are masters for all
the relevant range). The logical plan is:

```
TABLE-READER src
  Table: Orders
  Table schema: Oid:INT, Cid:INT, Value:DECIMAL, Date:DATE
  Output filter: (Date > 2015)
  Output schema: Cid:INT, Value:DECIMAL
  Ordering guarantee: Oid

AGGREGATOR summer
  Input schema: Cid:INT, Value:DECIMAL
  Output schema: Cid:INT, ValueSum:DECIMAL
  Group Key: Cid
  Ordering characterization: if input ordered by Cid, output ordered by Cid

PROGRAM sortval
  Input schema: Cid:INT, ValueSum:DECIMAL
  Output schema: SortVal:DECIMAL, Cid:INT, ValueSum:DECIMAL
  Ordering characterization: if input ordered by [Cid,]ValueSum[,Cid], output ordered by [Cid,]-ValueSum[,Cid]
  SQL Expressions: E(x:INT) INT = (1 - x)
  Code {
    EMIT E(ValueSum), CId, ValueSum
  }
```

![Logical plan](distributed_sql_logical_plan.png?raw=true "Logical Plan")

This logical plan above could be instantiated as the following physical plan:

![Physical plan](distributed_sql_physical_plan.png?raw=true "Physical Plan")

Each box in the physical plan is a *processor*:
 - `src` is a table reader and performs KV Get operations and forms rows; it is
   programmed to read the spans that belong to the respective node. It evaluates
   the `Date > 2015` filter before outputting data elements.
 - `summer-stage1` is the first stage of the `summer` aggregator; its purpose is
   to do the aggregation it can do locally and distribute the partial results to
   the `summer-stage2` processes, such that all values for a certain group key
   (`CId`) reach the same process (by hashing `CId` to one of two "buckets").
 - `summer-stage2` performs the actual sum and outputs the index (`CId`) and
   corresponding sum.
 - `sortval` calculates and emits the additional `SortVal` value, along with the
   `CId` and `ValueSum`
 - `sort` sorts the stream according to `SortVal`
 - `final` merges the two input streams of data to produce the final sorted
   result.

Note that the second stage of the `summer` aggregator doesn't need to run on the
same nodes; for example, an alternate physical plan could use a single stage 2
processor:

![Alternate physical plan](distributed_sql_physical_plan_2.png?raw=true "Alternate physical Plan")

The processors always form a directed acyclic graph.

### Processors

Processors are generally made up of three components:

![Processor](distributed_sql_processor.png?raw=true "Processor")

1. The *input synchronizer* merges the input streams into a single stream of
   data. Types:
   * single-input (pass-through)
   * unsynchronized: passes data elements from all input streams, arbitrarily
     interleaved.
   * ordered: the input physical streams have an ordering guarantee (namely the
     guarantee of the correspondig locical stream); the synchronizer is careful
     to interleave the streams so that the merged stream has the same guarantee.

2. The *data processor* core implements the data transformation or aggregation
   logic (and in some cases performs KV operations).

3. The *output router* splits the data processor's output to multiple streams;
   types:
   * single-output (pass-through)
   * mirror: every data element is sent to all output streams
   * hashing: each data element goes to a single output stream, chosen according
     to a hash function applied on certain elements of the data tuples.
   * by range: TODO (for index-join)

### Inter-stream ordering

**This is a feature that relates to implementing certain optimizations, but does
not alter the structure of logical or physical plans.**

Consider this example:
```sql
TABLE t (k INT PRIMARY KEY, v INT)
SELECT k, v FROM t WHERE k + v > 10 ORDER BY k
```

This is a simple plan:

```
READER src
  Table: t
  Output filter: (k + v > 10)
  Output schema: k:INT, v:INT
  Ordering guarantee: k

AGGREGATOR final:
  Input schema: k:INT, v:INT
  Input ordering requirement: k
  Group Key: none

Composition: src -> final
```

Now let's say that the table spans two ranges on two different nodes - one range
for keys `k <= 10` and one range for keys `k > 10`. In the physical plan we
would have two streams starting from two readers; the streams get merged into a
single stream before `final`. But in this case, we know that *all* elements in
one stream are ordered before *all* elements in the other stream - we say that
we have an **inter-stream ordering**. We can be more efficient when merging
(before `final`): we simply read all elements from the first stream and then all
elements from the second stream. Moreover, we would also know that the reader
and other processors for the second stream don't need to be scheduled until the
first stream is consumed, which is useful information for scheduling the query.
In particular, this is important when we have a query with `ORDER BY` and
`LIMIT`: the limit would be represented by an aggregator with a single group,
with physical streams merging at that point; knowledge of the inter-stream
ordering would allow us to potentially satisfy the limit by only reading from
one range.

We add the concept of inter-physical-stream ordering to the logical plan - it is
a property of a logical stream (even though it refers to multiple physical
streams that could be associated with that logical stream). We annotate all
aggregators with an **inter-stream ordering characterization function** (similar
to the ordering characterization described above, which can be thought of as
"intra-stream" ordering). The inter-stream ordering function maps an input
ordering to an output ordering, with the meaning that if the physical streams
that are inputs to distributed instances of that aggregator have the
inter-stream ordering described by input, then the output streams have the given
output ordering.

Like the intra-stream ordering information, we can propagate the inter-stream
ordering information starting from the table readers onward. The streams coming
out of a table reader have an inter-stream order if the spans each reader "works
on" have a total order (this is always the case if each table reader is
associated to a separate range).

The information can be used to apply the optimization above - if a logical
stream has an appropriate associated inter-stream ordering, the merging of the
physical streams can happen by reading the streams sequentially. The same
information can be used for scheduling optimizations (such as scheduling table
readers that eventually feed into a limit sequentially instead of
concurrently).

TODO some examples, especially one where the intra-stream and inter-stream
orderings are different and yet eventually useful:
`(SELECT k, v FROM k ORDER BY v LIMIT 100) ORDER by k`.

# Implementation strategy

Three streams:
 - implementing the physical building blocks (processors, synchronizers,
   routers)
 - physical planning and distribution logic
 - scheduling and resource management within a single node

# KV Layer requirements

Distributed txn coord (read and writes)

Figuring out how ranges are distributed

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
