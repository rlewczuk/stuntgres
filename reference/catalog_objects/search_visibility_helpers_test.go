package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type searchVisibilityClass struct {
	fn, catalog, namespace, name, ddl string
	routine, builtin                  int64
}

var searchVisibilityClasses = []searchVisibilityClass{
	{"pg_ts_config_is_visible", "pg_ts_config", "cfgnamespace", "cfgname", "TEXT SEARCH CONFIGURATION", 3758, 3748},
	{"pg_ts_dict_is_visible", "pg_ts_dict", "dictnamespace", "dictname", "TEXT SEARCH DICTIONARY", 3757, 3765},
	{"pg_ts_parser_is_visible", "pg_ts_parser", "prsnamespace", "prsname", "TEXT SEARCH PARSER", 3756, 3722},
	{"pg_ts_template_is_visible", "pg_ts_template", "tmplnamespace", "tmplname", "TEXT SEARCH TEMPLATE", 3768, 3727},
	{"pg_statistics_obj_is_visible", "pg_statistic_ext", "stxnamespace", "stxname", "STATISTICS", 3403, 0},
}

func searchVisibilityScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	for _, ns := range []string{"sv_a", "sv_b", "sv_off"} {
		fixture.SchemaReferenceCleanup(t, ns)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func searchVisibilitySetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	queries := []string{"CREATE SCHEMA sv_a", "CREATE SCHEMA sv_b", "CREATE SCHEMA sv_off", "CREATE TABLE sv_off.stats_input(x integer,y integer)"}
	for _, ns := range []string{"sv_a", "sv_b"} {
		queries = append(queries,
			searchVisibilityCreate(2, ns+".v"),
			searchVisibilityCreate(3, ns+".v"),
			searchVisibilityCreate(1, ns+".v"),
			searchVisibilityCreate(0, ns+".v"),
			searchVisibilityCreate(4, ns+".v"),
			"ALTER TEXT SEARCH CONFIGURATION "+ns+".v ADD MAPPING FOR asciiword WITH "+ns+".v")
	}
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

func searchVisibilityCreate(i int, name string) string {
	switch i {
	case 0:
		return "CREATE TEXT SEARCH CONFIGURATION " + name + " (PARSER=" + name + ")"
	case 1:
		return "CREATE TEXT SEARCH DICTIONARY " + name + " (TEMPLATE=" + name + ")"
	case 2:
		return "CREATE TEXT SEARCH PARSER " + name + " (START=pg_catalog.prsd_start,GETTOKEN=pg_catalog.prsd_nexttoken,END=pg_catalog.prsd_end,LEXTYPES=pg_catalog.prsd_lextype)"
	case 3:
		return "CREATE TEXT SEARCH TEMPLATE " + name + " (INIT=pg_catalog.dsimple_init,LEXIZE=pg_catalog.dsimple_lexize)"
	case 4:
		return "CREATE STATISTICS " + name + " (dependencies) ON x,y FROM sv_off.stats_input"
	default:
		panic("unknown search visibility fixture class")
	}
}

func searchVisibilityDiscover(t *testing.T, p *fixture.Pair, c *sql.Conn, k searchVisibilityClass, ns, name string) int64 {
	t.Helper()
	query := "SELECT o.oid::bigint FROM pg_catalog." + k.catalog + " o JOIN pg_catalog.pg_namespace n ON n.oid=o." + k.namespace + " WHERE n.nspname=$1 AND o." + k.name + "=$2"
	rows, err := c.QueryContext(p.Ctx, query, ns, name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var oid int64
	if !rows.Next() {
		t.Fatal("missing descriptor", k.fn, ns, name, rows.Err())
	}
	if err := rows.Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || oid > 4294967295 || rows.Next() || rows.Err() != nil {
		t.Fatal("invalid descriptor identity", oid, rows.Err())
	}
	return oid
}

func searchVisibilityStep(k searchVisibilityClass, arg, want any) fixture.Step {
	s := fixture.Select("SELECT pg_catalog."+k.fn+"($1)", []string{k.fn}, []string{"BOOL"}, [][]any{{want}}, arg)
	s.CheckMetadata = fixture.VisibilityBoolMetadata
	return s
}

func searchVisibilityRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, k searchVisibilityClass, stmt *sql.Stmt, arg, want any) {
	t.Helper()
	s := searchVisibilityStep(k, arg, want)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	if stmt != nil {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
	}
}

func searchVisibilityViewSQL(k searchVisibilityClass) string {
	return "SELECT n.nspname::text AS schema_name,pg_catalog." + k.fn + "(o.oid) AS visible FROM pg_catalog." + k.catalog + " o JOIN pg_catalog.pg_namespace n ON n.oid=o." + k.namespace + " WHERE n.nspname IN ('sv_a','sv_b') AND o." + k.name + "='v'"
}

func searchVisibilityStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for i, k := range searchVisibilityClasses {
		for _, q := range []string{
			fmt.Sprintf("CREATE VIEW sv_off.view_%d AS ", i) + searchVisibilityViewSQL(k),
			fmt.Sprintf("CREATE TABLE sv_off.check_%d(id oid CONSTRAINT valid_missing CHECK(pg_catalog.%s(id) IS NULL))", i, k.fn),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(fmt.Sprintf("INSERT INTO sv_off.check_%d VALUES(0)", i), 1))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW sv_off.nested AS SELECT schema_name,visible FROM sv_off.view_0", 0))
}

func searchVisibilityMatrix(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, views bool) {
	t.Helper()
	for _, reverse := range []bool{false, true} {
		path := "sv_a,sv_b"
		if reverse {
			path = "sv_b,sv_a"
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path="+path, 0))
		for i, k := range searchVisibilityClasses {
			a := searchVisibilityDiscover(t, p, c, k, "sv_a", "v")
			z := searchVisibilityDiscover(t, p, c, k, "sv_b", "v")
			if a == z {
				t.Fatal("duplicate identity")
			}
			searchVisibilityRoutes(t, p, b, c, k, nil, a, !reverse)
			searchVisibilityRoutes(t, p, b, c, k, nil, z, reverse)
			if views {
				s := fixture.Select(fmt.Sprintf("SELECT schema_name,visible FROM sv_off.view_%d ORDER BY schema_name", i), []string{"schema_name", "visible"}, []string{"TEXT", "BOOL"}, [][]any{{"sv_a", !reverse}, {"sv_b", reverse}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		}
		if views {
			s := fixture.Select("SELECT schema_name,visible FROM sv_off.nested ORDER BY schema_name", []string{"schema_name", "visible"}, []string{"TEXT", "BOOL"}, [][]any{{"sv_a", !reverse}, {"sv_b", reverse}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
	}
}
