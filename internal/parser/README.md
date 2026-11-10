# internal/parser

The SQL lexer, goyacc grammar and AST. It imports only the standard library and
`sqlstate`; binding, name resolution and execution policy belong to callers.

## Entry points and generation

- `Parse` and `ParseWithOptions` consume a complete SQL script. String handling,
  context, notices and restricted parsing options are captured per call.
- `ParseTypeName` parses standalone declarations; the reg* input splitters reuse
  the shared scanner and type grammar.
- `ParsePLpgSQL` uses a private lexer/goyacc entry with ordinary SQL expressions.
  Procedural statement and operand spans refer to the original definition source.
- `ParseRoutineDeclarationBody` captures syntax only; it grants no executable
  admission or provider authority.
- `Expressions` and `WithExpressions` provide shared expression traversal and
  copy-on-change reconstruction. Structural serialization is not SQL rendering,
  binding or evaluation. Statement normalization reuses scanner token boundaries,
  not a second SQL parser.
- Edit `grammar.y`, then run `make generate`; never hand-edit `grammar_gen.go`.
  Generator details are in [docs/DEVELOPMENT.md](../../docs/DEVELOPMENT.md).

## Invariants

- Parser state and options are invocation-local. ASTs preserve byte/character
  source positions, parameter indices, ordered syntax and distinctions needed by
  later semantic owners. Parsing never resolves catalog objects or permissions.
- Consume the entire input, including unreachable procedural syntax. A valid
  prefix followed by junk is an error, never an executable partial parse.
- Contextual keywords retain ordinary identifier behavior outside their grammar
  context. Lookahead preserves scanner errors and original positions.
- Grammar actions copy semantic values and allocate independent AST nodes and
  slices. Never retain addresses of goyacc semantic cells (`&$n`). Traversal and
  reconstruction must include every expression-bearing child without mutating
  the original tree.
- Procedural capture uses grammar-owned source boundaries rather than reparsing
  fragments or guessing delimiters from SQL text. Syntax admission is not runtime
  eligibility or authority.
- Reg* splitters mark only their own recoverable input errors. Callback errors
  retain their origin; presentation must not turn hard parser failures into soft
  conversion failures.

## Scratch ownership

Scratch reuse is an allocation optimization, not a parsed-result cache. Each
parse borrows one exclusive fixed-size generated receiver. Generated cleanup and
a full typed-zero reset run before return, including errors, cancellation and
callback panic; production code does not recover panics. Lexers, options,
contexts, notices, ASTs and expanded goyacc stacks are never pooled.

The private `sync.Pool` is GC-evictable and imposes no active-parse limit. Tests
must not assume receiver identity, guaranteed hits or guaranteed eviction. Reset
units use an explicit receiver rather than observing the pool.

## Testing

Units drive the real lexer and generated parser and assert AST shape, original
positions, independent ownership and complete-input rejection. Public SQL behavior
belongs in `reference/`; follow [docs/TESTING.md](../../docs/TESTING.md).

Select grammar units by their existing feature prefix. Shared keyword units use
`RUN='^Test(Keyword|OrdinaryKeywordCallHeads)'`; scratch lifecycle units use
`RUN='^TestParseScratch'`, also with `RACE=1` for concurrent ownership.

For scratch changes, compare `BenchmarkParseScratchMixed` and
`BenchmarkParseScratchNested` before and after, serial and parallel, at default
startup parallelism and in a separate `GOMAXPROCS=1` process. Report ns/op, B/op
and allocs/op; never disable GC or rely on a warm pool identity.
