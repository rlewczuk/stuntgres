package parser

import (
	"fmt"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestAlterGeneratedExpressionParser(t *testing.T) {
	for _, prefix := range []string{`ALTER TABLE g`, `ALTER TABLE ONLY g`, `ALTER TABLE ONLY (g)`, `ALTER TABLE public.g *`, `ALTER TABLE IF EXISTS "g.é"`} {
		q := prefix + ` ALTER COLUMN b SET EXPRESSION AS(a+2), ALTER b DROP EXPRESSION IF EXISTS`
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		if len(statements) != 1 {
			t.Fatal(statements)
		}
		a := statements[0].(*AlterTable)
		if len(a.Actions) != 2 || a.Actions[0].Action != SetExpression || a.Actions[0].Generation == nil || a.Actions[0].Generation.Kind != "" || a.Actions[0].Column.Text != "b" || a.Actions[1].Action != DropExpression || !a.Actions[1].IfExists || a.Actions[1].Generation != nil {
			t.Fatal(a)
		}
		if a.Actions[0].Generation.Pos.Character != utf8.RuneCountInString(prefix+" ALTER COLUMN b SET ")+1 || a.Actions[0].Column.Pos.Character != utf8.RuneCountInString(prefix+" ALTER COLUMN ")+1 {
			t.Fatal("character positions lost", a)
		}
	}
	for _, tail := range []string{`SET EXPRESSION (a+2)`, `SET EXPRESSION AS a+2`, `SET EXPRESSION AS(a+2) STORED`, `SET EXPRESSION AS(a+2) VIRTUAL`, `SET EXPRESSION AS(a+2) USING a`, `DROP IF EXISTS EXPRESSION`, `SET EXPRESSION IF EXISTS AS(a+2)`, `SET EXPRESSION AS(a+2), ALTER b SET EXPRESSION AS`, `DROP EXPRESSION IF EXISTS trailing`} {
		if _, err := Parse(`ALTER TABLE g ALTER b ` + tail); err == nil {
			t.Fatal("invalid suffix admitted", tail)
		}
	}
	for _, expr := range []string{`sum(a)`, `grouping(a)`, `row_number() OVER ()`, `generate_series(1,a)`} {
		statements, err := Parse(`ALTER TABLE g ALTER b SET EXPRESSION AS(` + expr + `)`)
		if err != nil {
			t.Fatal(expr, err)
		}
		raw := statements[0].(*AlterTable).Actions[0].Generation.Expression
		if _, ok := raw.(ForbiddenGenerationExpr); !ok {
			t.Fatalf("%s lost deferred restriction: %T", expr, raw)
		}
	}
	l := &lexer{input: `UPDATE g SET expression=1;CREATE VIEW v AS(SELECT sum(a) FROM g)`, options: Options{StandardConformingStrings: true}}
	for {
		var value sqlSymType
		token := l.Lex(&value)
		if token == 0 {
			break
		}
		if token == CHECK_FUNC || token == GENERATION_OVER || l.generationDepth != 0 {
			t.Fatal("ordinary UPDATE destination leaked generation mode", value)
		}
	}
	if l.err != nil || l.generationPending || l.generationCompletedRestriction != nil {
		t.Fatal("ordinary destination retained generation scratch", l.err)
	}
}

func TestAlterGeneratedExpressionParserIsolation(t *testing.T) {
	const workers = 8
	var wg sync.WaitGroup
	failures := make(chan error, workers)
	for worker := range workers {
		wg.Go(func() {
			for range 40 {
				q := fmt.Sprintf(`/*é%d*/ ALTER TABLE g ALTER b SET EXPRESSION AS(sum(a)), ALTER c SET EXPRESSION AS(a+2)`, worker)
				statements, err := Parse(q)
				if err != nil {
					failures <- err
					return
				}
				a := statements[0].(*AlterTable)
				if _, ok := a.Actions[0].Generation.Expression.(ForbiddenGenerationExpr); !ok {
					failures <- fmt.Errorf("restriction lost")
					return
				}
				if _, bad := a.Actions[1].Generation.Expression.(ForbiddenGenerationExpr); bad {
					failures <- fmt.Errorf("restriction leaked into next action")
					return
				}
				if _, err := Parse(`ALTER TABLE g ALTER b DROP EXPRESSION`); err != nil {
					failures <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}
