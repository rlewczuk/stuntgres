# Authentication-file reader

`authfile.Read(ctx, path)` owns the PostgreSQL authentication-file lexer/include
traversal used by `identfile`. Records own their filename, starting physical
line, token text and initially-quoted flag. The reader does not parse methods,
resolve users, authenticate, or discover sources.

The host supplies an authorized path. Absolute include and `@` spellings retain
noncanonical components and symlink traversal; relative paths resolve against
the containing file. Includes and token expansion share the released depth
counter, directory discovery finishes before parsing, and retained directory
open errors may repeat across a later successful file. Continuations strip all
trailing CR/LF from accumulated bytes before applying the progress guard. No
Scanner line-size cap or persistent content cache is used.

Top open/read failures are SQL exceptions; include failures become record
diagnostics. Cancellation always propagates as an ordinary context error and
never publishes a prefix.
The caller must release Engine/Driver mutexes before entering this package.

## Testing

External component units live in `enginetests`; use
`make check RUN='^TestAuthFile'`. Integrated ident parser regressions remain in
`identfile/enginetests`, and public SQL scenarios remain in `reference/catalogs`.
