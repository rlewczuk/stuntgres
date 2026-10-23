# internal/parser

The SQL lexer, goyacc grammar and AST. It imports only the standard library and
`sqlstate`; binding, name resolution and execution policy (including the
single-statement limit of driver calls) belong to callers.

## Layout and entry points

- `parser.go`: `Parse` and `ParseWithOptions` consume one complete SQL script;
  `Options` (standard-conforming strings, context, notice sink, closed routine
  formals, trusted line-comment hook) are captured per call.
- `type_input.go`: `ParseTypeName` for standalone type input;
  `regclass.go`, `regprocedure.go`, `regoperator.go`: reg* input splitters.
- `ast.go`: statement and expression nodes; feature-specific nodes live in
  files named after the feature.
- `lexer.go`: the scanner and lexer state; `keywords.go`: the PostgreSQL 18
  keyword list with its categories (`Unreserved`, `ColumnName`,
  `TypeFunctionName`, `Reserved`) and bare-label flags.
- `grammar.y`: edit it, then run `make generate`. Never hand-edit
  `grammar_gen.go`. Generator details and the rule against publishing goyacc
  semantic-cell addresses (`&$n`) are in
  [docs/DEVELOPMENT.md](../../docs/DEVELOPMENT.md).

## Invariants

- Parser state and options are per call. The AST keeps source positions
  (`Position{Offset, Character}`: byte offset and character index) and
  parameter indices.
- The entire input is consumed: a valid prefix followed by junk is an error,
  never a partial parse.
- Grammar actions copy semantic values and allocate AST nodes and backing
  slices independently, so no returned AST depends on parser lifetime.

### Scratch receivers

Scratch reuse is an allocation optimization, not a parsed-result cache.

- Each SQL parse, restricted INSERT VALUES replay and standalone type-input
  parse borrows one exclusive generated receiver (`parseLexer` in
  `parser_scratch.go`). `parseWithScratch` runs generated cleanup, then a full
  typed-zero reset, before the receiver returns, including on error,
  cancellation and callback panic. Production code never recovers panics.
- Lexers, options, contexts, notices and ASTs are never pooled. Expanded goyacc
  stacks stay invocation-local.
- One private `sync.Pool` holds fixed-size cleared receivers. Retention is
  GC-evictable, with no object-count bound; checkout never waits or caps active
  parses.
- Tests must not assume post-return receiver identity, a guaranteed pool hit or
  GC eviction. Reset tests use the explicit receiver helper, not the pool.

### Grammar conventions

- The AGGREGATE create-head token stays an unreserved identifier in ordinary
  table, column and function contexts; it is not a generic `CREATE name` head.
- SQL/JSON clause words are contextual to the selected call's own parenthesis
  depth. Wrapper introducers use scoped tokens so `::jsonpath` does not consume
  WITH/WITHOUT as a type-name tail; DEFAULT expressions ending in TRUE/FALSE/NULL
  keep their ordinary literal tokens.
- `JSON_TABLE` is a dedicated FROM item; its column types use the signature
  declaration grammar, and `Expressions`/`WithExpressions` visit and replace
  document, PASSING and DEFAULT children without aliasing the original slices.
- Qualified-operator lookahead preserves scanner errors (message and
  position) instead of replacing them with a later syntax error at `OPERATOR`.
- Type/function-name keywords (`authorization`, `join`, `like`, ...) use the
  ordinary call grammar without becoming table or column identifiers; binding,
  not spelling, selects a provider.
- Recursive view lowering keeps the original body, projects aliases from an
  unqualified lexical self and gives synthetic nodes zero positions.
- SEARCH-bearing CTE expandability is deferred to the binder; CYCLE-only,
  ordinary recursion and subquery/count restrictions stay early.
- The unreserved `operator` keyword keeps its contextual token after ADD or a
  comma in operator-family member lists.
- COPY keeps its complete WHERE expression and ordered positioned options;
  COPY TO WHERE fails at the WHERE token during parsing.
- CREATE DATABASE retains the decoded name and ordered `DatabaseOption`
  occurrences with one-based character positions. DEFAULT differs from quoted
  `'default'`, and numeric values differ from strings. Optional WITH/equals never
  erase duplicates. Complete-input parsing is effect-free; execution owns
  option semantics, permissions and transaction admission.

### Input-origin markers

Reg* alias splitters mark only their own save-context failures with the shared
`sqlstate` input-origin marker (`SoftInputError`). Decoded type input's own
empty/SETOF failures are soft; raw type-grammar errors are explicitly hard
(`HardInputError`). Callback errors keep their origin, so a signature's
type-parser or typmod exception never becomes a recoverable delimiter error.
Diagnostic presentation may unwrap to copy fields but keeps the outer origin.

`ParsePLpgSQL` uses a private lexer/goyacc entry and ordinary SQL expression
grammar, preserving positions and fully parsing unreachable syntax, including
ordered exception handlers and the bare-rethrow discriminator. SQLSTATE literals
are checked at parse time. Contextual PL keywords do not change SQL parsing,
quoted identifiers or configured prewalk behavior.

## Testing

Unit tests here drive the real lexer and generated parser and check AST shape,
positions, ownership and complete-input rejection. Public SQL acceptance and
diagnostics belong in `reference/`; follow
[docs/TESTING.md](../../docs/TESTING.md). Tests are mostly named after the
feature (`TestQueryWindow*`, `TestRowLock*`, `TestNotNull*`, ...), so select by
feature prefix.

- Keywords and ordinary keyword calls: `RUN='^Test(Keyword|OrdinaryKeywordCallHeads)'`
- Scratch receivers: `RUN='^TestParseScratch'` (also with `RACE=1`)
- SQL/JSON and JSON_TABLE: `RUN='^Test(SQLJSONQueryParser|JSONTableParser|JSONBPathOpsParser)$'`
- CTE SEARCH and recursive views: `RUN='^Test(CTESearch|CreateRecursiveView)'`
- Access methods and operator families: `RUN='^Test(AccessMethodGrammar|OperatorFamilyParser)$'`
- COPY options and WHERE: `RUN='^TestCopyFilterParser$'`
- Typed literals and operators: `RUN='^Test(GeometryConversionCLiteralSyntax|INETSubnetParser)$'`
- Database creation: `RUN='^TestCreateDatabase'`
- PL block syntax and full consumption: `RUN='^TestPLpgSQLParserFullConsumption$'`
- Exception syntax and handler full consumption: `RUN='^TestPLExceptionParsing$'`

**Benchmarks.** Compare scratch-receiver changes with the unchanged
`parser_scratch_bench_test.go` harness before and after:
`BenchmarkParseScratchMixed` (one fifteen-input cycle) and
`BenchmarkParseScratchNested` (one restricted multirow INSERT), serial and
parallel. Run both at default startup P and in a separate process started with
`GOMAXPROCS=1` (`-cpu` alone does not change startup-initialized capacity).
Report ns/op, B/op and allocs/op separately, and attribute allocations at every
actual receiver allocation site with matched profiles. Pool discards under GC
or the race detector are normal; never disable GC or rely on warm identity.
