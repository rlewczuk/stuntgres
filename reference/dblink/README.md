# reference/dblink

Public acceptance for installed dblink INSERT and UPDATE SQL construction. Scenarios use
only public SQL, driver/native-control APIs and `reference/fixture`; no network
connection is opened by the constructor. Incumbent DELETE scenarios remain in
`reference/extensions`.

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

Native-only phases inside shared scenarios cover Reset/Dump/Restore, independent
Driver destinations, genuine INSERT, UPDATE and DELETE membership, retained statement and
result lifetimes, preserved session TEMP/settings, and rejected/canceled replay.
These phases do not replace SQL assertions against real PostgreSQL.

## Testing

Family selector: `make check RUN='^TestReferenceDblinkBuildSQL(Insert|Update)'`, also with
`REFERENCE=1`. Follow `docs/TESTING.md` for selected test-first oracle discovery,
complete diagnostic assertions and dual-mode verification. This package's
`TestMain` uses the common `fixture.Main` without changing oracle configuration.
