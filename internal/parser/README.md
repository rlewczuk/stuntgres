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
- Trigger declarations retain explicit timing/row kind, ordered event syntax,
  positioned WHEN and literal arguments. Parsing does not reinterpret the default
  statement kind as ROW; execution admits the supported timing and target kind.
  Timing words require keyword identity: quoted `"before"`/`"after"` are names,
  not timing syntax, and cannot install definitions.
  ALTER/DROP retain positioned table-local trigger identities and explicit
  rename, extension-dependency, existence and drop-mode syntax. Preparing these
  utilities parses only; execution resolves relation, trigger and owner.

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
- Numeric-leading `date`, `timetz` and `timestamptz` calls remain ordinary
  calls unless the complete signed/numeric precision, `)` and string-literal
  continuation is present. A fixed raw-token buffer preserves positions and
  scans each token once, including qualified temporal literal continuations;
  it never advances returned-token depth or changes prewalk policy.
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
are checked at parse time; quoted identifiers retain their identity.
IF/CASE branches retain arm order and distinguish absent from empty ELSE;
expression occurrences retain separate token-content and delimiter ends,
including grouping. Depth-zero procedural THEN/WHEN/comma terminals cannot be
consumed by the SQL scalar grammar; parentheses and brackets protect inner SQL
delimiters. Diagnostic spans exclude trailing trivia while preserving interior
comments and raw tokens. The caller supplies RETURN legality so it precedes
scalar syntax errors.
Expression-origin probes inherit the predecessor's byte/character coordinates,
so their two-token scan does not recount the preceding body.
Contextual PL keywords do not change SQL parsing or configured prewalk behavior.
RAISE retains severity, explicit source form, decoded format, ordered arguments
and option occurrences; bare rethrow remains distinct. ASSERT retains separate
condition and optional-message spans. Their depth-zero commas and RAISE USING
boundaries do not consume nested SQL delimiters or quoted identifiers.
LOOP and cursor-FOR bodies retain opening/closing labels and an explicit loop
discriminator. Transfers retain positioned labels and WHEN expression boundaries;
WHEN projection aliases use the ordinary projection grammar and remain positioned
syntax. Target legality belongs to checked compilation, including unreachable flow.
Label diagnostics surround decoded contents with literal quotes without applying
Go string escaping.
Static query FOR retains an existing positioned target, an ordinary SQL statement
and its own query/LOOP boundary. SELECT/WITH and DML use the same SQL grammar;
the procedural delimiter is recognized only outside parentheses/brackets and
only from an unquoted identifier/keyword token, never a string's decoded payload.
Integer FOR retains ordered target expressions/comma positions, REVERSE position
and separate lower/upper/optional-BY scalar projection spans. A bounded raw-token header probe
selects the range production without consuming live state or changing cursor/query
FOR. Only depth-zero `..`, BY and LOOP delimit operands; scalar grammar owns the
expressions and aliases. Consumed delimiter terminals carry emission-time operand
ends, so alias reductions and later lookahead cannot change their spans. REVERSE
is a one-shot header introducer after IN. Counter-name admission belongs to checked
compilation; integer-loop closing labels are checked after the required semicolon.
FOREACH retains existing name/field targets, target-comma positions,
explicit-versus-omitted SLICE and a positioned scalar array expression. Its
target grammar excludes subscripts without restricting ordinary assignments
or FETCH. The LOOP delimiter uses the same depth-aware boundary. SLICE remains
unreserved in procedural identifiers, labels, field names and type names;
checked lexical resolution determines whether a datum shadows its clause use.
SLICE accepts nonnegative signed-32-bit integer constants, including radix
prefixes and digit separators; leading-zero decimal spelling stays decimal.
Failed FOREACH syntax retains scanner-owned source/delimiter and following
assignment-head provenance for diagnostics only. Failure probes never return
tokens or syntax to executable parsing and cannot turn rejected input into success.
Static SELECT and DML RETURNING capture retain a separate `PLIntoStatement`:
ordinary SQL syntax, ordered target expressions, STRICT, positioned boundaries
and original SQL with the procedural interval blanked. At every INTO placement,
the raw-token probe completes the datum/field list structurally before returning
the remaining tokens to ordinary SQL grammar, without a SQL-tail keyword list.
It excludes nested SQL and INSERT's table INTO. Field components retain quoted
identity and PL reserved-word admission; malformed fields fail at their dot.
Static INTO rejects array subscripts at the opening bracket, independently of
the broader assignment/FETCH target grammar. The bounded targets use the
generated PL target-list entry; SQL table-creation INTO remains a separate AST.
An isolated procedural target without an assignment operator is rejected at its
original head token, not at the following statement delimiter.

Statement-list boundaries also retain an unknown identifier head until a real
assignment operator clears it. On grammar failure only, a bounded scanner checks
whether the required statement semicolon is absent at EOF; it never interprets
unknown SQL or changes accepted syntax. Closing END LOOP labels have their own
lexical context, distinct from statements following an opening LOOP.
Diagnostic compound-name accounting scans bounded original expression and
assignment-target tokens, not SQL AST node kinds. It shares identifier/Unicode
decoding with the lexer and applies PL's reserved-word list and greedy,
non-overlapping names of at most three parts; a following call parenthesis does
not change recognition.
Cursor declarations and OPEN/FETCH/MOVE/CLOSE/FOR statements have explicit
positioned payloads. Motion counts remain expressions; counted multirow FETCH
syntax remains distinguishable from single-row motion for checked compilation.
Bound actuals preserve names, separator positions and original expression spans.
Declaration/open queries, dynamic commands, each USING expression, motion counts
and FETCH target lists retain the same grammar-owned token-content boundaries as
ordinary PL expressions. Spans exclude leading/trailing trivia and delimiters,
preserve grouping/interior comments, and distinguish UTF8 byte/character offsets.
The statement keyword position remains separate from its operand/query origin.
Fixed FIRST/LAST token ends do not depend on scanner lookahead. Dynamic OPEN
retains an expression and ordered USING list, not a second SQL parser; diagnostic
text is sliced from these boundaries and never rescanned into executable syntax.

## Testing

Trigger lifecycle AST/position/full-input units use `RUN='^TestParseTriggerLifecycle$'`.
Static capture AST/boundary units use `RUN='^TestPLpgSQLSelectIntoParser$'`.

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
- PL block/conditional/control syntax and full consumption: `RUN='^TestPLpgSQL(ConditionalParser|LoopParser|QueryLoopParser|ParserFullConsumption)$'`
- Exception syntax and handler full consumption: `RUN='^TestPLExceptionParsing$'`
- Message and assertion syntax: `RUN='^TestPLpgSQL(Raise|Assert)Parser$'`
- Array-loop syntax, integer bounds, spans and cursor-keyword reachability: `RUN='^TestPLpgSQLForeach(Parser|SliceConstants|CursorKeyword)$'`

**Benchmarks.** Compare scratch-receiver changes with the unchanged
`parser_scratch_bench_test.go` harness before and after:
`BenchmarkParseScratchMixed` (one fifteen-input cycle) and
`BenchmarkParseScratchNested` (one restricted multirow INSERT), serial and
parallel. Run both at default startup P and in a separate process started with
`GOMAXPROCS=1` (`-cpu` alone does not change startup-initialized capacity).
Report ns/op, B/op and allocs/op separately, and attribute allocations at every
actual receiver allocation site with matched profiles. Pool discards under GC
or the race detector are normal; never disable GC or rely on warm identity.
