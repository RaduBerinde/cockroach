# Brain dump

Thinking about logical plans.
In a good solution we should be able to recognize the following
concepts (possibly with a different terminology):

- kv scans:
  input: range selection(s)
  output: k/v pairs
  stateless

- kv updates:
  conditional or not
  input: k/v pairs, or condition,k,v
  output: the k's for which the value was modified

- row-wise pure functions ("programs")
  stateless
  consume one row, produce 0 or more rows

- "reducers" that produce 1 output row per group:
  e.g. count, min, max, sum
  constant, small state per group

- "reducers" that produce multiple output rows per group:
  e.g. limit, distinct, sort (within group)
  some have a predictably bounded amount of state (limit),
  for others it's more complicated (distinct, sort)
  
- we really want a cost function that estimates space and time requirements,
  could be for example (but not necessarily):

  - space: the worst case amount of state needed in total without
    parallelism for a given logical plan; and then possibly
    for each possible parallelisation opportunities, a function
    that expresses the space cost as a function of the parallelization parameter

  - time: the critical path through the query (minimum amount of
    operations on the longest path).

# Overview proposal

- a first language for logical plans, with separate input syntax (for humans and explanations) 
  and abstract syntax (for transformations/optimizations, simpler than the input)
  
  This contains constructs for:
  - rowwise transformations
  - filters
  - reductions (SQL aggregations)
  - concurrent (independent) operations
  - with some intelligence borrowed from Sawzall ("aggregators" = smart routing)
  
  The semantic model is a synchronous process network, which is equivalent to
  functional composition of stream transformers
  
- a second language for physical plans, this is just processes and connections.


# Language for logical plans

## Getting a feeling for it

An example simple logical program,
that just produces one row of data:

`gen Age,Name : Age = 30, Name = 'Radu'`

This is equivalent to, and could be produced as the plan for, the SQL query:

```sql 
SELECT 30 as Age, 'Radu' as Name
``` 

In the logical plan language, we can give any program a name:

```
let simple = gen Age,Name : Age = 30, Name = 'Radu'
in simple
```

Once a program has a name, we can compose it with another. For example
`trans` can perform rowise computations.

```
let simple = ...
in simple . (trans Age,Name -> Age,Name : Age = Age+10)
```

This would be a possible translation for the SQL query:

```sql
SELECT Age+10 as Age, Name FROM (SELECT 30 AS Age, 'Radu' AS Name)
```

The '.' says, take all the output from the program on the left and
give it as input to the program on the right. The column names
must match on both sides.

The program "`trans Age,Name -> Age,Name : Age = Age+10`" says:
- this is a transformers that takes rows with columns (Age, Name) as input
- produces rows with columns (Age,Name) as output
- performs the computation Age(output) = Age(input)+10 
- keeps Name unchanged

Actually `gen` is not a primitive construct, the real primitive
construct is `init` which produces a single row with no columns (empty
tuple). So `gen T : E` is really equivalent to `init . trans /*nothing*/ -> T : E`.

## Simple queries

The programs so far work on a single constant row of data. This is not
very interesting!

Let's look at the primitive program `scan` which reads values
from the database. Scan is not a source generator but really a transformer:
it takes rows with a single key range column as input, and produces rows of values as output.

For example if we want to read all rows from range `/10/20-/10/30`, where
the schema says we have 2 columns in each row, we would say:

```
(gen Keys : Keys = '/10/20-/10/30') . (scan Keys -> Age, Name)
```

Using the constructs so far we can construct more interesting logical programs. For example
the SQL query

```sql
SELECT Age + 30 AS Older, Name FROM Foo
```

would be translated as:

```
let src = (gen Keys : Keys = '/Foo/*') . (scan Keys -> Age, Name)
in src . (trans Age,Name -> Older, Name : Older = Age + 30)
```

The next useful program is `filter`, which only keeps rows of its input
that match a boolean expression. The syntax is trivial: `filter Columns... : Expression`.

For example:

```
let src = ...
in src . (filter Age,Name : Age > 30)
```

The columns indicate the interface of the filter. All columns of the input are passed
through to the output. The expression can only use listed column names.

With the constructs so far we can implement an optimized. Given the schema and query:

```sql
CREATE TABLE foo (Name TEXT PRIMARY KEY, Age INT)
```

We can run the following query:

```sql
SELECT Age + 10 AS Older FROM foo WHERE Name = 'Radu' AND Age > 30
```

using the following program:

```
let src = (gen Keys : Keys = '/foo/Radu') . (scan Keys -> Age)
in src 
 . (filter Age : Age > 30) 
 . (trans Age -> Older : Older = Age + 10
```

And then, for an indexed read:

```sql
CREATE TABLE foo (Name TEXT PRIMARY KEY, Age INT, INDEX idx(Age))
SELECT Name, Age + 10 AS Older FROM foo WHERE Age >= 20 and Age < 30
```

we can use the following:

```
let src = (gen Keys : Keys = '/idx/20-/idx/30') 
        . (scan Keys -> Primary)
		. (scan Primary -> Age, Name)
in src . (trans Age, Name -> Name, Older : Older = Age + 10)
```

## More simple queries

The next "simple" primitive programs are:

- `limit T : Num` take only the `Num` first rows of the input, then stop.
- `sort T1 : T2` sort the input rows containing columns `T1` using the keys listed in tuple `T2`.
- `update` (interface TBD): modify rows in the database. This takes the key,values to update
  as input (potentially also a condition for CPut) and reports which rows were modified as output.
  
Using these primitives and the constructs so far we can implement more complex queries. For example:

```
// SQL: SELECT Age,Name FROM foo ORDER BY Age
let src = (gen Keys : Keys = '/foo/*') . (scan Keys -> Age,Name)
in src . (sort Age,Name : Age) 
```

```
// SQL: SELECT Age,Name FROM foo ORDER BY Age LIMIT 10
let src = (gen Keys : Keys = '/foo/*') . (scan Keys -> Age,Name)
in src . (sort Age,Name : Age) . (limit Age,Name : 10)
```

```
// SQL: SELECT Age,Name FROM (SELECT Age,Name FROM foo LIMIT 10) ORDER BY Age
let src = (gen Keys : Keys = '/foo/*') . (scan Keys -> Age,Name)
in src . (limit Age,Name : 10) . (sort Age,Name : Age)
```

## Simple aggregations

The primitive program `reduce` provides aggregation, with a syntax similar to `trans`:

```reduce T -> T : Col = Op(<expr>), Col = Op(<expr>), ...``

This primitive performs one or more aggregations over all its input rows, then 
produces 1 output row with the result(s).

For example:

```reduce Age -> MaxAge : MaxAge = MAX(Age)```

reports the maximum age in the input as a single row.

This way we can evaluate SQL queries like the following:

```sql
SELECT COUNT(*) AS c, MAX(Age) as m FROM foo
```

using:

```
let src = (gen Keys : Keys = '/foo/*') . (scan Keys -> Age)
in src . (reduce Age -> c,m : c = count(), m = max(Age)) 
```

It is possible to reuse column names from the input interface in the
output interface without defining a reduction for them. When this
occurs, the reduction simply copies the corresponding columns from the
last row that was processed, and adds them to the output. This way we
can execute the following:

```sql
SELECT Age, COUNT(*) AS c FROM foo
```

using:

```
let src = (gen Keys : Keys = '/foo/*') . (scan Keys -> Age)
in src . (reduce Age -> Age,c : c = count()) 
```

## Aggregation with grouping

So far the examples have only used the binary operator '.' to compose two programs.

The language also offers a unary operator for grouping, noted as
`[ T ] ( E )`, that is: a tuple between square brackets, followed by a
program between parentheses.

What this means: the input rows coming into the grouping operator
are split into sub-sequences (groups), one per distinct value of the columns identified by the tuple. 
The inner program is then instantiated once for each group, and ran independently for its group.

The outputs of the instances of the inner program are then merged non-deterministically [*].

Once we have this, we can perform grouped aggregations easily. For example in SQL:

```
SELECT COUNT(*) + 10 FROM foo GROUP BY Age
```

can be computed using:

```
let src = (gen Keys : Keys = '/foo/*') . (scan Keys -> Age)
in src 
. [Age](reduce Age -> c : c = count())
. (trans c -> c : c = c + 10)
```

From a logical perspective this plan is also equivalent to:

```
let src = (gen Keys : Keys = '/foo/*') . (scan Keys -> Age)
in src 
. [Age]( (reduce Age -> c : c = count())
       . (trans c -> c : c = c + 10)
	   )
```

because applying a rowwise transformation on the subgroups gives identical results
as applying them on the combined result of the grouping. 

However at the moment where
we start transforming this to a physical plan a difference starts to appear: the
grouping operator also gives us an opportunity to parallelize the computation,
and then it becomes advantageous to do as much work as possible inside the grouping
operator where the work can be spread to multiple cores/machines.


[*] The fact that the grouping operator destroys the outer order is acceptable, since
SQL does not guarantee ordering anyway:

http://sqlblog.com/blogs/alexander_kuznetsov/archive/2009/05/20/without-order-by-there-is-no-default-sort-order.aspx

https://blogs.msdn.microsoft.com/conor_cunningham_msft/2008/08/27/no-seatbelt-expecting-order-without-order-by/

## Distinct values

There needs not be any separate primitive program for SQL's `distinct` keyword. Indeed, the following query:

```sql
SELECT COUNT(DISTINCT Age) AS c FROM foo
```

can be run as:

```
let src = (gen Keys : Keys = '/foo/*') . (scan Keys -> Age)
in src . [Age] ( limit Age : 1 ) . ( reduce Age -> c : c = count() )
```

Here, `limit` takes the first record in each input group and emits it as output,
which means only 1 row for each value of Age is given as input to the counter
reductor on the right.

## Joins




## Input syntax - BNF

```yacc
%%
Net : Conn
    | 'let' Defs 'in' Conn
    ;

Defs : Def
     | Def 'and' Defs
     ;

Def : <ident> '=' Atom
    |  <ident> '=' Net
    ;
Atom :
        // general-purpose row function:
        'trans' Sel  '->' Sel  ':' Transforms
        // reducers
    |   'reduce' Sel '->' Sel ':' Redux
    	// keep only rows satisfying the expression:
    |   Sel ':' <expr> 
        // sorter
    |   'sort' Sel ':' Sel
    	// keep only the head of the inut
    |   Sel 'limit' <number>
    	// scan an index/table extracting the specified columns
    |   'read' '(' ... ')' '->' Sel
        // update/set an index/table to the specified values, inform which rows have caused an update
    |   'update' '(' ... ':' Sel ')' '->' Sel
    ;
Transforms : Transform | Transform ',' Transforms ;
Transform : <identifier> '=' <expr>
Redux : Reduce | Reduce ',' Redux ;
Reduce : <identifier> '=' RedOp '(' <expr> ')'

Conn : <ident>
       | '(' Atom ')'
       | Composition
       | Separation
       ;
Composition : Net '.' Net
            ;
Separation  : '[' Sel ']' Net
            ;
	    
Sel : <ident> | <ident> ',' Sel
     | '*'
     ;                 
```

## Examples:

/////////////////
// SELECT COUNT(DISTINCT(v)) + 1 AS c FROM foo

(scan(foo):v) . [v] (limit 1) . (reduce count(*) as c) . (keep c) . (func{c+1}:c)

/////////////////
// SELECT COUNT(DISTINCT(v)) AS c FROM foo GROUP BY age

(scan(foo):v,age) . [age]( [v](limit 1) . (reduce count(*) : c) . (keep c) )

///////////////////////
// SELECT * FROM (SELECT COUNT(*) AS c FROM foo GROUP BY age ORDER BY c)
// LIMIT 10;

let subselect = (scan(foo):*) . [age](reduce count(*) : c) . (sort c)
in subselect . (limit 10)

///////////////////////
// CREATE v  (name PRIMARY KEY, age INT, INDEX foo(age), accountnr INT)
// SELECT COUNT(DISTINCT(accountr)) AS c FROM v WHERE age > 10 and age < 30
// GROUP BY age HAVING MIN(Name) > 'k'

(Scan(foo):(age,name))
. filter(age>10) .  filter(age<30)
. [age]( (reduce min(age) : tmp)
       . filter(tmp > 'k')
       . (keep accountnr)
       . distinct(accountnr)
       . (reduce count : c)
       . (keep c))

///////////////////////
// SELECT Age, Sum(whatever) as s FROM v GROUP BY Age ORDER BY Age

(Scan(v):Age,whatever) . [age]( (reduce sum(whatever) : s) )


## Abstract syntax:

Sel := tuple of ColName | '*' ;
Node := Atom | C(Node, Node) | S(Sel, Node) ;
Atom := Func(Prog,Sel)
     | Scan(..., Sel)
     | Update(...)
     | Limiter
     | Reduce(Op, id)
     | Distinct(Sel)
     | Sort(Sel)
     ;
 
### Examples:

// (scan(foo):age) . (func{age+10}:res)

C( Scan(...foo...,(age,)) ,
   Func((age,), (res,), ...)
   )

// (scan(foo):*) . [age] (reduce count : c)

C( Scan(...foo..., *),
   S( (age,)
      Reduce(count, c) ) )


# Semantics

Each Node in the abstract tree is conceptually
a network with 1 input port and 1 output port.

Composition rules:

  A . B   - connect A's output to B's input
[sel] N   - duplicate N virtually many times,
            and distribute the network's input rows
	    along the duplicates of N depending on the value
	    of the selection. All the outputs
	    of the duplicates of N are merged non-deterministically to
	    form a common output

Typing rules:

- each network is identified by its input and output sets of column names.

In[ C(A, B) ] = In[ A ]
Out[ C(A, B) ] = Out[ B ]

In[ S(Sel, N) ] = Union(Sel, In[ N ])
Out[ S(Sel, N) ] = Out[ N ]

In[ Func(Prog, Sel) ] = In[ Prog ]+
Out[ Func(Prog, Sel) ] = Sel

In[ Keep(Sel) ] = Sel+
Out[ Keep(Sel) ] = Sel


# Transformation to a physical plan



