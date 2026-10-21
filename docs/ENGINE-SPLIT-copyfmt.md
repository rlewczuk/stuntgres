# Engine split, step 19: `internal/engine/copyfmt`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-copyfmt.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 19; §3 (layer
  table); §7 decision 12 (the name).
- **Needs:** 19a: nothing. 19b: 19a. Step 29 (`dml`, item 108) needs 19b.
- **Linear items:** 19a = item 58 (needs —), 19b = item 59 (needs 58).
- **Effort:** high for both.
- **Session size:** one sub-step per session.
- **Tools:** `reach`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach -dir . -files '<regexp>'`)
  and `frontier`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier -dir . -files a.go,b.go`),
  run from the worktree root; never pipe them into `head`. Rewrite tools:
  `movex`, `keylit`, `shadowrn` in `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/`;
  `movespec` and `expspec` in `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`.

## Goal

COPY has three layers mixed in the same files:

- **protocol**: reserving the statement, the wire hooks, releasing locks
  around every read and write, cancellation (`copy_admission.go`,
  `protocol_copy*.go`);
- **execution**: binding the target, inserting rows, unique checks,
  partition routing, producing output rows (`copy.go`,
  `copy_insertion.go`, the cursors' `Next`);
- **codec**: the text, CSV and binary formats themselves: option parsing,
  line splitting, escapes, quoting, header lines, binary framing.

The codec is plain Go over a reader and options, but it is tangled with
the other two: `copyCursor` and `copyBinaryCursor` carry a
`*StatementScope` and an `analyze.Binding` only to reach the operation
context, the input environment and the target columns. This step splits
each cursor (19a) and moves the codec into `copyfmt`, a leaf package
(19b). The protocol stays in the root; execution goes to `dml` (step 29).

The name is `copyfmt`, not `copy`: a package named `copy` would shadow the
builtin `copy` in every importing file.

## Inventory (measured at `c154e14fb`)

| File | Lines | Clean / dirty lines | Layer |
|---|---:|---|---|
| `copy_options.go` | 231 | 217 / 0 | codec (options); `CopyBoundary` (13 lines) is protocol facade |
| `copy_force.go` | 75 | 67 / 0 | codec (FORCE_QUOTE/FORCE_NULL/FORCE_NOT_NULL binding) |
| `copy_text.go` | 508 | 104 / 388 | codec (`appendCopyText`, `decodeCopyField`, `copyHex`, `copyField`, `copyContextValue`, and the cursor methods `readLine` 87, `split` 46, `header` 25, `lineError` 5); execution (`Next` 80, `diagnostic` 28, `copyOutput` 67); protocol (`copyCommandError` 21) |
| `copy_csv.go` | 160 | 16 / 133 | codec (`appendCopyCSV`, `readCSVLine` 73, `splitCSV` 60) |
| `copy_binary.go` | 305 | 3 / 284 | codec (`copyBinarySignature`, `binaryEOF`, `headerInput` 48, `readFixed` 4); execution (`Next` 112, `diagnostic` 23, `copyBinaryOutput` 71) |
| `copy_insertion.go` | 129 | 46 / 70 | execution (insertion batching with partition groups) → `dml` |
| `copy.go` | 471 | 70 / 386 | execution (`bindCopyLocked`, `initCopyInputLocked`, `copyTableRows`) and the `StatementScope.Copy`/`CopyProtocol` facade |
| `copy_admission.go` | 93 | 0 / 85 | protocol (`reserveCopy`, `copyIO`, `copyReader`, `copyWriter`) |
| `protocol_copy.go`, `protocol_copy_query.go` | 43, 101 | 20 / 106 | protocol (`CopyProtocolHooks`, `CopyProtocolMetadata`, `startProtocolCopy`, protocol COPY of a query) |

Codec lines: about 0.4k are clean today; the cursor split makes the codec
methods clean too (about 0.35k), so `copyfmt` gets about 0.75k lines. The
cursors are dirty only because of their `scope` field
(`c.scope.op.Context()`, `c.scope.op.CopyInput`, `c.scope.input`,
`c.scope.copyCommandError`) and their `binding` field (`Targets`,
`Insert.Defaults`, `Relation.Columns`, `Relation.Name()`).

Public facade (stays in the root): `CopyBoundary` (called by the repo-root
driver, `copy.go:126`), `StatementScope.Copy` (`copy.go:131` of the
driver), `CopyProtocolHooks` and `CopyProtocolMetadata` (used by
`cmd/stuntgres/server.go` and `cmd/stuntgres/protocol_copy.go`).

Unit tests that read codec internals: `copy_csv_test.go`,
`copy_binary_test.go` (`parseCopyOptions`), `copy_admission_test.go`
(`decodeCopyField`, `appendCopyText`), `protocol_copy_cursor_cancel_test.go`,
`protocol_terminal_test.go`, `protocol_mechanism_test.go` (the cursors and
`copyOptions`).

## Part A

### 19a — Split the cursors, in place

1. Re-measure with `reach` on `'^(copy.*|protocol_copy.*)\.go$'`; confirm
   the table. Stop and report if the cursors changed shape.
2. **Facade out of the codec file.** Move `CopyBoundary` from
   `copy_options.go` to `protocol_copy.go`. `copyInsertionCursor`,
   `copyInsertionBuffer` and `copyInsertionState` stay in
   `copy_insertion.go`: they are execution. Commit.
3. **Target description.** Add a codec-side record built once per COPY
   from the binding, so the codec no longer reads `analyze.Binding`:

   ```go
   type copyTarget struct {
       name       string // column name, for header checks and diagnostics
       hasDefault bool   // a DEFAULT marker is allowed for this column
   }
   ```

   `split`, `splitCSV` and `header` read `[]copyTarget` instead of
   `c.binding.Targets`, `c.binding.Insert.Defaults` and
   `c.binding.Relation.Columns`. The DEFAULT-marker detail text
   (`Column "%s" has no default value.`) must stay byte-identical.
4. **Split `copyCursor`.** Introduce `copyLineReader` with the fields the
   codec methods use: `options`, `reader *bufio.Reader`, `line`,
   `decoded`, `fields`, `number`, `eol`, `ended`, `headerDone`,
   `lineValid`, `targets []copyTarget`. Move `readLine`, `split`, `header`,
   `readCSVLine`, `splitCSV` and `lineError` onto it. `copyCursor` keeps
   `scope`, `binding`, `defaults`, `omitted`, `column`, `value`,
   `nullInput` and embeds or holds the line reader; `Next`,
   `diagnostic`, `rowDefaults` and `insertionOrigin` stay on it.
5. **Split `copyBinaryCursor`** the same way: `copyBinaryReader` holds
   `reader`, `scratch`, `payload`, `number`, `header`, `ended` and gets
   `headerInput` and `readFixed`; the cursor keeps `scope`, `binding`,
   `defaults`, `omitted`, `column` and `Next`, `diagnostic`.
6. **Cancellation stays outside the codec.** The wire-input checks in
   `Next` and `headerInput` (`c.scope.op.CopyInput.Active`, `Held()`,
   `session.PendingProtocolUserCancel`, `PendingCopyInputError`) stay in the
   cursor. If `headerInput` needs one of them, pass a small
   `func(context.Context) context.Context` or split the method; do not
   give the codec a session.
7. The `copyReader{scope, r}` and `copyWriter{scope, w}` wrappers (they
   release admission around every read and write) stay in the root; the
   codec receives the wrapped `io.Reader`.
8. **Output.** `copyOutput` and `copyBinaryOutput` stay execution. Their
   per-value encoding already goes through `appendCopyText`,
   `appendCopyCSV` and `copyBinarySignature`, which are codec.
9. Re-run `reach`: the line reader, the binary reader, the options, FORCE
   binding and the append/decode functions must be clean. Commit.

### 19b — Create `copyfmt` and move the codec

1. Create `internal/engine/copyfmt` with `errors.go` (a private `sqlError`
   copy; the codec errors are `*sqlstate.Error` values) and `README.md`.
2. Move and export (proposed names; keep them short):
   `copyOptions` → `Options` (exported fields `Format`, `Delimiter`,
   `Quote`, `Escape`, `Null`, `Default`, `HasDefault`, `Header`,
   `ForceQuote`, `ForceNull`, `ForceNotNull`, `QuoteFields`,
   `NullFields`, `NotNullFields`); `copyFormat` → `Format` with `Text`,
   `CSV`, `Binary`; `parseCopyOptions` → `ParseOptions`; `copyForce` →
   `Force`; `bindCopyForce` and `bindForces` → `BindForce`,
   `Options.BindForces`; `copyField` → `Field`; `decodeCopyField` →
   `DecodeField`; `appendCopyText` → `AppendText`; `appendCopyCSV` →
   `AppendCSV`; `copyContextValue` → `ContextValue`; `copyBinarySignature`
   → `BinarySignature`; `binaryEOF` → `BinaryEOF`; `copyTarget` →
   `Target`; `copyLineReader` → `LineReader`; `copyBinaryReader` →
   `BinaryReader`, with their methods exported where the root calls them.
3. Rewrite the root (`copy*.go`, `protocol_copy*.go`) to `copyfmt.X`. No
   aliases.
4. Imports of `copyfmt`: `catalog` (FORCE binding reads `catalog.Relation`
   and `catalog.Column`), `parser`, `sqltype`, `sqlstate` and the standard
   library. If FORCE binding can take column names instead of catalog
   types without changing behavior, drop `catalog`; record the choice.
5. Move the unit tests that test only codec code into `copyfmt`
   (`parseCopyOptions` cases from `copy_csv_test.go` and
   `copy_binary_test.go`, `decodeCopyField`/`appendCopyText` cases from
   `copy_admission_test.go`); rewrite the rest to the new names.

## Invariants

- COPY framing is unchanged: the binary signature and header, the
  end-of-data marker (`\.`), CSV quoting, escapes and FORCE flags, NULL and
  DEFAULT markers, header-line checks, and the "COPY t, line N" context of
  every diagnostic, with the same SQLSTATEs and texts.
- `CopyProtocolHooks.Start` fires only at the reserved boundary, after
  option and relation validation, with no lock held. `copyIO` still
  releases the owner's admission and `replacementMu` around every read and
  write and re-checks the lease, `txRef` and command afterwards.
- Cancellation keeps its mapping: `copyCommandError` turns wire
  cancellation into 57014 or FATAL as today; `PendingProtocolUserCancel`
  and `PendingCopyInputError` are checked at the same points.
- No new per-row or per-field allocation: the line and decode buffers stay
  reused across rows; `[]Target` is built once per statement.
- `copyfmt` holds no state beyond one reader's buffers, no mutex and no
  engine callback.

## Targeted tests

Tests follow the code and never block a move (ENGINE-SPLIT §4).

- Engine units: `copy_csv_test.go`, `copy_binary_test.go`,
  `copy_admission_test.go`, `copy_csv_context_test.go`,
  `protocol_copy_cursor_cancel_test.go`, `protocol_copy_scope_test.go`,
  `protocol_copy_input_start_test.go`, `protocol_terminal_test.go`,
  `protocol_mechanism_test.go`. Run the cancellation and protocol units
  with `RACE=1`.
- Reference selections, quick mode and `REFERENCE=1` (names only; never
  edit `reference/` or `smoke/`): `make check RUN='^TestReferenceCopy'`
  (62 tests: `TestReferenceCopyText*`, `TestReferenceCopyCSV*`,
  `TestReferenceCopyBinary*`, including `...Framing`, `...Diagnostics`,
  `...Cancellation`, `...RoundTrip`, `...LineCounting`,
  `...ForceOptions`) and `TestReferenceCommitTimeCopyPublication`. With
  `REFERENCE=1` run at least the `Framing`, `Diagnostics`, `RoundTrip` and
  `Cancellation` tests of each format.
- `cmd/stuntgres` COPY protocol tests run in `make quickcheck`.
- **Allocation check** (19a and 19b): write a throwaway benchmark under
  `.cswd/tmp/<task>/` (not in the repository) that COPYs 10,000 text, CSV
  and binary rows into a table through the root driver. Build it as a
  test binary (`go test -c`) once in a detached `master` worktree
  (`git worktree add --detach`) and once on the branch, then run the two
  binaries alternately with `-test.benchmem`, so load from other sessions
  affects both. Allocations per operation must not grow. `make bench` is not
  needed: startup is untouched.

## Documentation

- Create `internal/engine/copyfmt/README.md` (19b): what it owns (option
  parsing, FORCE binding, text/CSV/binary decoding and encoding, binary
  framing), what stays in the root (protocol, reservation, cancellation)
  and in `dml` later (execution), the no-allocation rule, `## Testing`.
- Root `internal/engine/README.md`: the "Server protocol ownership" COPY
  paragraphs describe the protocol, which stays; change only sentences
  that name moved functions.
- `docs/design/PG18-DRIVER-COPY-TEXT.md`: where it names a moved
  structure, replace the passage with a one-line pointer to the
  `copyfmt` README (§8.4).
- `AGENTS.md`: add `copyfmt` to the subpackage brace list (19b); the file
  must not grow.
- Tick 19a and 19b here and in
  [`ENGINE-SPLIT-STATUS.md`](ENGINE-SPLIT-STATUS.md); update the §8 cell.

## Done when

The COPY codec (options, FORCE binding, text/CSV/binary reading and
encoding) lives in `copyfmt`, which imports only `catalog`, `parser`,
`sqltype`, `sqlstate` and the standard library; the root cursors hold only
execution and protocol state. The checks in §8.3 pass and allocations do
not grow.

## Status

- [ ] 19a — Cursors split in place
- [ ] 19b — `copyfmt` created and the codec moved

Outstanding aliases: none yet.

### Notes for later steps

- Step 29 (`dml`): the COPY execution half (`bindCopyLocked`,
  `initCopyInputLocked`, `copyTableRows`, the cursors' `Next` and
  `diagnostic`, `copy_insertion.go`, `copyOutput`, `copyBinaryOutput`)
  moves with the DML executors. The protocol half (`copy_admission.go`,
  `protocol_copy*.go`, `CopyBoundary`, `StatementScope.Copy` and
  `CopyProtocol`) stays in the root.
- §6 estimated the codec at about 1.0k lines; measured, it is about 0.75k.
  The insertion batching records (`copyInsertion*`) are execution, not
  codec.
