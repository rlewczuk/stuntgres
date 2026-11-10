# internal/engine/regobj

Catalog identity and typed values as SQL text and back: reg-object input/output,
type declarations/literals, ACL values, address/identity inquiries, prepared
literal capture and destination remapping. It sits above analysis and below
stored definitions, never imports core, and owns no engine state or mutex.

## What it owns

Captured resolvers consume exact catalog/role/definition/path inputs. Shared
spellings and literal renderers preserve canonical SQL, quoting and selected
identity; stored-value walkers/remappers authenticate genuine destination types
and principals. Inquiries project actual descriptors rather than infer identity
from presentation text or same-number objects.

## Soft and hard input errors

Recoverability is producer-owned. `SoftInputError` marks only the input boundary's
own recoverable lookup/grammar errors; SQLSTATE alone is insufficient. Context,
provider, authority and unrelated runtime failures remain hard. Notice routing
uses the current execution operation rather than retaining capture-time runtime.

## Invariants

- Reads/resolvers use only their explicit immutable capture and path. They do not
  resample live session state or cache permission decisions.
- Names/numbers are presentation or lookup inputs, never provenance authority.
  Identity, address and typed datum mapping retain their separate contracts.
- Input/output/dump spellings, diagnostics and remap results are compatibility
  contracts because stored values and native scripts replay them.
- Callers own locking, including callbacks passed into capture helpers. Nothing
  here acquires Engine.Mu; unlocked core wrappers own live lookup and readmission.

## What stays in the engine

Statement formatter caches, current authority/notice adapters, prepared analysis
cells, lock windows, replacement staging and atomic publication.

## Testing

Pure resolver/literal/remap units use small genuine catalog captures. Engine
fixtures test live capture, prepared generation and replacement ownership;
public SQL remains in reference packages under
[docs/TESTING.md](../../../docs/TESTING.md).
