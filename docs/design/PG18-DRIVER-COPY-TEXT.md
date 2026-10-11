# PostgreSQL18 COPY text streams

This deliverable records the implementation of the part's living plan, not a second orchestration plan or a claim that verification has run.

## Public boundary and transport

The production API is symmetric `CopyFrom(context.Context, *sql.Conn, string, io.Reader)` / `CopyTo(context.Context, *sql.Conn, string, io.Writer)`, returning `CopyResult{Count int64, CommandTag string}`. `sql.Conn.Raw` admits only this native driver's actual physical connection. All statements are parsed with that connection's captured parser settings. A stream request contains exactly one COPY of the matching direction; it has no parameter interpolation or alternate SQL execution path. Caller streams are not closed. Native caller errors retain identity; malformed arguments and precancellation perform no caller I/O. Failed requests have a zero result even when COPY TO already emitted a prefix.

The reference fixture's ancillary pgx stdlib5.9.2 pool uses the existing PostgreSQL18.0 DSN/container and genuine pgconn CopyFrom/CopyTo. Existing SQL fixtures stay on lib/pq. pgx dependencies are test-only; production code uses the standard library and existing module packages. Diagnostics extract every pgconn logical field, not just SQLSTATE/message.

## Grammar and options

COPY has its own positioned AST, including direction, endpoint, table/query, ordered columns and ordered options. COPY remains an unreserved identifier. Parsing consumes the whole input, including file/PROGRAM syntax, before native capability rejection. Ordinary database/sql calls have no stream channel and reject COPY during the ordinary admitted request. File and PROGRAM never open a file or invoke a process.

Validation handles FORMAT text, DELIMITER, NULL, DEFAULT and HEADER; ordered duplicates preserve their source position. DEFAULT is FROM-only and is compared against raw input before type conversion. CSV/binary and options needing unimplemented providers are explicit capability failures. HEADER is not ordinary generic Boolean parsing: true/false, on/off and numeric1/0 are the release forms; yes/no and quoted numeric aliases are rejected. MATCH uses target column names, including empty-input and header-only framing.

FORMAT compares the preserved option value directly: quoted `'TEXT'` and `'Text'`
are unknown formats with their original-case diagnostic and source position,
while unquoted identifiers retain ordinary lexer folding. Table COPY opens and
locks its original target, selects columns and checks INSERT/SELECT ACLs before
options or input default/type-environment initialization. TO relation-kind checks
follow those ACLs before options; FROM kind checks follow input default preflight.
Query TO instead validates options before existing query analysis. Later relation,
grant and publication revalidation is retained; no second binding authority is used.


## Input and atomic DML

A reusable cursor reads text through a mutex-free I/O wrapper. Framing recognizes consistent LF/CR/CRLF, final unterminated rows, escaped physical line endings, and standalone raw `\.` termination. Field splitting is escape-aware; decoding handles traditional backslash controls, octal/hex and escaped delimiters. Markers match raw fields. Raw/decoded bytes pass the existing canonical UTF8/NUL text-input validation.

Actual `sqltype.Usage` input environments provide scalar/domain/array and symbolic catalog codecs; COPY does not parse SQL literal strings or duplicate a type switch. Omission masks distinguish SQL NULL from effective DEFAULT markers. The shared INSERT binding/default helper and row assembly fill identities/defaults, compute generated values, check constraints, assemble all rows privately and invoke existing partition/write publication once. No per-row INSERT execution, partial row publication or parallel DML is introduced. Constant default preflight follows existing folding; a folded NULL is not an effective DEFAULT value. Sequence allocations remain governed by existing sequence semantics rather than fictional transactional sequence rollback.

COPY error context is attached at the actual stage: line/column for conversion, raw line for framing/field/default diagnostics, line-only for constraint contexts where the server does so. Already supplied contexts and primary errors win; object fields are not guessed from names in prose. A late row, reader, cancellation or publication failure discards the complete statement stage.

Actual SQL-owned timeout causes are translated by Operation.CommandError before
active input annotation. Admitted-input statement timeout carries COPY line
Context; relation-opening timeout remains outside the cursor with empty Context.
Caller context/I/O identities and existing FATAL diagnostics remain primary.


## Output

Query COPY runs the existing SELECT/query authority and uses its final schema, captured output environment and symbolic regclass/regtype snapshot. Table COPY captures the physical relation visibility prefix with the existing source machinery; it does not rewrite a table into an inherited SELECT. Table column/default/generated selection follows COPY's explicit target list contract. The serializer uses actual type output and text escaping. Count increments only after a complete row write; a short write fails with `io.ErrShortWrite`. The final scope active check/Finish remain publication vetoes.

Table COPY excludes generated columns by default and rejects explicit generated
targets in either direction; query COPY can include their generated values.


## Physical owner reservation and mutex-free I/O

An Operation lease is not exclusivity. COPY installs a pointer-owned reservation and completion channel under transactionOwner.admission. StartStatement, Begin, Commit, Rollback and EndSession all enter through the same admission helper. An entrant seeing COPY captures the completion channel, releases admission, waits with its own context, then retries and revalidates. Ordinary statements retain the existing admission hold, and the ordinary no-wait path allocates no completion channel.

Before every actual caller Read/Write, COPY drops any replacement read fence it currently owns and releases admission. No engine/session/owner/admission/replacement mutex is held inside user code. Deferred resumption reacquires admission directly as the reservation owner, restores only the fence previously held, and revalidates pointer ownership, operation liveness, original transaction reference/command, catalog epoch and captured relation/layout/storage authority. Caller errors remain primary; a successful/EOF callback cannot conceal stale authority. Peers can progress; Reset/Restore can replace the catalog while I/O is blocked, but COPY then rejects the obsolete authority instead of rebinding names.

The pinned sql.Conn still belongs to Raw throughout the transfer. Read and Write
callbacks must not execute SQL or reenter that same Conn; independent peer
connections can perform SQL or controls despite the mutex-free engine I/O seam.


Finish clears its own reservation and closes the completion channel once immediately before the final admission unlock. Copying a scope cannot release another pointer's reservation. A callback panic reacquires locks through deferred resumption; the driver performs failed Finish/lease cleanup before rethrowing. Termination cancels without admission, then waits for leased I/O to return. No caller-closing shortcut or detached read/write goroutine is used.

The cleaned request panic is caught at the Raw callback boundary and returned as an ordinary cleanup error through Raw's normal release, then the original value is rethrown outside Raw. Letting it escape Raw itself would make database/sql retire an otherwise cleaned native connection; swallowing it or claiming successful completion would be incorrect.

## Preserved boundaries

Native RLS execution is not present in the prerequisite engine and is not invented here. Existing unsupported query/type/DDL providers retain their boundaries. Native controls are actual Dump/Reset/Restore; the pinned reference counterpart replays real native dumps rather than pretending PostgreSQL has native reset APIs. Files/PROGRAM, CSV/binary and error-skipping options are excluded, never successful no-ops.

See [public API](../DRIVER.md#pinned-copy-text-streams--change-note), [dialect](../DIALECT.md#copy-text-streams--change-note) and [selected proof commands](../TESTING.md#copy-text-selected-proof). Root owns grammar generation, formatting, selected native/real/race verification and integration; this document does not substitute readiness for those observations.
