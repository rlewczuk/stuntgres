# internal/engine/copyfmt

The COPY data formats: option parsing, FORCE binding, text and CSV
reading and writing, and binary framing. It is plain Go over a reader or
writer and parsed options; it knows nothing of the statement, session or transaction
running the COPY.

The package sits at layer L4 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `catalog`, `parser`, `sqltype`, `sqlstate` and the
standard library. It holds no state beyond one reader's buffers, no mutex
and no engine callback. Step 19 of the split created it; see
[ENGINE-SPLIT-copyfmt.md](../../../docs/ENGINE-SPLIT-copyfmt.md). The name
is not `copy`, which would shadow the builtin in every importing file.

## What it owns

- **Options** (`options.go`): `ParseOptions` validates a `parser.Copy`'s
  options in PostgreSQL's order and applies the format defaults into
  `Options` (`Format` is `Text`, `CSV` or `Binary`; `Header` is 0, 1 or
  2 for MATCH). Error SQLSTATEs, texts and positions match PostgreSQL 18.
- **FORCE binding** (`force.go`): `Options.BindForces` turns
  FORCE_QUOTE/FORCE_NULL/FORCE_NOT_NULL into per-field flags, read with
  `Forced`. Relation COPY resolves names through `ResolveColumns`, the same
  column-list resolution the root binder uses (unknown, repeated and
  generated columns); query COPY matches the result schema. It reads
  `catalog.Relation`/`catalog.Column` so the relation errors keep their
  text without building a name list per statement.
- **Text and CSV** (`line_reader.go`, `csv.go`): `LineReader` frames
  records (LF/CR/CRLF consistency, the `\.` end marker in text, CSV quote
  parity), splits and decodes fields, recognizes NULL and DEFAULT markers
  against the `[]Target` built once per statement, checks the HEADER line
  (including MATCH) and applies the CSV FORCE null flags (`FieldNull`).
  `LineError` builds its 22P04 errors. Output: `AppendText`, `AppendCSV`.
  `ContextValue` truncates a value quoted in a diagnostic.
- **Binary** (`binary_reader.go`): `BinaryReader` checks the file header
  (`ReadHeader`, `BinarySignature`), and frames tuples (`Row`: field count,
  `-1` trailer, data after the trailer) and fields (`Field`: length word,
  size limits, payload).
- **COPY TO writers** (`writer.go`, `binary_writer.go`): `WriteRows`
  encodes the rows of a `RowSource` (`Next` into a value slice,
  `OutputEnvironment`) to an `io.Writer`, one `Write` per line or tuple
  (a short write is `io.ErrShortWrite`). Text and CSV write the HEADER
  line first and use `AppendText`/`AppendCSV`; binary writes the file
  header, the tuples and the `-1` trailer, and with `checkSend` resolves
  the send providers before the signature.

## What stays outside

- **Protocol** (root `copy_admission.go`, `protocol_copy*.go`,
  `CopyBoundary`, `StatementScope.Copy`/`CopyProtocol`): reservation, the
  wire hooks, releasing admission around every read and write
  (`copyReader`/`copyWriter` wrap the `io.Reader` this package receives),
  and cancellation. Wire-input checks (`CopyInput`,
  `PendingProtocolUserCancel`, `PendingCopyInputError`) stay in the
  cursors' `Next`, never in the codec.
- **Execution**: binding the target and producing the output rows in the
  root (`copy.go`; `copy_text.go`'s `copyOutput` passes them to `WriteRows`
  through `copyWriter` and re-checks the statement after the last row); converting fields to
  values, the "COPY t, line N" context and insertion in `dml`
  (`copy_cursor.go`, `copy_binary_cursor.go`, `copy_insertion.go`).

## Invariants

- COPY framing and diagnostics are byte-identical to the root code they
  came from: signature and header, end marker, quoting, escapes, FORCE
  flags, NULL/DEFAULT markers, header checks, SQLSTATEs and texts.
- No per-row or per-field allocation: the line, decode, field and payload
  buffers are reused across records; `Fields`, `Line` and a `Field`
  payload are valid only until the next read. Binary payloads grow with
  bytes actually read, not the declared length.
  `WriteRows` allocates its value slice and one output buffer per
  statement and reuses them for every row.
- `LineReader` and `BinaryReader` are values held by one cursor; they are
  not safe for concurrent use and need no lock.

## Testing

Unit tests: option parsing (`options_test.go`), CSV framing, fields and
output (`csv_test.go`), text escapes (`line_reader_test.go`), context
truncation (`context_test.go`) and the COPY TO writers (`writer_test.go`):

```sh
make check RUN='^TestCopy(CSV(Options|Framing|Fields|Output|Context)|BinaryOptions|TextEscapeUnits|WriteRows)$'
```

The public behavior is covered by the dual-mode reference tests
(`make check RUN='^TestReferenceCopy'`, plus `REFERENCE=1`); the root
cursor, protocol and cancellation units are `^Test(Copy|ProtocolCopy)`
in `internal/engine`.
