# internal/engine/host

Immutable application-provisioned scalar routine contracts and executable tokens.
This leaf imports only the standard library and `sqltype` for its plain owned
hstore entry carrier. It owns no catalog, SQL binder, session, transaction or
mutable execution-time name fallback.

`New` validates shape without calling application code and copies all declaration
slices. Runtime/program/conversion tokens are immutable; Program declarations
contain logical input/result types, never conversion selectors. Catalog owners
resolve actual current directional procedures from each SQL routine's explicit
TRANSFORM associations, or its genuine registered language defaults.
`Program.Invoke` receives those captured conversion tokens,
checks their runtime/type/direction and invokes genuine supplied converters,
handler and program. Its caller releases every engine/store/session/settings lock
and semantic catalog reservation. It supplies balanced current-authority
readmission around each individual converter, handler and nested program callback,
not just around their batch. Invocation clears transient object slots on all
unwind paths. The program capability lent to a handler captures a detachable
holder; completion (including panic) clears its context, admission hook and
program references. A retained capability returns an ordinary ended-invocation
error without retaining or entering a SQL scope. Calls are synchronous;
application-owned resources remain the application's responsibility.

`Manifest` and versioned prerequisite comments contain logical provider/ABI,
program/version/signature and directional identities only. They never serialize
closures, pointers, invocation maps or numeric catalog identity. Applications must
advance stable versions when executable semantics or object ABI is incompatible.
The prerequisite format is strictly `stuntgres-host-runtime-v2`; selector-bearing
v1 streams are rejected, not reinterpreted. Direction/pair absence format remains
unchanged because its schema and authority have not changed.
Catalog/bootstrap/retirement and destination remapping belong to the owning
catalog/core/control packages, not to this registry.

Native replay preserves genuine current pair absences at a versioned,
administrator-owned boundary after language replay and before consumers.
The strict canonical envelope names provider/ABI, logical type, actual decoded
SQL type name and language, never source OIDs or callback objects. It cannot
create missing directions or implement SQL CREATE/DROP TRANSFORM definitions.
Alias materialization resolves actual SQL type descriptors through authoritative
installation provenance, independently of current extension membership. This
preserves genuine TO-only pairs while replay temporarily detaches types; it does
not recreate dropped directions or infer capabilities from SQL names.

## Testing

Public behavior uses `reference/operators` and the ordinary configured backend
loop; Go registration and object ABI controls are explicit native phases. Local
`enginetests` supplement it with token/declaration backing ownership assertions;
the owning package also observes detached continuation fields and real normal
and panic unwind: `make check RUN='^TestHost(RegistryOwnership|CallbackContinuationOwnership)$'`
(also with `RACE=1`).
