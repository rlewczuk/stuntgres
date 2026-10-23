# Engine split: `internal/engine/catalog`, fifth round

Session prompt for linear item 153 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the catalog README](../internal/engine/catalog/README.md). Sizes were
measured at `fe0683b68`; re-run `reach` on the move set first.

## Item 153 — Candidate publication helpers

Needs —. About 0.38k lines move, and the root loses about 40 more.

**Moves.** The receiver becomes `*catalog.Candidate`. `CatalogStage` embeds
it, so the calls stay promoted and no wrappers are needed.
- From `publishCatalog` (`catalog.go`):
  - the domain-base fence (about L532-549, including the terminal TEMP
    cleanup scan);
  - the descriptor, namespace, removal and touched-name fences (about
    L560-625), without the extension-materialized-view check (L588-598);
  - the descriptor part of the apply step (about L690-734).
- The stage index and subclass history markers
  (`markStageIndexHistoryLocked`, `markStageSubclassHistoryLocked`).
- Five `stageDefinitionReader` methods.
- The sort in `catalogStageChanges` (`catalog_changes.go`) and
  `accessMethodStageCurrent` (`access_method.go`).
- `CatalogStage.creationACL`, `creationTypeACL`, `defaultPreflight` and
  `removeSchemaDefaults` (`default_privileges_catalog.go`).

**Cut.**
- The value inputs are the current `*catalog.State`, the domain, the
  stage's `domainBases` and its `terminalCleanup` flag.
- The history markers get two callbacks, `revision()` and `replace()`, because
  `CatalogStage.Replace` also synchronizes the row type.
- `stageDefinitionReader.IndexLayout` stays a root wrapper, because the stage
  keeps its own index state.

**Callers and locks.**
- The fences, the history markers and the ACL helpers run under `Engine.mu`
  and become `…Locked` or are documented as running under it.
- The apply step and the reader run with nothing held, as today.

**Stays root.**
- the extension-materialized-view check, run right after the `catalog` fence
  call (`catalog` cannot import `view`);
- the post-apply validators (L809-855), which are a composition point like
  `catalogDependencyPreflight`;
- the relations map, so `catalog` needs no `storage` import;
- publication itself.

**Root dedupe.** `replaceCatalog` repeats code:
- its install tail matches `installForeignCatalogLocked`;
- its capacity checks appear twice;
- its ACL loop appears twice.

Share one helper for each.

**Risks.** Every fence returns `catalog.Conflict{}` and has no side effects,
so splitting off the extension-MV check cannot change which error wins. Keep
the fence order. DDL only: no hot path.

**Tests.**
- Root units: `access_method_test.go`, `default_privileges_helpers_test.go`,
  and grep `*_test.go` for every moved name; tests of pure helpers move to
  `catalog`.
- Quick and `REFERENCE=1` selections: `TestReferenceAccessMethod`,
  `TestReferenceRoleDefaultPrivileges`, `TestReferencePGDepend`,
  `TestReferenceDatabaseCreate` and one concurrent DDL conflict selection
  (`TestReferenceForeignActionsConcurrentDDL`).

**Docs.** The README's Candidate paragraph says reads "never merge, clone,
scan the base or return writable maps". Keep that for reads, and document
the moved functions separately: they are publication and validation helpers
that the root calls under `Engine.mu`, and they scan the current state.

## Status

- 153: not started.
