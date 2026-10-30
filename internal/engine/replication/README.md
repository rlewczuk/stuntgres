# internal/engine/replication

Pure argument validation shared by subscription metadata and replication-slot
SQL runtime. `ValidateSlotName` validates bytes and owns released diagnostics;
callers own input conversion (NAME clips before lifecycle validation, subscription
text does not). `PluginAllowed` scans an entire exact SplitGUCList spelling,
without identifier folding, clipping or accepting a match before malformed syntax.
Only an initial double quote starts quoted-element grammar; embedded quotes in
unquoted elements are literal bytes.

This package owns no catalog, session, locks, provider, slot registry, WAL source
or plugin loader. Core owns live authorization/settings and the explicit embedded
physical-mechanism boundary; no successful lifecycle effects are simulated.

## Testing

`make check RUN='^(TestReplicationPluginAllowlist|TestSubscriptionSlotValidation)$'`
covers pure syntax and shared name validation. Public SQL lives in
`reference/roles` under `TestReferenceReplicationSlot` and runs in both modes.
