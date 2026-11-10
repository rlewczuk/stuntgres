# internal/engine/core/nativetrig

Native DML trigger bodies below core. Provider identity selects admitted
implementation; definitions and invocation orchestration remain with
stored/view/core owners. The leaf may depend on core/tsearch, never core.

## Contract

`Invoke` runs one admitted body inside core's balanced routine/trigger-depth,
effective-user/settings, command-policy and statistics boundary. It validates
provider firing protocol and returns a replacement row plus proceed decision.
OLD/NEW are borrowed for the call only; no activation state enters definitions.

## Host and authority

`Host[A]` supplies current operation, I/O, execution context, captured private
catalog, namespace/routine authority, scoped runtime operations and parameterized
nested SQL. Core implements it on statementHost. The authority is the by-value
`tsearch.Authority[A]`; the generic seam avoids per-row boxing and never
transfers ownership of the enclosing statement.

## Locks

Bodies enter with nothing held. Explicit ReacquireEngine/ReleaseEngineLocked
windows protect metadata capture; Locked host methods run only inside those
windows. Parsing, lexical work, nested SQL, notices and actual runtime calls run
unlocked. Current authority/readmission and the atomic invocation barrier remain
core-owned on every exit.

## Testing

Pure body helper units live here. Composed core tests exercise genuine frames,
provider identity, atomic invocation and cleanup. Public trigger SQL belongs to
reference packages under [docs/TESTING.md](../../../../docs/TESTING.md).
