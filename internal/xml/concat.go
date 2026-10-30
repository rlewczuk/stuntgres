package xml

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// ConcatAccumulator owns one serial concatenation. It retains no operation
// authority. A failed append poisons this state, never an accepted operand.
// Copies of an accumulator must not be used after its first Append.
type ConcatAccumulator struct {
	first Result
	body strings.Builder
	declaration Declaration
	count int
	failed error
}

func concatOperand(ctx context.Context, r Result) (Declaration, string, error) {
	d, detail, err := readContentDeclaration(ctx, r.original)
	if err != nil { return Declaration{}, "", err }
	// xmlconcat ignores parse_xml_decl's return code. Its initial length is
	// the entire operand; a failed reader therefore contributes no body.
	if detail != "" { return d, "", nil }
	return d, r.original[d.End:], nil
}

func mergeConcatDeclaration(ctx context.Context, a, b Declaration) (Declaration, error) {
	d := Declaration{}
	equal := len(a.Version) == len(b.Version)
	if equal {
		for at := 0; at < len(a.Version); {
			if err := ctx.Err(); err != nil { return Declaration{}, err }
			end := at + min(4096, len(a.Version)-at)
			if a.Version[at:end] != b.Version[at:end] { equal = false; break }
			at = end
		}
	}
	if a.Present && b.Present && equal { d.Version, d.Present = a.Version, true }
	if a.Standalone != Absent && b.Standalone != Absent {
		d.Standalone = No
		if a.Standalone == Yes && b.Standalone == Yes { d.Standalone = Yes }
	}
	// Normalize after every binary transition, not once over all inputs.
	if d.Standalone != Absent && !d.Present { d.Version, d.Present = "1.0", true }
	if d.Standalone == Absent && d.Version == "1.0" { d.Version, d.Present = "", false }
	return d, ctx.Err()
}

func concatHeaderParts(d Declaration) [5]string {
	if !d.Present { return [5]string{} }
	standalone := ""
	if d.Standalone == Yes { standalone = " standalone=\"yes\"" }
	if d.Standalone == No { standalone = " standalone=\"no\"" }
	return [5]string{"<?xml version=\"", d.Version, "\"", standalone, "?>"}
}

func concatHeaderSize(d Declaration) (int, error) {
	total := 0
	for _, s := range concatHeaderParts(d) {
		if !appendSize(total, len(s)) { return 0, appendSizeError(total, len(s)) }
		total += len(s)
	}
	return total, nil
}

func concatCopy(ctx context.Context, b *strings.Builder, s string) error {
	for len(s) > 0 {
		if err := ctx.Err(); err != nil { return err }
		n := min(4096, len(s))
		b.WriteString(s[:n]); s = s[n:]
	}
	return ctx.Err()
}

// Reserve before writing so Builder never implicitly copies an unbounded prefix.
// Reset preserves prior immutable strings; only an empty builder is grown.
func (a *ConcatAccumulator) reserveConcatBody(ctx context.Context, additional int) error {
	if !appendSize(a.body.Len(), additional) { return appendSizeError(a.body.Len(), additional) }
	if additional <= a.body.Cap()-a.body.Len() { return nil }
	needed := a.body.Len()+additional
	capacity := needed
	if a.body.Cap() <= math.MaxInt/2 { capacity = max(capacity, a.body.Cap()*2) }
	if !appendSize(0, capacity) { capacity = needed }
	if err := ctx.Err(); err != nil { return err }
	prior := a.body.String()
	a.body.Reset()
	a.body.Grow(capacity)
	return concatCopy(ctx, &a.body, prior)
}

func (a *ConcatAccumulator) Append(ctx context.Context, r Result) (err error) {
	if a.failed != nil { return a.failed }
	defer func() { if err != nil { a.failed = err } }()
	if err = ctx.Err(); err != nil { return err }
	if !r.Valid() { return fmt.Errorf("cannot concatenate zero XML Result") }
	if a.count == 0 { a.first = r; a.count = 1; return nil }
	right, body, err := concatOperand(ctx, r)
	if err != nil { return err }
	left := a.declaration
	firstBody := ""
	if a.count == 1 {
		left, firstBody, err = concatOperand(ctx, a.first)
		if err != nil { return err }
	}
	d, err := mergeConcatDeclaration(ctx, left, right)
	if err != nil { return err }
	headerSize, err := concatHeaderSize(d)
	if err != nil { return err }
	total := a.body.Len()
	for _, size := range [3]int{len(firstBody), len(body), headerSize} {
		if !appendSize(total, size) { return appendSizeError(total, size) }
		total += size
	}
	if err = a.reserveConcatBody(ctx, total-headerSize-a.body.Len()); err != nil { return err }
	if err = concatCopy(ctx, &a.body, firstBody); err != nil { return err }
	if err = concatCopy(ctx, &a.body, body); err != nil { return err }
	a.declaration = d
	a.first = Result{}
	a.count++
	return nil
}

// Finish freezes the current spelling without reparsing accepted content. A
// later Append cannot mutate returned strings (Builder only appends).
func (a *ConcatAccumulator) Finish(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil { return Result{}, err }
	if a.failed != nil { return Result{}, a.failed }
	if a.count == 0 { return Result{}, fmt.Errorf("cannot finish empty XML concatenation") }
	if a.count == 1 { return a.first, nil }
	headerSize, err := concatHeaderSize(a.declaration)
	if err != nil { return Result{}, err }
	body := a.body.String()
	if headerSize != 0 {
		var out strings.Builder
		if !appendSize(len(body), headerSize) { return Result{}, appendSizeError(len(body), headerSize) }
		out.Grow(len(body)+headerSize)
		for _, part := range concatHeaderParts(a.declaration) {
			if err := concatCopy(ctx, &out, part); err != nil { return Result{}, err }
		}
		if err := concatCopy(ctx, &out, body); err != nil { return Result{}, err }
		body = out.String()
	}
	if err := ctx.Err(); err != nil { return Result{}, err }
	d := a.declaration
	d.End = headerSize
	return Result{original: body, requested: Content, effective: Content, declaration: d, valid: true}, nil
}
