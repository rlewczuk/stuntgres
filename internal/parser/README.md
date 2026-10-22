# internal/parser

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Recursive view lowering

`make check RUN='^TestCreateRecursiveView(Lowering|SyntheticPositions)$'`
checks the private AST contract: original body pointer/WITH/set shape and hidden
tuple width survive, the wrapper explicitly projects aliases from an unqualified
lexical self, synthetic positions are zero, and destination alias storage remains
independent. Public grammar combinations, mandatory-list, suffix and INTO
diagnostics belong to `TestReferenceRecursiveViewBoundaries` and Columns in
[the owning dual-mode suite](../../reference/views/README.md#recursive-view-contract).
Generation belongs to the normal make targets; never edit `grammar_gen.go`.

### SQL/JSON query syntax

`SQLJSONQueryExpr` retains document/path, ordered PASSING and lazy DEFAULT
children separately from constructors. Its Children/WithChildren contract is
shared by scalar admission, qualification, query visitation and raw capture.
Clause words are contextual to the selected call's own parenthesis depth;
quoted/qualified ordinary calls and nested expressions retain their own syntax.
Wrapper introducers use scoped clause tokens so a preceding `::jsonpath` does
not consume WITH/WITHOUT as a type-name tail. PASSING operands and aliases can
still use ordinary unquoted clause-word identifiers.
Boolean/NULL behavior words retain their ordinary literal tokens: DEFAULT
expressions ending in false, true or NULL must not be mistaken for standalone
ON EMPTY/ON ERROR behaviors. Edit `grammar.y`, never the generated parser.
`TestSQLJSONQueryParser` checks positioned nested clauses, child ownership,
DEFAULT literal children, nested/generated/index syntax and complete-input rejection:
`make check RUN='^TestSQLJSONQueryParser$'`.


### SQL/JSON relational syntax

`JSONTable` is a dedicated FROM item. `Expressions`/`WithExpressions` visit and
replace the document, ordered PASSING and recursively written DEFAULT children
without aliasing the original recursive slices. Paths retain original literal
positions, while COLUMNS/NESTED/PATH and existing query options use scoped depths.
Column types use the signature declaration grammar, avoiding the general table-
column two-word diagnostic guard that would consume a following EXISTS clause.
`TestJSONTableParser` checks recursive options, positions, replacement ownership,
central visitor coverage, context reset and full-input rejection:
`make check RUN='^TestJSONTableParser$'`.

### Qualified JSONPath operators

`TestJSONBPathOpsParser` checks generic maximal-munch `@?`/`@@`, qualified
operator AST positions, whole-input consumption and empty quoted schema
diagnostics. Qualified-operator lookahead preserves scanner failures rather
than replacing their exact English message and character position with a later
syntax error at `OPERATOR`. Public SQL direct/Prepare diagnostics remain in
`reference/binary_json_arrays`.

```sh
make check RUN='^TestJSONBPathOpsParser$'
```

### Ordinary keyword calls

Type/function-name keywords such as `authorization`, `join`, and `like` use
the existing ordinary call grammar without becoming legal table or column
identifiers. Binding—not keyword spelling—selects an executable provider or
reports a missing signature. Dedicated special-call syntax retains its own
admission rules.

```sh
make check RUN='^Test(OrdinaryKeywordCallHeads|CompositeExprParser|QueryLimitZeroWidthContinuation|KeywordDiagnostics|KeywordInventory|KeywordContexts|KeywordLabels)$'
```

The four `TestReferenceIdentifierContextualCapabilities`,
`TestReferenceIdentifierLaterProjectionCalls`,
`TestReferenceIdentifierContextSourceConsumerRoutes`, and
`TestReferenceIdentifierLaterCallSourceConsumerRoutes` definitions cover absent
and created ordinary calls through public consumer routes; run their selector
in native and `REFERENCE=1` modes.

### Parser scratch reuse

Parser scratch reuse is an internal allocation change, not a parsed-result cache.
Each SQL parse, restricted INSERT replay and standalone type-input parse borrows
an exclusive generated receiver. The whole receiver is cleared before return,
including on error or panic; lexers, options, contexts and ASTs are never pooled.
One private `sync.Pool` retains fixed-size cleared receivers; retention is
GC-evictable, not bounded by a hard object count. Checkout does not wait for
another invocation or cap active parses. No test may assume a particular
post-return receiver identity, guaranteed hit, or GC eviction. Grammar cells use
owned Column, AlterTableAction and WindowSpec pointers and compact ON COMMIT
action/position metadata; existing public AST storage boundaries stay unchanged.
Invocation-local grown stacks are not retained. `^TestParseScratch` in
`./internal/parser` covers deterministic full typed-zero reset after actual SQL/
type success, syntax error, in-flight cancellation and callback error/panic,
notice ownership/reentry, deep stacks, retained parameters/source positions,
declarations, lexical options and concurrent independent results/cancellation.
The production-used explicit receiver helper lets reset tests observe a known
receiver without manipulating the pool. Generated cleanup precedes full reset,
which precedes return; original panics are not recovered by production code.
Run `make check RUN='^TestParseScratch'` and its
`RACE=1` counterpart, then ordinary quickcheck after edits.

Performance evidence must compare the identical
`internal/parser/parser_scratch_bench_test.go` harness before and after changes.
`BenchmarkParseScratchMixed` measures one complete fifteen-input cycle;
`BenchmarkParseScratchNested` measures one restricted multirow INSERT.
Record serial/parallel samples, ns/op, B/op and allocs/op, absolute and percentage
deltas, toolchain/environment and actual commands/exits. Measure both default
startup P and a separate process started with `GOMAXPROCS=1`; `-cpu` alone does
not change a cache capacity initialized at startup. Attribute scratch bytes and
object counts at every actual receiver allocation site using matched profiles,
not only the unused generated constructor. GC and race-mode pool discards are
normal; neither warm identity assumptions nor disabled GC are acceptable.
The prior channel can already reuse ordinary serial parses at default P; reduced
P1 nested allocations do not promise default-P or whole-suite runtime gains.
Historical fresh-parser allocation profiles are motivation, not candidate
deltas. Complete real-reference race measurements are human-only; agents MUST NOT
invoke refcheck. Selected dual-mode proof and complete Docker-free verification
precede postintegration finalcheck. Disclose timeouts/failures honestly.

### Recursive SEARCH syntax

`make check RUN='^TestCTESearch(Grammar|Legality)$'` exercises symbolic mode/key
order, rune positions, SEARCH-before-CYCLE, unreserved names and existing FIRST
consumers, malformed identifier-only key lists and recursive legality precedence.
SEARCH-bearing expandability is deferred to binder analysis/rewrite boundaries;
the same validators retain CYCLE-only early errors and identity-aware nested WITH
shadowing. Ordinary recursion and subquery/count restrictions remain early.
Public SQL diagnostics/results remain in `reference/query_core`, not this package.

### Network subnet syntax

`make check RUN='^TestINETSubnetParser$'` covers all five existing symbolic
spellings, inclusive maximal munch, adjacent comments, qualified operators and
Unicode character positions. No new token or precedence convention is introduced.
Public signature/diagnostic expectations remain in `reference/types`.

### Operator-family member syntax

`TestOperatorFamilyParser` exercises the actual lexer/generated-parser path for
ADD/DROP operator entries, including subsequent comma-separated members, mixed
support entries, signatures, character positions and complete-input rejection.
The unreserved `operator` keyword retains its contextual token after ADD or a
comma; the family grammar accepts it without promoting ordinary identifiers.
Public SQL acceptance remains in the shared operator-family Smoke and
SecondaryUniqueness reference definitions.

```sh
make check RUN='^TestOperatorFamilyParser$'
```
