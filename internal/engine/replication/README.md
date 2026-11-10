# internal/engine/replication

Pure shared replication argument validation. `ValidateSlotName` owns byte/name
diagnostics; callers own SQL type conversion. `PluginAllowed` validates the whole
exact configured list before accepting membership, without identifier folding.

The package owns no catalog, session, lock, provider, registry or stream. Core
owns live authorization/settings/slots; `logical` owns change events and plugins.

## Testing

Pure syntax/name units live here. Public replication SQL belongs to the owning
reference packages; unavailable runtime facilities are not simulated by fixtures.
