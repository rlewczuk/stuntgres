# Engine split: `internal/engine/exec`, sixth round

Session prompt for linear item 174 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the exec README](../internal/engine/exec/README.md).

The PL/pgSQL query loops and the FTS `ts_rewrite(query, text)` path added
row-owning wrappers to the root. Each wraps `exec` stages, sources and CTE
invocations. None of them needs `StatementScope`, except one read of the
scope's I/O environments. Sizes were measured at `ea8c445fe`; re-run `reach`
with `-files '^(nested_query_rows|cte_owned_rows|fts_query_batch|fts_query_runtime)\.go$'`
first.

## Item 174 — row owners

Needs —. About 0.17k lines move.

**Moves.**
- `nestedQueryRows` with its methods, and `openNestedQueryRows`
  (`nested_query_rows.go`), as `exec.OpenNestedQueryRows`. This is the
  private all-row producer that PL/pgSQL query loops borrow
  (`execute_bound.go`).
- `cteOwnedRows` (`cte_owned_rows.go`), as `exec.CTEOwnedRows`, built in
  `cte_mutation_execute.go`.
- `fetchRewriteBatch`, `consumeRewriteRules` and `rewriteFetchSize`
  (`fts_query_batch.go`). They read an `exec.RowCursor` into
  `sqltype.FTSRewriteRules`. `fts` cannot own them, because it imports only
  `sqlstate`.
- `rewriteSQLDiagnostic` (`fts_query_runtime.go`), the SPI prepare/open
  error mapping.

**Cut.** `openNestedQueryRows` reads the environments through
`scopeOf(source.LiveHost())`. Replace that with the existing
`exec.Host.Environments()` on `source.LiveHost()`, keeping the nil guard
(§2.2 rule 7, no new method). Everything else is clean today.

**Stays root.**
- `rewriteQuerySQL`, `prepareRewriteStatement` and
  `statementHost.RewriteQuerySQL` (routine depth, binder, dispatch
  re-entry);
- `rewriteTupleStatement` and `rewriteReadOnlyViolation`, which use the
  root's `routineCommandTag`;
- the call sites in `execute_bound.go` and `cte_mutation_execute.go`.

**Callers and locks.** Nothing is held. The close order must stay the
same, because the main stage closes before its CTE chain and captured
sources. FOUND publication after producer cleanup depends on it.

**Bench.** This is a per-row path. Run `make bench`, and compare
`-gcflags=-m` heap diagnostics for the moved constructors against a master
snapshot. Allow no new allocation per row or per loop.

**Tests.**
- Root units that construct the moved types are rewritten to the `exec`
  names or moved to `exec`:
  - `plpgsql_query_loop_test.go`, `plpgsql_query_loop_failure_test.go` and
    `plpgsql_query_loop_cte_test.go`;
  - `fts_query_batch_test.go` and `fts_query_runtime_test.go`.

  Item 170 also edits the two query-loop files. Whichever item merges second
  rebases the other's renames.
- Quick and `REFERENCE=1` selection:
  `TestReferencePLpgSQLQueryLoops(Sources|Lifecycle|Empty)`,
  `TestReferenceFTSQueryFunctions(SQLRules|SQLDemand|Cancellation|Diagnostics)`.
  Use `RACE=1` for the cancellation selection.

**Docs.** Add the nested producer and the CTE row owner to the exec README's
row-result contracts. Move the "Static query loops borrow a private all-row
producer" paragraph from the root README.

## Status

- 174: not started.
