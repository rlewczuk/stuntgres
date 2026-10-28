# internal/parser

The SQL lexer, goyacc grammar and AST. It imports only the standard library and
`sqlstate`; binding, name resolution and execution policy (including the
single-statement limit of driver calls) belong to callers.

## Layout and entry points

- `parser.go`: `Parse` and `ParseWithOptions` consume one complete SQL script;
  `Options` (standard-conforming strings, context, notice sink, closed routine
  formals, trusted line-comment hook) are captured per call.
  Ordinary `ParseWithOptions` checks cancellation at its public admission
  boundary before delegating to shared lexer work; scanner polls continue
  during parsing. Readonly declaration acquisition retains its own initial
  cancellation guard without moving the ordinary admission frontier.
  `ParseRoutineDeclarationBody` uses the same complete-input lexer/grammar with a
  private per-parse readonly recipe mode. Only the selected-call scalar execution
  frontier is relaxed so genuine released declaration arithmetic can be retained
  as metadata. Ordinary `Parse`, `ParseWithOptions`, their options and prewalk
  behavior remain unchanged; this entry grants no executable admission or
  provider authority. The proc owner privately retains its syntax behind an
  immutable structural carrier rather than submitting it for execution.
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
- Column expressions share one-, two- and three-component syntax in every
  expression consumer, including casts, calls and grouped operands. Each
  identifier retains its normalized spelling and source position; grouping
  does not carry a separate qualification policy. The semantic owner decides
  which rows are visible and whether its context permits column references.
  FROM-list commas retain explicit `JoinReference.Comma` provenance, separate
  from CROSS JOIN despite identical cross-product execution. Binding consumes
  this syntax fact for namespace visibility, never source-text inspection.
- Setting calls use ordinary SELECT/function expression syntax, including aliases,
  concatenation, FROM and logical, NULL-predicate or nested-call arguments.
  Binding owns signatures and implicit function applicability before assignment
  coercion. Call-start and final name-component positions remain distinct, and
  arguments retain their own positions through casts and nesting.
  Output labels live in `Select.Labels`, independently of the target
  `FunctionExpr` name and operand positions. The ordinary projection rules own
  explicit-AS, quoted and admitted bare labels, including LIKE/ILIKE/SIMILAR;
  a following pattern operand instead produces the ordinary operator expression.
  `TestSettingsCallOutputLabelsGrammar` covers this current-owner boundary,
  complete-input rejection and independently retained concurrent parse results.
  Ordinary selected calls require the FILTER opener before rejecting unsupported
  modifiers; incomplete FILTER reports its natural syntax error. Supported
  aggregate/window FILTER syntax and quoted or AS labels remain independent.
  Recovery registry calls retain the same failure-only star/decorator syntax
  for ordinary binder diagnostics; this lexical admission grants no aggregate
  capability and preserves quoted function spelling and bare identifiers.
- Standalone `Call` retains the command position and the positioned shared
  `FunctionExpr` argument/notation syntax. Raw expression visitors include every
  actual, including pure OUT placeholders; CALL remains an unreserved name.
- `Explain` retains its child statement, command `Position` and ordered
  `ExplainOption{Name, Value, Pos}` entries. An omitted value is nil; literals
  retain `LiteralExpr` kinds and identifier values use `ColumnExpr`. All
  positions refer to the original complete input, including legacy options.
  Query/source and mutation-target discovery inspect the child for planning;
  the wrapper itself is not modifying and exposes no executable WITH namespace.
  SQL PREPARE and SQL routine-body grammar do not gain utility eligibility.
- Routine signature types retain SETOF in the AST for declaration validation,
  including positioned named/mode-bearing formals. The lexer scopes its type
  token to formal declarations (excluding defaults), the first token of an actual
  RETURNS clause, SQL/JSON result types, operator options and standalone type
  input; ordinary identifiers retain its ColumnName keyword category. The
  RETURNS grammar reduction arms a one-token state, consumed by the next lexer
  call even for a quoted or non-SETOF token; it never extends into the body.
  General type productions are not SETOF-recursive.
- `SET [SESSION|LOCAL] XML OPTION DOCUMENT/CONTENT` lowers to the existing
  xmloption Set node. XML has an explicit unreserved terminal to distinguish
  this prefix from SET CONSTRAINTS, while retaining its ordinary identifier,
  type-name and typed-literal contexts. No XML evaluator is implied.
- Trigger declarations retain explicit timing/row kind, ordered event syntax,
  positioned WHEN and literal arguments. Parsing does not reinterpret the default
  statement kind as ROW; execution admits the supported timing and target kind.
  Timing words require keyword identity: quoted `"before"`/`"after"` are names,
  not timing syntax, and cannot install definitions.
  ALTER/DROP retain positioned table-local trigger identities and explicit
  rename, extension-dependency, existence and drop-mode syntax. Preparing these
  utilities parses only; execution resolves relation, trigger and owner.
- ALTER TABLE and ALTER DOMAIN share contextual `validate_word`, requiring
  keyword identity rather than quoted-identifier spelling. Table validation
  retains the action and constraint-name positions in ordinary complete-input
  action lists; execution owns current constraint lookup and authority.
  Rule-mode actions retain a distinct action kind and positioned rule name;
  relation-only IF EXISTS does not add a per-rule existence modifier or wildcard.
- Statistics owner replay has a per-call `StatisticsOwnerReplay` option only
  for the isolated native Restore parser. It yields the shared positioned
  `AlterOwner` AST; ordinary calls retain the ALTER capability diagnostic.
  Parsing permission supplies no execution or ownership authority.
  The dedicated CREATE head cannot fall into generic unsupported-CREATE parsing
  on incomplete input, without changing STATISTICS's unreserved name legality.
  A supplied kind list requires at least one identifier.
  CREATE retains ordered `Elements []Expr`, reusing column, function and grouped
  expression productions. Grouping a column does not change its semantic kind;
  no SQL-text scanner or alternate expression grammar determines statistics keys.
- ALTER ROLE and USER share positioned rename and persisted-setting nodes.
  Role specs retain execute-time current/session identity, while ALL and optional
  database scope are separate from RESET ALL. Setting lists reuse ordinary SET
  extraction; DEFAULT and FROM CURRENT remain explicit actions. Complete grammar
  shapes, not prefix guards, reject malformed suffixes before execution.
- Large-object ownership retains a positioned `NumericOnly` node in `AlterOwner`:
  scanner int32 integers keep their decoded value; larger and real spellings stay
  textual. OID input and role/object resolution belong to execution, never parsing.

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
- Aggregate ALTER/DROP signatures lower to the shared routine utility AST with
  kind `aggregate`, independently of GRANT/REVOKE syntax. They require a
  nonempty identity or `(*)`, reject output modes at the argument position,
  and share flattened ordered-set declaration identity with CREATE. CREATE also
  retains the ordered discriminator and direct count, including zero direct
  inputs. Duplicate paired VARIADIC arguments compare parsed type names, including
  implicit pg_catalog components of SQL aliases, not resolved type OIDs.
  Aggregate utility heads retain generic declaration-name components so
  canonical operand labels cannot erase that parsed qualification distinction.
- Routine CREATE and attribute ALTER retain ordered `RoutineAttribute` clauses,
  values/settings and introducer positions in every admitted body order.
  Duplicate groups and procedure-inapplicable attributes are utility-execution
  decisions, not parser errors. Complete grammar-owned shapes retain signed
  numbers, NULL-input aliases, SECURITY/EXTERNAL SECURITY, PARALLEL and setting
  lists/DEFAULT/FROM CURRENT; unknown or incomplete clauses fail the whole parse.
  Security modes require unquoted DEFINER/INVOKER keyword provenance and normalize
  to those two values; malformed modes fail at their own token before utility
  admission. Clause words remain unreserved names outside declarations. ALTER
  RESET actions retain their original order; identity/owner/rename/schema actions
  stay distinct.
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
- Non-VARIADIC named argument right-hand sides use the ordinary predicate
  grammar, retaining comparison/logical trees and labels in both scalar and
  FROM calls. Positional and VARIADIC argument grammar remains operand-only.
- Selected ordinary calls retain FILTER and OVER on their actual function node,
  not on a parenthesized or composed scalar. Duplicate FILTER and FILTER after
  OVER are syntax errors; binding owns aggregate/window versus scalar admission.
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
Unreserved FIRST and SECOND are also positioned PL target names; FETCH FIRST
retains its separate cursor-motion production, and SECOND remains a temporal field.
RAISE retains severity, explicit source form, decoded format, ordered arguments
and option occurrences; bare rethrow remains distinct. ASSERT retains separate
condition and optional-message spans. Their depth-zero commas and RAISE USING
boundaries do not consume nested SQL delimiters or quoted identifiers.
GET diagnostics retains its area, ordered target/item associations and original
positions. GET and its area/head words are contextual, preserving ordinary
identifier assignments. The parser seals syntax-only admission; checked
validation rejects coordinated target/item/area mutation as well as extra variants.
LOOP, WHILE and cursor-FOR bodies retain opening/closing labels and an explicit
loop discriminator. WHILE retains a scalar projection condition, its optional
alias and token-exclusive span separately from the WHILE and LOOP positions.
Its depth-zero LOOP boundary shares the ordinary operand grammar and does not
change quoted PL names or ordinary SQL identifiers.
Transfers retain positioned labels and WHEN expression boundaries;
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
and original SQL with every byte of the procedural interval blanked to a space,
including CR/LF. SQL trivia outside that interval and original source coordinates
remain unchanged; the borrowed text supplies exact execution-context spelling.
At every INTO placement,
the raw-token probe completes the datum/field list structurally before returning
the remaining tokens to ordinary SQL grammar, without a SQL-tail keyword list.
It excludes nested SQL and INSERT/MERGE's relation INTO. A MERGE target alias
therefore stays in the ordinary SQL grammar, not the procedural target list.
Field components retain quoted
identity and PL reserved-word admission; malformed fields fail at their dot.
Static INTO rejects array subscripts at the opening bracket, independently of
the broader assignment/FETCH target grammar. The bounded targets use the
generated PL target-list entry; SQL table-creation INTO remains a separate AST.
An isolated procedural target without an assignment operator is rejected at its
original head token, not at the following statement delimiter.
Static commands, contextual PERFORM and PL CALL retain distinct `PLSQLKind`
policies with ordinary statement ASTs and original source spans/text. No-INTO
SQL is parsed without deciding its runtime destination policy. Statement-head
recognition excludes DECLARE sections, including names after declaration
semicolons. PERFORM changes only the grammar head; its diagnostic SELECT
coordinates belong to the host.
Utility statements share the ordinary SQL command grammar, including bounded
SET TRANSACTION characteristics. Dynamic EXECUTE has its own instruction.
COMMIT/ROLLBACK retain a dedicated positioned `PLTransaction` payload with
optional AND [NO] CHAIN and parser-owned admission; lexical BEGIN/END remain
blocks. Runtime eligibility is not a declaration-time parser decision.
The completed static-statement grammar reduction retains an unexported,
by-value admission association. `ValidPLSQLStatement` checks the original AST
identity, variant and source interval/text without reparsing or a second
statement-type whitelist. This syntax-only association is shared with the AST.
Dynamic EXECUTE retains a `PLDynamicStatement`, with independently positioned
command/USING expressions and optional ordered INTO targets. Both clause orders
share the depth-aware scalar boundary; quoted text, comments and nested SQL
cannot delimit operands. RETURN QUERY EXECUTE has its own mode, not a scalar
RETURN or static SQL command. Parser admission preserves the original mode,
operands, targets and source bounds for checked-program validation; no computed
SQL or execution plan belongs to this payload.
Duplicate USING diagnostics retain the repeated clause's first operand position,
independently of later delimiter lookahead.
The private EXECUTE head token is contextual to standalone statements and
RETURN QUERY; OPEN FOR EXECUTE retains its existing cursor grammar token.

GET diagnostics likewise seals its exact source interval at the statement-list
semicolon. Checked owners compare that admitted text and UTF8 boundaries with
their definition source before sharing syntax. Quoted item identifiers, including
Unicode-quoted spellings of known items, follow the unknown-item scanner path.

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
Trigger-mode selectors, quoted names and complete-input AST units use
`RUN='^TestTriggerModeGrammar$'`.
Static capture AST/boundary units use `RUN='^TestPLpgSQLSelectIntoParser$'`.
Embedded static SQL variants and per-call syntax ownership use
`RUN='^TestPLpgSQLSQLExecutionParser$'`.
Diagnostics syntax/admission ownership uses `RUN='^TestPLpgSQLDiagnostics'`.
Routine declaration syntax and positioned CALL/notation/full-input ownership units use
`RUN='^TestRoutine(ResultDeclarations|ProcedureCall|Attribute)'`.
Statistics owner replay option isolation and full-input positions use
`RUN='^TestStatisticsObjectOwnerReplayParser$'`.
Bounded `ALTER STATISTICS [IF EXISTS] ... SET STATISTICS` has its own positioned
AST, independent of the private owner-replay option. Signed values reuse the
released int32-magnitude grammar fence; DEFAULT retains distinct catalog meaning.
AST/options/boundaries use `RUN='^TestStatisticsNDistinctTargetParser$'`.

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
- EXPLAIN wrapper/options, child positions and traversal: `RUN='^TestExplainParser'`
- Typed literals and operators: `RUN='^Test(GeometryConversionCLiteralSyntax|INETSubnetParser)$'`
- Database creation: `RUN='^TestCreateDatabase'`
- PL block/conditional/control syntax and full consumption: `RUN='^TestPLpgSQL(ConditionalParser|LoopParser|WhileParser|QueryLoopParser|ParserFullConsumption)$'`
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
