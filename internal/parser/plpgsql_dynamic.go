package parser

import (
	"reflect"
	"slices"
)

// PLDynamicStatement retains only procedural syntax; computed commands are
// parsed by the invocation that reaches this statement.
type PLDynamicStatement struct {
	ReturnQuery                              bool
	Start                                    Position
	Command                                  PLExpression
	Using                                    []PLExpression
	Targets                                  []Expr
	Separators                               []Position
	Strict                                   bool
	IntoStart, TargetsStart, TargetsEnd, End Position
	admitted                                 *plDynamicAdmission
}

type plDynamicAdmission struct {
	mode                        bool
	command                     PLExpression
	using                       []PLExpression
	targets                     []Expr
	separators                  []Position
	strict                      bool
	into, start, end, delimiter Position
	head                        Position
}

func (q *PLDynamicStatement) finishDynamic(end Position) {
	q.End = end
	q.admitted = &plDynamicAdmission{q.ReturnQuery, q.Command, slices.Clone(q.Using), slices.Clone(q.Targets), slices.Clone(q.Separators), q.Strict, q.IntoStart, q.TargetsStart, q.TargetsEnd, end, q.Start}
}

func ValidPLDynamicStatement(q *PLDynamicStatement) bool {
	if q == nil || q.admitted == nil {
		return false
	}
	a := q.admitted
	return q.Start == a.head && q.ReturnQuery == a.mode && reflect.DeepEqual(q.Command, a.command) && reflect.DeepEqual(q.Using, a.using) && reflect.DeepEqual(q.Targets, a.targets) && slices.Equal(q.Separators, a.separators) && q.Strict == a.strict && q.IntoStart == a.into && q.TargetsStart == a.start && q.TargetsEnd == a.end && q.End == a.delimiter
}

func (l *lexer) plDynamicInto(q *PLDynamicStatement, into lexeme, targets *PLForeach, strict bool) *PLDynamicStatement {
	if q.IntoStart.Character != 0 {
		l.syntax(into)
		return q
	}
	probe := lexer{input: l.input, offset: into.pos.Offset, positionOffset: into.pos.Offset, positionCharacters: into.pos.Character - 1, options: l.options}
	probe.nextRaw()
	read, _ := l.plReadIntoTargets(&probe, probe.nextRaw(), q.Start, into.pos)
	if l.err != nil {
		return q
	}
	if read.Strict != strict || !reflect.DeepEqual(read.Targets, targets.Targets) || !slices.Equal(read.Separators, targets.TargetCommas) {
		l.syntax(into)
		return q
	}
	q.IntoStart, q.TargetsStart, q.TargetsEnd = into.pos, read.TargetsStart, read.TargetsEnd
	q.Targets, q.Separators, q.Strict = read.Targets, read.Separators, strict
	return q
}

func (l *lexer) plDynamicUsing(q *PLDynamicStatement, using lexeme, values []PLExpression) *PLDynamicStatement {
	values[0] = l.plBoundExpression(values[0], using)
	if len(q.Using) != 0 {
		// A repeated USING leaves an extra operand after the first expression
		// list. Its syntax diagnostic belongs to that operand, not the later
		// delimiter used to finish reading the clause.
		start := values[0].Start
		probe := lexer{input: l.input, offset: start.Offset, positionOffset: start.Offset, positionCharacters: start.Character - 1, options: l.options}
		token := probe.nextRaw()
		if probe.err != nil {
			l.err = probe.err
		} else {
			l.syntax(token.lexeme)
		}
		l.plBoundaryHit = 0
		return q
	}
	q.Using = values
	return q
}
