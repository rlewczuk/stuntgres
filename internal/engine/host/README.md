# internal/engine/host

Immutable application-provisioned scalar routine contracts and executable tokens.
This leaf owns no catalog, binder, session, transaction or mutable name fallback.

`New` validates shape without calling application code and copies declaration
slices. Program/runtime/conversion tokens are immutable. Program declarations use
logical types; catalog owners resolve genuine directional conversions from SQL
associations or registered language defaults.

`Program.Invoke` checks runtime/type/direction for captured tokens. Its caller
releases all engine/session/settings locks and semantic catalog reservations,
and performs balanced current-authority readmission around each individual
converter, handler and nested callback. Callbacks receive no SQL frame or grant.

Invocation clears transient slots on every exit, including panic. A capability
retained by application code loses context, admission hook and program references
at completion and returns an ended-invocation error afterwards. Calls are
synchronous; application resources remain the application's responsibility.

Manifests/prerequisites contain logical provider/ABI, version, signature and
directional identities, never closures, pointers or source catalog IDs.
Applications advance versions for incompatible executable or object-ABI changes.
Catalog/bootstrap/retirement and destination remapping stay with catalog/core/
control, which authenticate actual destination tokens instead of replaying code.

## Testing

Local `enginetests` check token/declaration backing and callback continuation/
unwind ownership. Configured public behavior and native ABI controls live in
`reference/operators` through the ordinary backend loop.
