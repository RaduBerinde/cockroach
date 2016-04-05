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
construct is `init` which creates a single row with no columns (empty
tuple). `gen T : E` is really equivalent to `init . trans /*nothing*/ -> T : E`.

The programs so far work on a single constant row of data. This is not
very interesting, so we also have the program `scan` which reads values
from the database. Scan is not a source generator but really a transformer:
it takes rows of keys as input, and produces rows of values as output.

For example if we want to read all rows from prefix `/10/20`, where
the schema says we have 2 columns in each row, we would say:

```
(gen Key : Key = '/10/20') . (scan Key -> (Age, Name))
```

Using the constructs so far we can construct more interesting logical programs. For example
the SQL query

```sql
SELECT Age + 30 AS Older, Name FROM Foo
```

would be translated as:

```
let src = (gen Key : Key = '/10/20') . (scan Key -> (Age, Name))
in src . (trans Age,Name -> Older, Name : Older = Age + 30)
```

The next useful program is `filter`, which only keeps rows of its input
that match a boolean expression. The syntax is trivial: `filter Columns... : Expression`.

For example:

```
let src = ...
in src . (filter (Age,Name) : Age > 30)
```

The columns indicate the interface of the filter. All columns of the input are passed
through to the output. The expression can only use listed column names.



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

### Examples:

/////////////////
// SELECT age + 10 AS res FROM foo

let src = scan(foo) -> age
    mod = trans age -> res : res = age + 10
in src . mod

// alternatively:

(scan(foo)->age) . (trans age->res : res=age+10)

/////////////////
// SELECT age + 10 AS age2 FROM foo ORDER BY age2

let src = scan(foo) -> age
and mod = trans age->age2 : age2 = age + 10
and sorter = sort age2 : age2
in src . mod . sorter

// alternatively:
(scan(foo):age)
. (func{age+10}:age2)
. (sort age2)

/////////////////
// SELECT COUNT(*) AS c FROM foo

(scan(foo):*) . (reduce count(*) : c) . (keep c)

/////////////////
// SELECT COUNT(*) AS c FROM foo GROUP BY age

(scan(foo):*) . [age] ( (reduce count(*) : c) . (keep c) )

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



