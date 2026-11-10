# internal/engine/core/stats

Statistics imports, catalog visitors, expression recipes and snapshot codecs
below core. Core supplies statement and bootstrap adapters; this leaf never
imports core. Collected facts are distinct from cumulative monitoring.

## Contract

- `ImportRelation` and `ImportAttribute` are the bodies of the restore and
  clear functions: argument decoding with WARNING notices in released
  validation order, relation resolution and maintenance authorization, the
  relation lock, then a fenced record into the statement's private catalog
  journal. They return whether every argument was applied.
- `VisitAttributes` and `VisitExtended` project the resolved statistics facts
  of every visible relation into `pg_stats` and `pg_statistic_ext_data` rows.
- `VisitExtendedView` traverses current definitions for the two extended views.
  It attaches compatible generation-qualified local/inherited facts through the
  same resolver, retaining unbuilt expression identities with NULL summaries.
- `BindExpression` binds one statistics expression with the index expression
  binder that core builds over the parent, preflights its coercions and
  returns the retained `catalog.StatisticsExpression`; `SameExpression`
  compares two definitions.
- `SnapshotCodec` (`NewSnapshotCodec`) implements
  `storage.StatisticsSnapshotCodec`: deparsed expressions, encoded typed
  replay nodes and their checked canonical decoding.

## Host and authority

- `Host[A]` is the statement an import runs in; core implements it on
  `statementHost` (the shared leaf method set). It gives the operation
  (notices), the execution context, the input environment, the Engine.Mu
  pair, the pending catalog journal, the engine store, the routine
  authorization, relation resolution, the VACUUM maintenance check, the
  relation lock, the published relation and the statement's private catalog
  journal.
- `Authority` is that authorization, held by value (`Access`). The imports
  are generic over it, as `tsearch` is, so capturing it never allocates;
  `CatalogAuthority` adds the pending journal for the visitors. Core's
  `AuthorizationView` implements both.
- `RecipeAuthority` (routine admission and the view) and `SnapshotAuthority`
  (also the binder authority) are implemented by `*AuthorizationView`.
- `SnapshotHost` is a codec's captured bootstrap authority: a fresh copy of
  the authorization per decode, and the snapshot binding host for constructor
  scalars.

## Locks

- Imports are entered with no engine lock held. `Host.ReacquireEngine` takes
  `Engine.Mu` and `Host.ReleaseEngineLocked` releases it; the host methods
  named …Locked run inside those windows. Argument decoding, value parsing,
  notices and the relation lock (`AcquireObjectLock`) run with nothing held,
  and every window re-fences the catalog generation and authorization.
- The visitors run under the catalog runtime's capture lock; the codec and
  `BindExpression` run under the caller's lock and hold none of their own.

## Testing

Engine-host tests live in core and `core/enginetests/pgcatalog`. Public
statistics and native-control SQL belongs in reference packages.
