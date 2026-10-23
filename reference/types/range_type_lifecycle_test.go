package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeLifecycle(t *testing.T) {
	for _, kind := range []string{"class", "subtype", "collation", "companion", "table"} {
		t.Run(kind, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "range_type_other")
			rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE SCHEMA range_type_other`, 0))
				run(fixture.Exec(rangeTypeClass("range_life_ops", "btint4cmp"), 0))
				run(fixture.Exec(`CREATE TYPE range_life_endpoint AS ENUM('z','a')`, 0))
				run(fixture.Exec(`CREATE COLLATION public.range_life_collation FROM pg_catalog."C"`, 0))
				options := `SUBTYPE=int4,SUBTYPE_OPCLASS=public.range_life_ops`
				if kind == "subtype" {
					options = `SUBTYPE=public.range_life_endpoint`
				}
				if kind == "collation" {
					options = `SUBTYPE=text,COLLATION=public.range_life_collation`
				}
				run(fixture.Exec(`CREATE TYPE public.range_life AS RANGE(`+options+`,MULTIRANGE_TYPE_NAME=range_type_other.range_life_many)`, 0))
				if kind == "table" {
					run(fixture.Exec(`CREATE TABLE range_life_data(r range_life,m range_type_other.range_life_many)`, 0))
					run(fixture.Exec(`INSERT INTO range_life_data VALUES('[1,3]','{[1,3]}')`, 1))
				}
				q := `DROP OPERATOR CLASS public.range_life_ops USING btree`
				want := fixture.Diagnostic{Code: "2BP01", Message: `cannot drop operator class range_life_ops for access method btree because other objects depend on it`, Detail: `type range_life depends on operator class range_life_ops for access method btree`, Hint: `Use DROP ... CASCADE to drop the dependent objects too.`}
				switch kind {
				case "subtype":
					q = `DROP TYPE public.range_life_endpoint`
					want.Message = `cannot drop type range_life_endpoint because other objects depend on it`
					want.Detail = `type range_life depends on type range_life_endpoint`
				case "collation":
					q = `DROP COLLATION public.range_life_collation`
					want.Message = `cannot drop collation range_life_collation because other objects depend on it`
					want.Detail = `type range_life depends on collation range_life_collation`
				case "companion":
					q = `DROP TYPE range_type_other.range_life_many`
					want.Message = `cannot drop type range_type_other.range_life_many because type range_life requires it`
					want.Detail = ""
					want.Hint = `You can drop type range_life instead.`
				case "table":
					q = `DROP TYPE public.range_life`
					want.Message = `cannot drop type range_life because other objects depend on it`
					want.Detail = "column m of table range_life_data depends on type range_type_other.range_life_many\ncolumn r of table range_life_data depends on type range_life"
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
				if kind == "companion" {
					q = `DROP TYPE public.range_life`
				}
				run(fixture.Exec(q+` CASCADE`, 0))
				run(fixture.Select(`SELECT typname FROM pg_type WHERE typnamespace IN ('public'::regnamespace,'range_type_other'::regnamespace) AND typname IN ('range_life','_range_life','range_life_many','_range_life_many') ORDER BY typname`, []string{"typname"}, []string{"NAME"}, nil))
				if kind == "table" {
					run(fixture.Select(`SELECT attname FROM pg_attribute WHERE attrelid='range_life_data'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum`, []string{"attname"}, []string{"NAME"}, nil))
				}
			})
		})
	}
}
