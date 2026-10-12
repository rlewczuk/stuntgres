package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeExprFields(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,'(7,z)','("(8,y)",9)'),(2,NULL,NULL),(3,'(,)','("(,)",)')`, 3))
		compositeExprBoth(t, p, b, c, `SELECT (v).i,(h.v).t,(w).p.i FROM holder h ORDER BY k`, []string{"i", "t", "i"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "z", int64(8)}, {nil, nil, nil}, {nil, nil, nil}})
		compositeExprBoth(t, p, b, c, `SELECT (ROW(7,'z'::text)).f1,(ROW(7,'z'::text)).f2`, []string{"f1", "f2"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "z"}})
		compositeExprBoth(t, p, b, c, `SELECT ($1::pair).i AS i,($1::pair).t AS t`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "z"}}, "(7,z)")
		stmt, err := c.PrepareContext(p.Ctx, `SELECT ($1::pair).i AS i,($1::pair).t AS t`)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, x := range []struct {
			arg  any
			want []any
		}{{"(7,z)", []any{int64(7), "z"}}, {[]byte("(9,y)"), []any{int64(9), "y"}}, {nil, []any{nil, nil}}, {"(11,recovered)", []any{int64(11), "recovered"}}} {
			rows, err := stmt.QueryContext(p.Ctx, x.arg)
			if err != nil {
				t.Fatal(err)
			}
			cols, err := rows.Columns()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cols, []string{"i", "t"}) {
				rows.Close()
				t.Fatal(cols)
			}
			if !rows.Next() {
				rows.Close()
				t.Fatal("held field cursor empty")
			}
			var i, txt any
			if err := rows.Scan(&i, &txt); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			got := []any{fixture.Normalize(i), fixture.Normalize(txt)}
			if !reflect.DeepEqual(got, x.want) {
				t.Errorf("held fields got %#v want %#v", got, x.want)
			}
			if rows.Next() {
				t.Error("held fields returned extra row")
			}
			if err := rows.Err(); err != nil {
				t.Error(err)
			}
			rows.Close()
		}
		fixture.CompositeReferenceSetup(t, p, b, c, `PREPARE field_param(pair) AS SELECT $1.i AS i`)
		compositeExprSelect(t, p, b, c, fixture.Select(`EXECUTE field_param('(12,formal)')`, []string{"i"}, []string{"INT4"}, [][]any{{int64(12)}}))
		compositeExprSelect(t, p, b, c, fixture.Select(`EXECUTE field_param(NULL)`, []string{"i"}, []string{"INT4"}, [][]any{{nil}}))
		fixture.CompositeReferenceSetup(t, p, b, c, `DEALLOCATE field_param`, `CREATE DOMAIN domain_pair AS pair`)
		compositeExprBoth(t, p, b, c, `SELECT (ROW(5,'domain')::domain_pair).i AS i`, []string{"i"}, []string{"INT4"}, [][]any{{int64(5)}})
	})
}

func TestReferenceCompositeExprScopeAmbiguity(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,'(7,z)',NULL)`, 1))
		compositeExprBoth(t, p, b, c, `SELECT (v).i,(h.v).i FROM holder h`, []string{"i", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}})
		compositeExprError(t, p, c, `SELECT v.i FROM holder`, "42P01", `missing FROM-clause entry for table "v"`, "v.i")
		compositeExprError(t, p, c, `SELECT h.v.i FROM holder h`, "42P01", `missing FROM-clause entry for table "v"`, "h.v.i")
		q := `SELECT holder.k FROM holder h`
		d := fixture.CompositeReferenceDiagnostic("42P01", `invalid reference to FROM-clause entry for table "holder"`)
		d.Hint = `Perhaps you meant to reference the table alias "h".`
		d.Position = compositeExprPosition(q, "holder.k")
		fixture.CompositeReferenceError(t, p, c, q, "prepare", d)
		compositeExprError(t, p, c, `SELECT (v).i FROM holder a CROSS JOIN holder b`, "42702", `column reference "v" is ambiguous`, "v")
		compositeExprBoth(t, p, b, c, `SELECT (d).* FROM (VALUES (1,'x'::text),(2,NULL::text)) d(i,t) ORDER BY i`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), nil}})
		compositeExprBoth(t, p, b, c, `SELECT (d).* FROM (SELECT k,(v).i AS i FROM holder) d`, []string{"k", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}})
		compositeExprBoth(t, p, b, c, `SELECT (j).* FROM (holder a JOIN (VALUES (1)) b(x) ON a.k=b.x) j`, []string{"k", "v", "w", "x"}, []string{"INT4", "", "", "INT4"}, [][]any{{int64(1), "(7,z)", nil, int64(1)}})
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE shadow(h integer,i integer)`, `CREATE VIEW saved_holder AS SELECT k,v FROM holder`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO shadow VALUES(6,8)`, 1))
		compositeExprBoth(t, p, b, c, `SELECT h,h.* FROM shadow h`, []string{"h", "h", "i"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(6), int64(6), int64(8)}})
		compositeExprBoth(t, p, b, c, `SELECT (s).* FROM saved_holder s`, []string{"k", "v"}, []string{"INT4", ""}, [][]any{{int64(1), "(7,z)"}})
		compositeExprBoth(t, p, b, c, `SELECT (SELECT (h.v).i) AS i FROM holder h`, []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}})
		q = `SELECT (SELECT h.v FROM (VALUES (1)) h(x)) FROM holder h`
		d = fixture.CompositeReferenceDiagnostic("42703", `column h.v does not exist`)
		d.Detail = `There is a column named "v" in table "h", but it cannot be referenced from this part of the query.`
		d.Position = compositeExprPosition(q, "h.v")
		fixture.CompositeReferenceError(t, p, c, q, "prepare", d)
		q = `SELECT d.i FROM holder h,(SELECT (h.v).i) d`
		d = fixture.CompositeReferenceDiagnostic("42P01", `invalid reference to FROM-clause entry for table "h"`)
		d.Detail = `There is an entry for table "h", but it cannot be referenced from this part of the query.`
		d.Hint = `To reference that table, you must mark this subquery with LATERAL.`
		d.Position = compositeExprPosition(q, "h.v")
		fixture.CompositeReferenceError(t, p, c, q, "prepare", d)
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE quoted_pair AS ("Mixed Field" integer,"Text" text)`)
		compositeExprBoth(t, p, b, c, `SELECT (ROW(4,'quoted')::quoted_pair)."Mixed Field"`, []string{"Mixed Field"}, []string{"INT4"}, [][]any{{int64(4)}})
		compositeExprBoth(t, p, b, c, `SELECT (h).id,(h).renamed.i FROM holder h(id,renamed,nested)`, []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}})
		compositeExprError(t, p, c, `SELECT (h).x FROM holder h(x,x,z)`, "42702", `column reference "x" is ambiguous`, "h")
	})
}

func TestReferenceCompositeExprFunctionalNotation(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,'(7,z)',NULL),(2,NULL,NULL)`, 2))
		compositeExprBoth(t, p, b, c, `SELECT i(v),(v).i,t(v) FROM holder ORDER BY k`, []string{"i", "i", "t"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(7), int64(7), "z"}, {nil, nil, nil}})
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE collision AS (i integer,t text,num_nulls integer)`, `CREATE TABLE collisions(v collision)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO collisions VALUES ('(1,x,7)')`, 1))
		compositeExprBoth(t, p, b, c, `SELECT num_nulls(v) AS builtin,(v).num_nulls AS field FROM collisions`, []string{"builtin", "field"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(7)}})
		compositeExprBoth(t, p, b, c, `SELECT (v).num_nulls FROM holder ORDER BY k`, []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}})
		compositeExprBoth(t, p, b, c, `SELECT (1).num_nulls AS n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}})
		compositeExprBoth(t, p, b, c, `SELECT id(h),renamed(h) FROM holder h(id,renamed,nested) ORDER BY id`, []string{"id", "renamed"}, []string{"INT4", ""}, [][]any{{int64(1), "(7,z)"}, {int64(2), nil}})
		compositeExprError(t, p, c, `SELECT x(h) FROM holder h(x,x,z)`, "42702", `column reference "x" is ambiguous`, "x(")
		// A known provider's own no-matching signature permits field projection;
		// an error in its argument remains that child's diagnostic.
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE abs_pair AS (abs integer)`, `CREATE TABLE abs_values(v abs_pair)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO abs_values VALUES ('(11)'),(NULL)`, 2))
		compositeExprBoth(t, p, b, c, `SELECT abs(v),(v).abs FROM abs_values ORDER BY (v).abs`, []string{"abs", "abs"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(11)}, {nil, nil}})
		q := `SELECT abs(ROW(missing(v))::abs_pair) FROM holder`
		childDiagnostic := fixture.CompositeReferenceDiagnostic("42883", `function missing(pair) does not exist`)
		childDiagnostic.Hint = `No function matches the given name and argument types. You might need to add explicit type casts.`
		childDiagnostic.Position = compositeExprPosition(q, "missing")
		fixture.CompositeReferenceError(t, p, c, q, "prepare", childDiagnostic)
		for _, q := range []string{`SELECT missing(v) FROM holder`, `SELECT public.i(v) FROM holder`, `SELECT i(v,v) FROM holder`, `SELECT i(v => v) FROM holder`} {
			// Full signature/Hint/Position are deliberately asserted, not learned from
			// native errors. Pinned oracle checkpoint settles decoration spelling.
			message := `function missing(pair) does not exist`
			base := "missing"
			if q == `SELECT public.i(v) FROM holder` {
				message = `function public.i(pair) does not exist`
				base = "public.i"
			}
			if q == `SELECT i(v,v) FROM holder` {
				message = `function i(pair, pair) does not exist`
				base = "i("
			}
			if q == `SELECT i(v => v) FROM holder` {
				message = `function i(v => pair) does not exist`
				base = "i("
			}
			d := fixture.CompositeReferenceDiagnostic("42883", message)
			d.Hint = `No function matches the given name and argument types. You might need to add explicit type casts.`
			d.Position = compositeExprPosition(q, base)
			fixture.CompositeReferenceError(t, p, c, q, "prepare", d)
		}
		for _, q := range []string{`SELECT i(v) FILTER (WHERE true) FROM holder`, `SELECT i(v) OVER () FROM holder`} {
			d := fixture.CompositeReferenceDiagnostic("42883", `function i(pair) does not exist`)
			d.Hint = `No function matches the given name and argument types. You might need to add explicit type casts.`
			d.Position = compositeExprPosition(q, "i(")
			fixture.CompositeReferenceError(t, p, c, q, "prepare", d)
		}
	})
}

func TestReferenceCompositeExprDiagnostics(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		for _, x := range []struct{ q, code, message, base string }{
			{`SELECT (v).missing FROM holder`, "42703", `column "missing" not found in data type pair`, "v"},
			{`SELECT /*é*/ (v).missing FROM holder`, "42703", `column "missing" not found in data type pair`, "v"},
			{`SELECT (ROW(1,'x')).missing`, "42703", `could not identify column "missing" in record data type`, "ROW"},
			{`SELECT (1).missing`, "42809", `column notation .missing applied to type integer, which is not a composite type`, "1"},
			{`SELECT (1).*`, "42809", `type integer is not composite`, ""},
			{`SELECT num_nulls((h).*) FROM holder h`, "0A000", `row expansion via "*" is not supported here`, "h"},
		} {
			t.Run(x.q, func(t *testing.T) { compositeExprError(t, p, c, x.q, x.code, x.message, x.base) })
		}
		q := `SELECT $1.i`
		d := fixture.CompositeReferenceDiagnostic("42809", `column notation .i applied to type unknown, which is not a composite type`)
		d.Position = compositeExprPosition(q, "$1")
		// A supplied argument chooses pq extended protocol on the direct route too;
		// no-argument simple-protocol parameter absence is not this feature.
		fixture.CompositeReferenceError(t, p, c, q, "prepare", d, "scalar")
	})
}
