package functions

import (
	"context"
	"fmt"
	"math"
	"unicode/utf8"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// LevenshteinSignature admits only the four installed PostgreSQL signatures.
func LevenshteinSignature(name string, count int) string {
	switch {
	case name == "levenshtein" && count == 2:
		return "25,25"
	case name == "levenshtein" && count == 5:
		return "25,25,23,23,23"
	case name == "levenshtein_less_equal" && count == 3:
		return "25,25,23"
	case name == "levenshtein_less_equal" && count == 6:
		return "25,25,23,23,23,23"
	}
	return ""
}

func LevenshteinInput(name string, args []Argument, text, integer sqltype.Usage) Resolution {
	if LevenshteinSignature(name, len(args)) == "" {
		return Missing
	}
	for i, arg := range args {
		target := text
		if i >= 2 {
			target = integer
		}
		if arg.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(arg.Usage, target, sqltype.Implicit); err != nil {
				return Missing
			}
		}
	}
	return Resolved
}

func CoerceLevenshteinArguments(b ArgumentCoercer, args []Argument, text, integer sqltype.Usage) ([]expr.Expr, error) {
	children := make([]expr.Expr, len(args))
	for i, arg := range args {
		target := text
		if i >= 2 {
			target = integer
		}
		child, err := b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		children[i] = child
	}
	return children, nil
}

type Levenshtein struct {
	expr.Info
	Function string
	Args     []expr.Expr
}

func NewLevenshtein(info expr.Info, name string, args []expr.Expr) (Levenshtein, error) {
	call := Levenshtein{Info: info, Function: name, Args: args}
	return call, call.Validate()
}

func (e Levenshtein) Reconstruct(info expr.Info, args []expr.Expr) (Levenshtein, error) {
	return NewLevenshtein(info, e.Function, args)
}

// Levenshtein implements expr.Composite.
var _ expr.Composite = Levenshtein{}

func (e Levenshtein) Children() []expr.Expr { return e.Args }
func (e Levenshtein) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Levenshtein", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}

func (e Levenshtein) Validate() error {
	if LevenshteinSignature(e.Function, len(e.Args)) == "" || e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Int4 {
		return fmt.Errorf("stuntgres: invalid stored Levenshtein call")
	}
	for i, arg := range e.Args {
		want := sqltype.Text
		if i >= 2 {
			want = sqltype.Int4
		}
		if arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != want {
			return fmt.Errorf("stuntgres: invalid stored Levenshtein argument")
		}
	}
	return nil
}

func (e Levenshtein) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [6]sqltype.Value
	null := false
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	source, err := values[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	target, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	ins, del, sub, bound := int32(1), int32(1), int32(1), int32(-1)
	bounded := e.Function == "levenshtein_less_equal"
	if len(e.Args) >= 5 {
		costs := [3]*int32{&ins, &del, &sub}
		for i, cost := range costs {
			v, err := values[i+2].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
			*cost = int32(v)
		}
	}
	if bounded {
		v, err := values[len(e.Args)-1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		bound = int32(v)
	}
	result, err := LevenshteinDistance(ctx, source, target, ins, del, sub, bound)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Integer(e.Usage.Type, int64(result))
}

// LevenshteinDistance follows PostgreSQL REL_18_6 utils/adt/levenshtein.c,
// including its band slides and int64 intermediates: a result outside int32
// is 22003 rather than 18.0's wrapped value. Negative costs cannot be replaced
// by a mathematical full-DP fallback: the release's nonnegative-bound pruning
// remains observable.
// Copyright (c) 2001-2025, PostgreSQL Global Development Group.
// Permission to use, copy, modify, and distribute this software and its
// documentation for any purpose, without fee, and without a written agreement
// is hereby granted, provided that the above copyright notice and this
// paragraph and the following two paragraphs appear in all copies.
//
// IN NO EVENT SHALL THE UNIVERSITY OF CALIFORNIA BE LIABLE TO ANY PARTY FOR
// DIRECT, INDIRECT, SPECIAL, INCIDENTAL, OR CONSEQUENTIAL DAMAGES, INCLUDING
// LOST PROFITS, ARISING OUT OF THE USE OF THIS SOFTWARE AND ITS
// DOCUMENTATION, EVEN IF THE UNIVERSITY OF CALIFORNIA HAS BEEN ADVISED OF THE
// POSSIBILITY OF SUCH DAMAGE.
//
// THE UNIVERSITY OF CALIFORNIA SPECIFICALLY DISCLAIMS ANY WARRANTIES,
// INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY
// AND FITNESS FOR A PARTICULAR PURPOSE. THE SOFTWARE PROVIDED HEREUNDER IS
// ON AN "AS IS" BASIS, AND THE UNIVERSITY OF CALIFORNIA HAS NO OBLIGATIONS TO
// PROVIDE MAINTENANCE, SUPPORT, UPDATES, ENHANCEMENTS, OR MODIFICATIONS.
func LevenshteinDistance(ctx context.Context, source, target string, insCost, delCost, subCost, maxDistance int32) (int32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	ins, del, sub, bound := int64(insCost), int64(delCost), int64(subCost), int64(maxDistance)
	m, n := utf8.RuneCountInString(source), utf8.RuneCountInString(target)
	// Release order: empty operands bypass both the limit and the bound.
	if m == 0 {
		return levenshteinResult(int64(n) * ins)
	}
	if n == 0 {
		return levenshteinResult(int64(m) * del)
	}
	if m > 255 || n > 255 {
		return 0, sqlError("22023", "levenshtein argument exceeds maximum length of 255 characters")
	}
	start, stop := 0, m+1
	if bound >= 0 {
		net := int64(n - m)
		minimum := net * ins
		if net < 0 {
			minimum = -net * del
		}
		if minimum > bound {
			return levenshteinResult(bound + 1)
		}
		sub = min(sub, ins+del)
		maximum := minimum + sub*int64(min(m, n))
		if bound >= maximum {
			bound = -1
		} else if ins+del > 0 {
			best := int64(0)
			if net < 0 {
				best = -net
			}
			stop = int(min(best+(bound-minimum)/(ins+del)+1, int64(m+1)))
		}
	}
	var sourceRunes, targetRunes [255]rune
	i := 0
	for _, r := range source {
		sourceRunes[i] = r
		i++
	}
	i = 0
	for _, r := range target {
		targetRunes[i] = r
		i++
	}
	var rows [2][256]int64
	prev, curr := &rows[0], &rows[1]
	for i := start; i < stop; i++ {
		prev[i] = int64(i) * del
	}
	for j := 1; j <= n; j++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if stop < m+1 {
			prev[stop] = bound + 1
			stop++
		}
		i := start
		if start == 0 {
			curr[0] = int64(j) * ins
			i = 1
		}
		for ; i < stop; i++ {
			substitution := prev[i-1]
			if sourceRunes[i-1] != targetRunes[j-1] {
				substitution += sub
			}
			curr[i] = min(prev[i]+ins, curr[i-1]+del, substitution)
		}
		prev, curr = curr, prev
		if bound >= 0 {
			zero := j - (n - m)
			residual := func(column int) int64 {
				net := int64(column - zero)
				if net > 0 {
					return net * ins
				}
				return -net * del
			}
			for stop > 0 && prev[stop-1]+residual(stop-1) > bound {
				stop--
			}
			for start < stop && prev[start]+residual(start) > bound {
				prev[start], curr[start] = bound+1, bound+1
				start++
			}
			if start >= stop {
				return levenshteinResult(bound + 1)
			}
		}
	}
	return levenshteinResult(prev[m])
}

// levenshteinResult is REL_18_6's int32 range check of a final distance.
func levenshteinResult(distance int64) (int32, error) {
	if distance < math.MinInt32 || distance > math.MaxInt32 {
		return 0, sqlError("22003", "levenshtein distance out of range")
	}
	return int32(distance), nil
}
