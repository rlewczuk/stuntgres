# Engine split, step 16: `internal/engine/extension`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-extension.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 16; §4.1 (two parts,
  measured clean sets).
- **Needs:** Part A: 15b (`typedef`), because `intarray` validation goes
  through type validation. Part B: 13d. Read the 5c record in
  [`ENGINE-SPLIT-catalog.md`](ENGINE-SPLIT-catalog.md) (the
  `extensionImplementation` seam) and the pgcrypto/fuzzystrmatch/unaccent
  records in [`ENGINE-SPLIT-functions.md`](ENGINE-SPLIT-functions.md) (the
  kernels already moved).
- **Effort:** high for 16a and 16b; xhigh for 16c; high for 16d.
- **Session size:** one sub-step per session.

## Goal

The installed-extension layer (provider records, control data, member
validation, ownership metadata and remap) becomes a feature package. The
function kernels are already in `functions`; installation stays in the engine
until Part B.

## Inventory (measured at `75fe0d710`)

16 files, about 2.1k lines; 873 of 1,925 declaration lines are clean.
Feature work adds extensions often (`PG18-EXT-*` parts): re-measure first.

**Clean declarations:**

- providers: `fuzzystrmatchProvider` (`Control`, `ValidateExtension`,
  `validateProcedure`), `fuzzystrmatchMembers`, `fuzzystrmatchResult`,
  `stockFuzzystrmatch`; `intaggProvider` (`Control`, `ValidateExtension`,
  `validateProcedure`), `intaggMembers`, `stockIntagg`; `intarrayProvider`
  (`Control`), `intarrayInputContext`, `intarrayTypeContext`,
  `queryIntSignature`, `stockIntarray`; `pgcryptoProvider` (`Control`,
  `ValidateExtension`, `validateProcedure`), `stockPgcrypto`;
  `unaccentProvider` (`Control`, `ValidateExtension`), `stockUnaccent`;
  `uuidOsspProvider` (`Control`, `ValidateExtension`,
  `validateProcedure`), `stockUUIDOssp`;
- `extension_implementation.go`: the `*ProviderOf` accessors (all clean);
- `extension_catalog.go`: `extensionCatalogProvider` (type and `ClassID`),
  `extensionMembership`, `namespaceChildren`, `procedureInNamespaces`
  (`procedureArity` is `pgcatalog.ProcedureArity` since 9b);
- `extension_owners.go` (203 of 306 lines): `decodeExtensionOwners`,
  `extensionMemberOwner`, `extensionOwnerFields`, `extensionOwnerMetadata`,
  `extensionOwnerRecord`, `extensionOwnersMarker`;
- `extension_remap.go`: `remapExtensionDescriptor` (all clean);
- `extension_drop.go`: `extensionRemovalRoots`; `extension_install.go`:
  `extensionAuthorityObservation`, `extensionMemberConflict`,
  `extensionNameConflict`, `unaccentTemplateContext`;
- `intagg_expression.go`: `sameIntaggIdentity`, `validateIntaggIdentity`;
  `intagg_presentation.go`: `intaggRecipeFunction`;
- `pgcrypto_expression.go`: `closedPgcryptoDefault`,
  `cryptoExpressionSignature`, `installedCryptoFunctionExpression`;
  `uuid_ossp_expression.go`: `installedUUIDOsspFunctionExpression` (all
  clean).

**Dirty, and why:**

- every provider's `stage` method (`CatalogStage`, `Engine`), the
  `extensionInstaller` interface, `Engine.executeExtension`,
  `stageExtensionLocked`, `registerExtension`, `executeDropExtension`,
  `importExtensionOwners`: Part B;
- `extensionCatalogProvider.Validate` and `intarrayProvider.
  ValidateExtension`/`validateProcedure`: through `typeProvider.Validate`
  (stored-SQL rendering), see step 15;
- the intagg binder methods: step 12; `EvalContext.FunctionPgcrypto*`:
  step 11; `installedFunctionExpression.Eval`: step 10.

**Not extension code:** `extension_database.go` is database GRANT and
`initializeDatabase`; leave it, and rename it in 16a (for example
`database_grant.go`).

## Part A

### 16a — In place

1. Re-measure; update the inventory.
2. Split each provider file into its records (clean) and its `stage` method
   (Part B), so the clean halves move as files. Rename
   `extension_database.go`.
3. Give `intarrayProvider.ValidateExtension` and
   `extensionCatalogProvider.Validate` a narrow validation capability for
   the type checks they need, so they become clean (coordinate with step
   15's seam).
4. Decide where the installed expression wrappers
   (`installedCryptoFunctionExpression`, `installedUUIDOsspFunctionExpression`)
   go: they hold catalog identity (see the `expr` README); record the
   decision.

### 16b — Create `extension` and move the clean set

1. Export in place, then move with the `movex` approach.
2. Check imports: `catalog`, `functions` (kernels, if providers name them),
   `expr`, `authorization`, `ids`, `parser`, `sqltype`, `sqlstate` and the
   standard library. Not `typedef` unless 16a recorded it.
3. The catalog's `extensionImplementation` field and `catalog.Owner.
   Extensions` keep holding the implementation interface; only the concrete
   providers move.

## Part B

### 16c — Refine the host (docs only), after 13d

Measure `executeExtension`, `stageExtensionLocked`, the provider `stage`
methods, `executeDropExtension` and `importExtensionOwners`; write the host
interface and move set; wait for approval.

### 16d — Move the installation handlers

Follow 16c. Remove the remaining aliases.

## Invariants

- Installed members, their OIDs, namespaces, owners and dependency records
  stay identical; CREATE/DROP EXTENSION messages and SQLSTATEs unchanged.
- Extension remap and owner metadata in native Dump stay byte-identical.
- Provider registration order on engine startup is unchanged.

## Targeted tests

- Engine units: grep `*_test.go` for each provider type, `extensionOwners`,
  `remapExtensionDescriptor`.
- Reference selections (quick mode and `REFERENCE=1`; grep `reference/`):
  CREATE/DROP EXTENSION, each extension's functions (fuzzystrmatch, intagg,
  intarray, pgcrypto, unaccent, uuid-ossp), `pg_extension`,
  `pg_available_extensions`, and native Dump/Restore with extensions.
- `make bench` after 16b (provider registration runs at startup).

## Documentation

- Create `internal/engine/extension/README.md` (16b).
- The `functions` README names provider files; update it.
- `AGENTS.md`: add `extension` to the shared subpackage bullet.

## Done when

Part A: provider records, validation, owner metadata and remap live in
`extension`. Part B: CREATE/DROP EXTENSION handlers live there too. The
checks in §8.3 pass.

## Status

- [ ] 16a — In place
- [ ] 16b — `extension` created and clean set moved
- [ ] 16c — Host refined (docs only; after 13d)
- [ ] 16d — Installation handlers moved

Outstanding aliases: none yet.
