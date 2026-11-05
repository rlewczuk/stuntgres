# reference/dblink

Public acceptance for installed dblink INSERT/UPDATE SQL construction, local
primary-key metadata and genuine host-bound connection lifecycle.
Scenarios use only public SQL, driver/native-control APIs and `reference/fixture`.
Constructor/metadata scenarios open no network connections; connection physical
phases use genuine external PostgreSQL and explicitly require `REFERENCE=1`.
Incumbent DELETE scenarios remain in `reference/extensions`.

The shared family helper owns schema/extension cleanup and pinned sessions.
Expected SQL and logical diagnostics are independently specified from released
PostgreSQL 18 sources. Each scenario is shared by native and pinned PostgreSQL
backends, with direct, fresh prepared and retained statement routes as applicable.
Generated-SQL smoke executes the actual returned TEXT and checks the complete
ordered destination contents, including unchanged source and decoy rows.

Source scenarios distinguish real type conversion, view/inheritance/RLS execution,
private writes and full-result errors from catalog-only construction. Source
presentation scenarios assert escaped setting effects, pre-source relation
spelling and post-source column/date formatting with independently reset direct
and repeated held calls. UPDATE keys come from the target array in both SET and
WHERE; source keys only select the copied local row. Snapshot
scenarios cover lexical volatile source execution within a stable caller, restored
outer visibility, and deterministic peer-commit admission at each isolation level.
Cancellation uses observed relation/sleep waits, joined workers, exact server
cancellation diagnostics and independent post-command resource ownership.
Statement atomicity witnesses roll back enclosing marker/source effects rather
than deleting them manually.

Primary-key scenarios assert constraint order after dropped/renamed attributes,
typed strict empties, caller-path/TEMP resolution, table versus column SELECT,
released kind precedence and nominal composite/array/backing identities. Retained
definitions use current names and privileges; native replacement checks detached
Rows, owned nominal values and symbolic member/type replay without live handles.

Native-only phases inside shared scenarios cover Reset/Dump/Restore, independent
Driver destinations, genuine INSERT, UPDATE and DELETE membership, retained statement and
result lifetimes, preserved session TEMP/settings, and rejected/canceled replay.
These phases do not replace SQL assertions against real PostgreSQL.

Connection scenarios cover the seven scoped declarations, strict NULL and
current ACL admission, literal/clipped names, conninfo/security diagnostics,
actual separate remote sessions, default replacement, disconnect, abandoned and
pending resource cleanup. Constant-NULL STRICT calls can be eliminated before
EXECUTE admission, including VOLATILE calls; separate nonNULL and NULL-column
calls witness genuine runtime permission checks for both untrusted overloads.
Rename/move invalidation can re-resolve a held statement's original SQL to an
ordinary shadow. Stored view dependencies and separately prepared current-name
calls witness authentic routine identity and current ACL/schema admission.
URI query spaces use `%20`, not form-encoded `+`: libpq treats `+` literally.
Port-boundary scenarios independently require ports 1 through 65535 after named
duplicate admission. Zero, 65536 and -1 have exact `08001` parser Detail, leave
existing named/default PID and TEMP state unchanged, never enter provider Open,
and release a fresh failed reservation for genuine valid reuse. Restricted
original-secret checks still precede duplicate/range admission. Expectations
follow [REL_18_6 port bounds](https://github.com/postgres/postgres/blob/REL_18_6/src/interfaces/libpq/fe-connect.c#L3036-L3047),
not host-library parser messages; pure units also cover valid endpoints.
The common fixture's single real pgconn adapter is
shared with table-provider tests; no duplicate backend implementation exists.
Actual retained provider Execute errors plus independent PID exit witness close.
Oracle-only dblink query helpers observe PG remote state; native connection
lifecycle does not install the adjacent SQL query/async/cursor families.

These distinctions follow PostgreSQL 18.6's
[`evaluate_function` constant-NULL simplification](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/optimizer/util/clauses.c),
[`ExecInitFunc` runtime admission](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/executor/execExpr.c)
and [`conninfo_uri_decode` percent decoding](https://github.com/postgres/postgres/blob/REL_18_6/src/interfaces/libpq/fe-connect.c).
Unexpected SQL errors expose complete logical diagnostics through the shared
fixture; successful calls stay quiet and expected errors retain exact fields.

Direct conninfo explicitly selects a registered host provider through
`BindDblinkExternalSQLProvider`; every request distinguishes present text
(including empty) from structured options. Native controls require canonical
bound or explicitly unbound replay prerequisites, preserve persistent identities
and join detached published/pending generations outside all locks. Failed
controls preserve live state; postpublication cleanup errors do not undo catalog
publication. These are native host/control phases, not PostgreSQL SQL APIs.
Alias witnesses require genuine registered handler authority and effective-role
mapping before PUBLIC/USAGE, not SQL spelling or direct-binding fallback.
Settings-restoration witnesses use the existing settings metadata contract:
native current_setting TEXT is known nullable, while PostgreSQL reports unknown;
dblink's computed TEXT remains unknown. Native other-database isolation witnesses
use unique registrations and close every connection/pool; the Driver owns each
registration's lifetime because native DROP DATABASE is not implemented.

The compact connection smoke opens/lists/duplicates/replaces/disconnects real
sessions, retains remote transaction state, checks backend exit and current
untrusted admission, uses actual trust-vs-password authentication, and exercises
native persistent markers and resource-free replay. It is not a suite alias or
an all-skipped/package-startup smoke.


## Testing

Family selector: `make check RUN='^TestReferenceDblink(BuildSQL(Insert|Update)|GetPkey)'`, also with
`REFERENCE=1`. Follow `docs/TESTING.md` for selected test-first oracle discovery,
complete diagnostic assertions and dual-mode verification. This package's
`TestMain` uses the common `fixture.Main` without changing oracle configuration.

Connection family selector:
`make check RUN='^TestReferenceDblinkConnection(Signatures|Nulls|Names|Lifecycle|Conninfo|PortBounds|Security|Controls|Cleanup|FeatureSmoke)$'`,
also with `REFERENCE=1`. Follow `docs/TESTING.md` for feature smoke and real/race
proof. Expectations are independently specified, never learned from native output.
