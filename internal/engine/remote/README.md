# internal/engine/remote

Owns host-supplied external SQL connections for a genuine physical session. It
imports `catalog`, `exec`, `ids` and `session`, never core, the root driver or a
network library. Public standard-value contracts remain in catalog; registration
and FDW authority capture belong to the driver/fdw/core gateways.

`Owner` is attached before Open, below the existing transaction/cursor cleanup
order. Each opener captures an authentic immutable provider token, exact live
operation and owned caller/mapping metadata. Open requires that operation's
private callback context (or a derivative). A sibling callback context, stale
operation, canceled Open or missing capability cannot publish a connection.
Nonnil provider returns transfer cleanup even with an error. Publication validates
under Session.Mu; no provider callback runs under it.

Owner.mu protects membership, dblink indices and generation only; the session lock precedes it at publication.
Connection.command serializes commands, notifications and physical close.
Connection.mu protects active cancellation and closing state, never application
I/O. Cancel runs independently of command exclusion. Close first cancels the
active command context and dispatches best-effort physical cancellation, then
joins command/cancel completion before once-only physical close/removal. An
explicit Cancel does not cancel the command context or fabricate SQLSTATE 57014.

Already-owned connection commands can outlive a statement but not their physical
session. Cross-owner tagged contexts are rejected. Joined terminal rollback uses
an engine-private exact-session cleanup context, not a new lease/opener. Session
End closes all remaining handles before WaitEnd completes. No handle, context,
result or notification queue is catalog/dump state. `ValidateResult` checks owned
text descriptors and flat cell width without interpreting PostgreSQL codecs;
ordinary FDW import supplies the existing type conversions. Reserved database/sql
retry sentinels use exec's common sanitization; all other error identity survives.

Dblink uses this same Owner, with a default pointer, case-sensitive clipped named
index and pending child contexts/completion channels. Installed admission in core
registers a pending Open under Engine.Mu before transport; `Publish` then fences
the exact operation and Owner generation under Session.Mu -> Owner.mu. Failed
publication closes nonnil returns before signaling completion. Successful default
replacement preserves the old connection until admission succeeds, then joins its
close. No dblink connection enlists in the FDW transaction coordinator.

`ParseConninfo` performs syntax-only keyword/URI and original-secret admission
without network, environment, service-file or passfile access. Its small owned
value keeps only password presence and port; numeric `Validate` runs after named
duplicate admission for both effective-role branches, without reparsing. Each
explicit nonempty port component must parse as a signed 32-bit integer and lie
within 1 through 65535. Integer syntax/overflow and numeric range have distinct
released diagnostics; the original component is retained in the error. These
deterministic engine checks precede provider Open and do not convert arbitrary
host parser errors into SQL errors.
`ConninfoPresent` distinguishes explicit empty from structured requests. Effective
restricted roles require explicit original password plus actual host-reported
password/md5/SCRAM exchange; unknown/none never proves password use.

`DetachDblink` is lock-only and advances the same Owner's generation, without
ending it or detaching FDW resources. The replacement caller cancels every
detached child and joins completion/physical close after all Engine, Session,
Owner and Replacement locks release. Even cancellation-ignoring late returns
close before completion. Failed prepublication controls never detach.
The genuine shared test transport/auth observer lives in `reference/fixture`;
network/TLS/authentication stays outside this standard-library-only runtime.

## Testing

Pure registration/shape coverage and genuine network ownership scenarios use:

```sh
make check RUN='^TestExternalSQLProvider'
make check REFERENCE=1 RUN='^TestExternalSQLProvider'
```

The network ownership phase lives in `reference/tables` and requires the pinned
real oracle; quick mode does not substitute an embedded or canned remote source.
