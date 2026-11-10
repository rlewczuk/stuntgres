# internal/engine/fdw

Foreign-data registration, options/codecs, DDL decisions, immutable request
metadata, modification protocol and transaction participants below core. The
package does not own network transport, engine publication or session cleanup.

## What it owns

Public wrapper/validator definitions and the immutable installed Registry;
ordered option transforms; row-identity membership; modification and participant
state; DDL/import decisions; logical dump prerequisites. Public optional scan,
planning, rowmark, sampling and explain records live in `catalog`.

Metadata selection and handler invocation are distinct. The selected genuine
procedure/ref authorizes only its declared capability. Actual provider demand
borrows current live host authority; it is not initialized by Prepare or an
undemanded capture. Names and handler metadata alone grant no invocation.

## Foreign row marks and refetch

Identity and rowmark requests retain source-owned immutable identity separately
from SQL strength/wait policy and local projected slots. Refetch uses the actual
enlisted transaction and full declared tuple; the executor owns import, latest-
row rechecks and predicate/RHS semantics. Provider participation lasts through
SQL transaction settlement, not just result/scan close.

Execution requests capture the SQL owner's resolved transaction characteristics.
They are invocation values, not provider/session configuration handles. Foreign
participation explicitly refuses unsupported prepared-transaction semantics
rather than synthesizing a remote prepare/commit guarantee.

## Invariants

- No package-owned mutex. `Engine.Mu` guards Registry installation; admission and
  owner locking serialize a Transaction. Application validators, modifications,
  participant callbacks and rollback run outside Engine/replacement locks.
- Handler admission checks current typed relation-kind restrictions before
  invoking a provider. Metadata-only lookup/DDL is a separate capability.
- Requests, cached source methods and private plans retain no session, operation,
  settings store or invocation grant.
- Commit each open participant once. Failed Commit has unknown outcome: never
  retry it; roll back remaining participants. Rollback survives cancellation.
- Published registration is immutable. Restore replaces namespace mappings whole
  in the same publication as catalog roots.
- Messages, SQLSTATEs, details/hints and callback error identity are contracts.

## What stays in the engine

`schemacmds` owns DDL locks/stages and unlocked provider validation. Core owns
registration adapters, scan capture, current authority, enlistment, savepoint/
transaction coordination and physical resource cleanup. DML owns foreign writes;
`remote` owns authentic physical-session external SQL handles.

## Testing

Pure definition, options, identity and protocol units live here. Composed core/
family tests cover real participants and publication. Public foreign SQL and
real transport behavior live in `reference/tables`; an unavailable remote
facility is not replaced with a canned or embedded result.
