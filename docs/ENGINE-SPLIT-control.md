# Engine split: `internal/engine/control`, sixth round

Session prompt for linear item 173 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the control README](../internal/engine/control/README.md).

`control` already owns the comparison-context comment codec
(`control.DecodeComparisonContext`, `control.InvalidComparisonContext`) and
the Restore hook table type. The state machine that pairs those comments with
`SET`/`RESET search_path` statements and checks the bound definitions is
still in the root. It reaches the root only through `restoreRun` (the run
state, `r.stage.Execute` and `r.stage.CatalogRead`) and through
`Engine.authorizationLocked`. Sizes were measured at `ea8c445fe`; re-run
`reach` with `-files '^restore_comparison_'` first.

## Item 173 — Restore comparison context

Needs —. About 0.29k lines move.

**Moves.**
- From `restore_comparison_context.go`: `comparisonMarker`,
  `comparisonRestore` and `comparisonDDLPosition`. Move also the bodies of the
  five hook functions (`decode…`, `pair…`, `before…`, `after…` and
  `finishRestoreComparisonContext`) as methods of `control.ComparisonRestore`:
  - `Decode(comment string, position parser.Position) (bool, error)`;
  - `Pair(source string, statements []parser.Statement) error`;
  - `Before(index int, stmt parser.Statement, current catalog.Read) (execute bool, err error)`.
    `execute` tells the root to run the marked statement itself.
  - `After() (check ComparisonCheck, ok bool)` and `Bound()`, around the
    root's binding check;
  - `Finish() error`.
- From `restore_comparison_receipt.go`: everything after the authorization
  capture of `Engine.validateComparisonBindingStatement`. That is the
  statement-kind switch, the namespace check and the
  `stored.VisitDefinitionExpressions` walk. It becomes
  `control.ValidateComparisonBinding(current, before catalog.Read, stmt parser.Statement, namespace ids.ObjectAddress, position int) error`.

**Cut.**
- **Slot.** `restoreRun.comparison` becomes a `control.ComparisonRestore`
  value (§2.2 rule 2).
- **Value inputs.** `r.stage.CatalogRead()`, `r.source`, `r.statements`
  and `r.cur.index` are passed as values (§2.2 rule 6).
- **Imports.** `control` already imports `extension`, `stored` and
  `catalog`, so the cube installation check (`extension.StockCube`) moves
  as it is.

**Stays root.**
- The five one-line hook functions in `restore_hooks.go`'s table. They
  forward to the slot's methods.
- `r.stage.Execute` for marked statements.
- The `Engine.mu` window that captures `authorizationLocked`, and the USAGE
  check on the bound namespace. These are about 10 lines left in
  `validateComparisonBindingStatement`.

**Callers and locks.** The state machine holds nothing. Only the root
authorization capture takes `Engine.mu`. The order of the hooks in the
table, and therefore the error precedence against the other Restore hooks,
stays the same.

**Tests.**
- No root unit names the moved code. Add `control` units for the state
  transitions:
  - begin, bind, close and finish;
  - unpaired markers;
  - `SET search_path` outside a window;
  - a second DDL in one window.
- Quick and `REFERENCE=1` selection:
  `TestReferenceCubeComparison(BindingPath|InheritedBindings|WrittenDefaults|Controls)`
  and `TestReferenceUserOperatorComparisonConsumerStoredConstructors`.

**Docs.** Add the comparison-context Restore state machine to the control
README.

## Status

- 173: not started.
