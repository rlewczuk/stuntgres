# Decimal arithmetic operators

## Scope and authority

Selected surface: NUMERIC `+`, unary `+`, `-`, unary `-`, `*`, `/`, `%`, `^`.
PostgreSQL18.0 UTF8/C remains the pinned runtime; published REL_18_0 numeric
regression outputs and the pinned public reference observations ground exact
strings/diagnostics. [PostgreSQL18 mathematical operators](https://www.postgresql.org/docs/18/functions-math.html#FUNCTIONS-MATH-OP-TABLE)
and [numeric storage](https://www.postgresql.org/docs/18/datatype-numeric.html)
describe the public contract. The implementation follows
[REL_18_0 numeric.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/adt/numeric.c)
precision schedules, not development-source runtime output.

This does not add SQL power(), ln(), sqrt(), a function registry, casts,
SELECT-without-FROM, an alternate evaluator/store or physical persistence.
Raw integer2^3^2 remains FLOAT8=64; explicitly typed Numeric witnesses cover
Numeric64 and right-grouped512. Existing REAL/Numeric resolution stays FLOAT8.

## Shared ownership and computation

`Arithmetic` remains an immutable comparable descriptor. Existing resolution,
implicit coercion, parameter occurrence typing and engine evaluation own the
binding/execution phases. Numeric providers run after descriptor/context
validation and strict NULL. Every result owns the bound result descriptor,
including unary identity, rather than retaining an operand's descriptor.

Values retain immutable coefficient strings, exponent, display scale and special
tag. big.Int coefficients/scratch are operation-local and never escape into a
Value. Addition/subtraction align exact coefficients and keep maximum scale;
zero identity paths avoid coefficient expansion. Multiplication computes the
full product before rounding combined scale to16383. Each operator checks its
packed result, so a later subtraction cannot cancel an earlier overflow.

`numericLeading` and `numericDivisionScale` are shared with AVG: normalized
base10000 weight/first digit selects the decimal result scale, clipped to1000.
AVG retains its separate accumulation/transient-subtotal policy. Division uses
signed exact quotient/remainder and half-away rounding. `%` delegates to the
existing exact modulo helper shared by SQL mod(). SQL div() remains truncating
integer quotient, not the implementation of fractional `/`.

## Power precision and range

Integrality/parity use normalized decimal digits. Exact int32 range selects the
integer path; larger exact integers and fractions select the general path.
Integer power uses release leading-digit precision estimates,0/1/−1/2
shortcuts and guarded repeated squaring with varying local scales. Negative
powers divide at the chosen power scale, not the ordinary division scale.
The integer physical bound remains distinct from general power's exp guard.

General power computes decimal ln(base), multiplies by the exact exponent and
uses the explicit-target-scale exponential kernel shared with SQL exp(). A
low-precision pass selects significant precision; a refined pass supplies the
rounded decimal result. Exact base−1 supplies near-one logarithm weight, avoiding
binary cancellation. Private ln reduces with decimal square roots, then the
atanh series with release guards. Square-root rounding compares the original
scaled rational against the exact half threshold. Float estimates select
precision/guards only; no math.Pow or binary result fallback is used.

NaN/infinity/domain rules precede finite math. Special canonical1/0 results,
zero-negative2201F and negative-nonintegral2201F retain release behavior.
Finite node overflow and general exponential guard yield22003
`value overflows numeric format`; tiny division/power results round to zero,
not floating underflow. Destination typmod overflow remains separately owned.

## Metadata, stored consumers and publication

Computed Usage is NUMERIC/OID1700/typmod−1, no physical origin and unknown
nullability/DecimalSize. Native ScanType is string; pq's raw bytes and metadata
decoding are separately asserted adapter contracts. Direct/held parameters use
SQL-inferred Numeric slots with fresh scales and detached input bytes.
Zero-argument native binding retains the predecessor42P08 boundary while pq's
simple protocol reports42P02; both adapters expose the actual unnamed-portal
parameter Context for invalid Numeric binds.

Existing DEFAULT/CHECK/view capture and symbolic deparse preserve typed power
leaves/grouping. Semantic replay demands64/512 rather than incidental whitespace.
Dump/Reset/Restore reuse exact codecs and isolated catalog installation. Query
finalization, write Finish and ReplaceCatalog retain failure/cancellation fences;
old immutable cursors/metadata survive replacement. Iterative decimal math checks
context at convergence/reduction/squaring stages. No interruption inside an
indivisible big.Int operation or public mid-running Restore is claimed.

## Evidence and root verification

Permanent public scenarios use only `reference/fixture` test support and public driver,
error and control APIs. Exact finite/scale/power/special expectations precede
backend parity. High-precision pinned strings are exact; the2609-byte guard
success is checked in full by SHA256 plus weight/scale/prefix/suffix. No oracle
filters, observation loggers, native-derived goldens or numeric tolerances remain.
Package tests supplement provider ownership, exact private rounding/classification
and real-computation publication fences. They do not replace public reference SQL.

Run each selective command under a900-second deadline:

```sh
make check RUN='^TestReference(NumericArithmetic|ArithmeticBoundaries)'
make check REFERENCE=1 RUN='^TestReference(NumericArithmetic|ArithmeticBoundaries)'
make check RUN='^TestReference(NumericAvg|MathScalar|FloatingArithmetic)'
make check REFERENCE=1 RUN='^TestReference(NumericAvg|MathScalar|FloatingArithmetic)'
make check RUN='^Test(NumericArithmetic|NumericPower|NumericAvg|MathScalar)'
make check RUN='^TestExpressionNumericArithmetic'
make check RUN='^TestReferenceNumericArithmeticFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceNumericArithmeticFeatureSmoke$'
```

Smoke demands exact0.3,9223372036854775809,2.76000, rounded division/remainder,
left64/right512, fractional18.379173679952560, held scales/NULL/NUMERIC metadata,
full diagnostics, late-write unchanged sentinel and native symbolic/control replay.
Root owns verification; this design is not evidence that unexercised checks pass.
After integration, final `make finalcheck` has one1800-second deadline.
