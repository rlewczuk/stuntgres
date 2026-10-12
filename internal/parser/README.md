# internal/parser

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

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
