# internal/engine/remote

Host-supplied external SQL connections for genuine physical sessions. It imports
lower catalog/execution/session contracts, never core, the driver or a network
library. Public standard-value records live in catalog; gateways own registration
and current FDW/caller authority.

`Owner` attaches before Open and tracks connection/pending membership. Open
requires an authentic provider token and exact live operation callback context;
a stale, sibling or canceled operation cannot publish. Every nonnil provider
return transfers cleanup responsibility, even alongside an error. Publication
validates under Session.Mu without application callbacks beneath it.

Owner.mu protects membership/generations; session locking precedes it. A
connection's command exclusion serializes commands and close, while its leaf
mutex protects cancellation/closing state, never I/O. Close cancels active work,
joins command/cancel completion, then closes/removes once. Explicit cancellation
does not fabricate context cancellation or SQLSTATE.

Already-owned commands may outlive a statement, never the physical session.
Terminal cleanup uses exact-session authority, closes/joins handles before End
completion and remains idempotent through later participant release. Results
carry owned text descriptors/cells; ordinary codecs own SQL conversion. No
handle, queue or context is catalog/Dump state.

Dblink and postgres_fdw reuse this Owner, not parallel resource registries.
Raw dblink links do not enlist in FDW transaction coordination. Reusable mapping
keys retain actual database/provider/mapping identity, not names or caller OIDs.
Core admits each caller, including reuse, and fences current metadata after
unlocked transport. Pending Open publication checks the Owner generation.

Invalidation detaches reusable entries and marks active ones for retirement.
Replacement selects the authoritative connection membership, including detached
active links, and cancels/joins only after all Engine/Session/Owner/replacement
locks release. Failed prepublication controls preserve resources; another
database's links retain their own lifetime.

Connection-string parsing is syntax/credential precheck only: it never consults
network, files or environment. Host-reported actual authentication facts remain
independent of supplied password presence. Network/TLS/authentication belongs to
the application's transport, not this standard-value runtime.

## Testing

Pure registration/result/parser units exercise local contracts. Real transport,
authentication and composed ownership scenarios live in `reference/fixture` and
`reference/tables` with the pinned oracle; quick mode does not substitute a fake
remote source. Core tests cover publication and physical-session cleanup.
