# Authentication-file reader

`authfile.Read(ctx, path)` owns the authentication-file lexer/include traversal
used by `identfile`. Records own filename, starting physical line, token text
and initially-quoted state. The reader does not discover sources, parse methods,
resolve users or authenticate.

The host supplies an authorized path. Relative includes resolve against their
containing file; absolute spellings preserve filesystem/symlink semantics.
Traversal is bounded and does not cache file contents. Top-level I/O failures
are SQL exceptions; include failures become record diagnostics. Cancellation
propagates as a context error and never publishes a prefix.

Callers release Engine/Driver mutexes before entering the package.

## Testing

External component units live in `enginetests`, integrated parser tests in
`identfile/enginetests`, and public SQL scenarios in `reference/catalogs`.
