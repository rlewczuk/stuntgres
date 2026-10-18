# Engine split, step 3: `internal/engine/functions`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-functions.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §5 step 3; the diagnosis in
  §2.5; dependency rule 5 in §3.
- **Needs:** step 2 through 2b for F1 and F3–F5, and through 2c for F2.
- **Effort:** high for F1 and F4/F5; xhigh for F3 and for the first F2
  switch; high for the later F2 switches.
- **Session size:** F1 and F3 take one session each. F2 takes one or two
  sessions. F4/F5 cover one large family or a few small ones per session.
  This step spans many sessions; the Status section tracks where to resume.

## Goal

`functions` becomes the real home of builtin functions: plain nodes, their own
node operations, bind code that depends on a `functions.Binder` interface, and
a registry instead of the name if-chain. It stays a leaf library: it imports
`expr`, `ids`, `sqltype`, `parser` and `sqlstate`, never `catalog`, `storage`,
`lock`, `visibility` or the root engine.

## Sub-steps

### F1 — Remove generics (needs 2b)

- Each `X[E, C, P]` in `functions` becomes a plain `X` over `expr.Expr`,
  `expr.Context` and `expr.Info`. The context constraints
  (`LiteralContext[E]`, `BinaryContext[E]`, `CurrentDateTimeContext`, ...)
  fold into `expr.Context`. `functions.Info[P]` is replaced by `expr.Info`.
- In the engine, delete the instantiation suffixes (about 1,257 references at
  `a2252c0b1`) and the `functionInfo` conversions. Use tools, not hand edits.
- This is a broad rewrite: do not run it in parallel with another (§8).

### F2 — Adopt the node protocol, one switch per commit (needs 2c)

Step 2c converted the walk switch. Convert the others:

- retag (`expression_retag.go`);
- remap (`view_remap.go`);
- volatility (`expression_volatility.go`);
- deparse (`deparse_renderer.go`, `deparse_function.go`);
- node-tree output;
- generated-column admission (`generated.go`);
- `conditional_plan.go`;
- join legality.

Each switch gains one protocol branch (`case expr.Composite:` or a render
hook) and loses its per-function cases. Function nodes implement the protocol
methods next to their definitions. If a switch case encodes behavior that does
not fit the protocol, give the protocol a small, named hook rather than
keeping a special case, and record it in the `expr` README.

### F3 — Binder interface and registry, with a pilot family

- Define `functions.Binder`, starting from the methods that function glue uses
  most: `info`, `typ`, `builtinRoutineInfo`, `AnalyzeArgument`,
  `functionSignatureErrorForTypes`, `builtinVisible`, `diagnosticTypeName`,
  `CoerceArgument`, `comparisonOperand`, `coerceConditional` and
  `builtinRoutineReference`. Re-measure. `*expressionBinder` implements it.
- Define `Definition` and `Lookup(name)` over a static table in `functions`,
  with no `init()` registration.
- Engine dispatch (`bindFunctionProvider` in `function.go`, `bindFunction` in
  `composite_expression.go`) consults `Lookup` at the exact point where the
  if-chain tests names today. Then it falls through to the existing chain.
  Resolution precedence must not change: user-defined functions,
  `search_path`, builtin visibility, and overload and ambiguity errors.
- Port one small Tier A family end to end (F4 then F5) in the same session, so
  the registry lands with a real entry.

### F4/F5 — Port in place, then move, family by family

- **F4.** Convert `func (b *expressionBinder) bindX(...)` into
  `func bindX(b functions.Binder, ...)` while it is still in the engine. The
  compiler then proves the interface is enough. Every new `Binder` method is a
  reviewed decision, recorded in the `functions` README: either a genuine
  analyzer capability, or a leak that the plan should remove. Prefer passing
  values to adding methods.
- **F5.** Move the ported family into `functions` and register it. Delete its
  engine branch, its glue file and its now-dead switch cases. Its unit tests
  move with it.
- Extension kernels move with their data files (for example
  `unaccent.rules`, embedded by `unaccent_provider.go`). Extension
  installation and procedure identity stay in the engine; installed functions
  reach their procedure identity through a `Binder` capability.
- Aggregates and windows: signatures, provider selection and state algorithms
  move. FILTER, DISTINCT, ORDER, grouping and window execution stay in the
  engine's grouping code. `aggregate_bind.go` is the largest single item;
  split it over several sessions if needed.
- Tier C (catalog, ACL, xid, advisory-lock and sequence inquiry) waits for
  steps 4, 5 and 7. Refine it then: narrow capability interfaces in
  `functions`, or homes in the owning subsystem.

## Invariants

- Results, errors, SQLSTATEs, positions, notices, volatility and
  generated-column admission stay identical.
- Deparsed SQL in views, stored defaults, checks and native Dump output stays
  byte-identical.
- `functions` never reaches engine state except through `Binder` or
  `expr.Context`.

## Targeted tests

- The unit tests of the family or switch being changed. Grep for them.
- F2: reference selections for views, stored defaults and checks, generated
  columns and native Dump/Restore, run with `REFERENCE=1` and in quick mode.
- F3–F5: the family's reference tests, run with `REFERENCE=1` and in quick
  mode. Pick names by grepping `reference/`; never edit it.
- `make bench` after F1 and after any family on the startup path.

## Documentation

- Rewrite `internal/engine/functions/README.md`. Today it describes the
  shallow split. The rewrite covers: the package contract, the `Binder` method
  set with the reason for each method, the registry, how to add a builtin, the
  node protocol duties, what stays in the engine, and `## Testing` selectors
  moved from `internal/engine/README.md`.
- Design notes that name moved glue files or the generic contract:
  `PG18-ARCH-EXPRESSION-ANALYSIS.md`, `PG18-ARCH-ROUTINE-EXECUTION.md`,
  `PG18-FUNC-*.md`. Grep for more.
- `AGENTS.md`: once F3 lands, the shared subpackage bullet may say in a few
  words that new builtins go into the `functions` registry, pointing to its
  README. Remove the text this replaces, so the file does not grow.

## Done when

No generic instantiations remain. `function.go` dispatch is a registry lookup.
The central switches contain no function-node cases. The Tier A and Tier B
glue files are gone from the engine, and aggregates follow the split above.
Tier C has a refined plan.

## Status

- [ ] F1 — Generics removed
- [ ] F2 — Switches converted: [ ] retag [ ] remap [ ] volatility
  [ ] deparse [ ] node tree [ ] generated admission [ ] conditional plan
  [ ] join legality
- [ ] F3 — `Binder`, `Definition`/`Lookup` and engine dispatch; pilot family:
  (name)
- [ ] F4/F5 Tier A: [ ] math [ ] string [ ] LIKE/SIMILAR/regex [ ] datetime
  [ ] JSON/JSONB/SQL-JSON [ ] arrays [ ] ranges [ ] UUID [ ] format
  [ ] binary [ ] numeric formatting [ ] pgcrypto [ ] fuzzystrmatch
  [ ] unaccent [ ] intarray [ ] intagg [ ] uuid-ossp
- [ ] F4/F5 Tier B: [ ] random [ ] current time and clocks [ ] sleep
  [ ] settings reads
- [ ] F4/F5 aggregates and windows
- [ ] Tier C refined (after steps 4, 5 and 7)

`Binder` methods added beyond the F3 set: none yet.

Outstanding aliases: none yet.

Notes for later steps: none yet.
