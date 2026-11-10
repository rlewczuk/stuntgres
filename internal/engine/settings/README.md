# internal/engine/settings

Setting definitions and pure behavior: immutable registration, typed parsing,
normalization, formatting and metadata. Session values, callbacks, journals and
execution belong to session/core, never this package.

## What it owns

`Definition` describes type, access/context, units, category, boot/reset metadata
and normalization. `Registry` validates names/definitions and preserves canonical
order. `Value` retains typed input and presentation facts; shared numeric/unit,
identifier-list and search-path helpers avoid parallel grammars in consumers.

Parsed settings and operative capabilities are distinct. Some definitions model
fixed native configuration or validated setting-only simulations; a stored value
never proves that a physical facility, worker, loader, file or transport exists.
Actual consumers own admission and unsupported-operation behavior.

Access context and read authority are independent. Live assignment/reset checks
precede value parsing where required, including equal/default values. Normalizers
return notices for the caller to emit; they do not call providers or read sessions.
Boot metadata, native startup values, reset sources and current formatted output
are separate fields, not interchangeable strings.

## Invariants

- Build a Registry once, then treat it as immutable. Core stores it by value and
  providers borrow its address. Definitions, order, enum lists and Value path
  backing are read-only borrows; reads require no mutex.
- No locks, mutable runtime state, filesystem/network access or callbacks here.
  Definition lookup cannot initialize a module or grant execution permission.
- Builtin order, metadata, normalization/error precedence, SQLSTATEs, hints and
  unit conversions are compatibility contracts.
- Typed values and original accepted presentation remain distinct where required;
  consumers must not reconstruct settings semantics from SHOW text.

## What stays in the engine

SET/RESET/SHOW, current_setting/set_config, RESET ALL, provider defaults and
current read/write authority belong to core/session. The session action stack
owns LOCAL/SAVE/default/reset/savepoint semantics and physical initialization.
Parameter ACLs use the same immutable registry for canonical names, not a second
setting/permission store. Routine environments restore effective values on every
exit while preserving genuine physical-session initialization.

## Testing

Pure registry/parser/normalizer/format/metadata units live here. Session tests
cover typed action stacks and immutable images; core tests cover current authority
and actual consumers. Public registered/custom/SHOW behavior belongs to
`reference/session_state` under [docs/TESTING.md](../../../docs/TESTING.md).
