# internal/engine/copyfmt

COPY formats: option parsing, FORCE binding, text/CSV reading and writing, and
binary framing. The package consumes readers/writers and parsed options; it owns
no statement, session, transaction, mutex or engine callback.

## What it owns

- `ParseOptions` validates ordered syntax into plain `Options` and shared format
  defaults. Scalar extraction, cross-option validation and positioned diagnostics
  follow the compatibility contract. Other consumers reuse this parser rather
  than implementing another COPY option grammar.
- `BindForces` resolves FORCE lists to per-field flags. Relation column resolution
  preserves actual catalog identity and query COPY uses the result schema.
- `LineReader` frames records, decodes fields and NULL/DEFAULT markers, and checks
  headers. `AppendText`/`AppendCSV` share output escaping; diagnostics preserve
  original field context independently of later value conversion.
- `BinaryReader` validates headers and tuple/field framing.
- `WriteRows` consumes a `RowSource` with the supplied output environment, reusing
  one values vector/output buffer. Short writes fail with `io.ErrShortWrite`.
  Conversion precedes escaping; failures preserve complete prior writes, not a
  partial row.

## What stays outside

Core owns protocol admission, conversion capabilities, cancellation and unlocked
I/O wrappers. DML owns input value conversion, insertion and COPY statement/line
context. The codec never checks wire state or acquires statement authority.

## Invariants

- COPY framing and diagnostics are compatibility contracts, byte for byte:
  signature and header, end marker, quoting, escapes, FORCE flags,
  NULL/DEFAULT markers, header checks, SQLSTATEs and texts.
- No per-row or per-field allocation: the line, decode, field and payload
  buffers are reused across records; `Fields`, `Line` and a `Field`
  payload are valid only until the next read. Binary payloads grow with
  bytes actually read, not the declared length.
  `WriteRows` allocates its value slice and one output buffer per
  statement and reuses them for every row.
- `LineReader` and `BinaryReader` are values held by one cursor; they are
  not safe for concurrent use and need no lock.

## Testing

Pure option/framing/codec/writer units live here. Core and DML tests exercise
cursor/protocol/cancellation integration; public behavior belongs in
[reference/dml](../../../reference/dml/README.md#testing).
