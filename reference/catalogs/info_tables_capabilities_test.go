package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoTablesInsertability(t *testing.T) {
	itScenario(t, func(c *itCase) {
		c.exec("CREATE TABLE it_scope.base(a integer,b integer)", "CREATE TABLE it_scope.part(a integer) PARTITION BY RANGE(a)", "CREATE VIEW it_scope.simple AS SELECT a,b,a+b AS computed FROM it_scope.base", "CREATE VIEW it_scope.nested AS SELECT b FROM it_scope.simple", "CREATE VIEW it_scope.subset AS SELECT computed FROM it_scope.simple", "CREATE VIEW it_scope.constant AS SELECT 1 AS n", "CREATE VIEW it_scope.computed AS SELECT a+1 AS n FROM it_scope.base", "CREATE VIEW it_scope.joined AS SELECT a.a FROM it_scope.base a JOIN it_scope.base b ON a.a=b.a", "CREATE VIEW it_scope.aggregated AS SELECT count(*) AS n FROM it_scope.base")
		for _, tc := range []struct{ name, kind, insert string }{{"base", "BASE TABLE", "YES"}, {"part", "BASE TABLE", "YES"}, {"simple", "VIEW", "YES"}, {"nested", "VIEW", "YES"}, {"subset", "VIEW", "NO"}, {"constant", "VIEW", "NO"}, {"computed", "VIEW", "NO"}, {"joined", "VIEW", "NO"}, {"aggregated", "VIEW", "NO"}} {
			c.check(tc.name, c.row(tc.name, tc.kind, tc.insert))
		}
		held := c.hold(itSelect)
		check := func(flag string) {
			s := itStep(c.b.PG, itSelect, [][]any{c.row("constant", "VIEW", flag)}, "it_scope", "constant")
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(held, s)
		}
		check("NO")
		c.exec("CREATE RULE it_cond AS ON INSERT TO it_scope.constant WHERE NEW.n>0 DO INSTEAD NOTHING")
		check("NO")
		c.exec("DROP RULE it_cond ON it_scope.constant", "CREATE RULE it_insert AS ON INSERT TO it_scope.constant DO INSTEAD NOTHING")
		check("YES")
		c.exec("DROP RULE it_insert ON it_scope.constant")
		check("NO")
		c.exec("CREATE TABLE it_scope.noexec_base(a integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO it_scope.noexec_base VALUES(0)", 1))
		c.exec("CREATE SEQUENCE it_scope.noexec_seq", "CREATE VIEW it_scope.noexec AS SELECT 10/a AS quotient,nextval('it_scope.noexec_seq') AS tick FROM it_scope.noexec_base")
		c.check("noexec", c.row("noexec", "VIEW", "NO"))
		c.run(c.hold(itSelect), itStep(c.b.PG, itSelect, [][]any{c.row("noexec", "VIEW", "NO")}, "it_scope", "noexec"))
		c.run(c.conn, fixture.Select("SELECT nextval('it_scope.noexec_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		c.exec("CREATE ROLE it_reader", "GRANT SELECT ON it_scope.simple TO it_reader", "SET SESSION AUTHORIZATION it_reader")
		c.check("simple", c.row("simple", "VIEW", "YES"))
		c.exec("RESET SESSION AUTHORIZATION")
	})
}

var itForeignIO = errors.New("tables metadata attempted foreign I/O")

type itForeignRead struct{}

func (itForeignRead) GetForeignRelSize(context.Context, stuntgres.ForeignRequest) (stuntgres.ForeignRelSize, error) {
	return stuntgres.ForeignRelSize{}, itForeignIO
}
func (itForeignRead) GetForeignPaths(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignRelSize) ([]stuntgres.ForeignPath, error) {
	return nil, itForeignIO
}
func (itForeignRead) GetForeignPlan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPath) (stuntgres.ForeignPlan, error) {
	return stuntgres.ForeignPlan{}, itForeignIO
}
func (itForeignRead) BeginForeignScan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	return nil, itForeignIO
}

type itForeignInsert struct{ itForeignRead }

func (itForeignInsert) ExecForeignInsert(context.Context, stuntgres.ForeignModifyRequest, any, stuntgres.ForeignRow) (stuntgres.ForeignRow, bool, error) {
	return nil, false, itForeignIO
}
func itConfigureForeign(d *stuntgres.Driver) error {
	for _, tc := range []struct {
		name    string
		wrapper stuntgres.ForeignDataWrapper
	}{{"read", itForeignRead{}}, {"insert", itForeignInsert{}}} {
		if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "it_" + tc.name}, Wrapper: tc.wrapper}); err != nil {
			return err
		}
	}
	return nil
}
func TestReferenceInfoTablesForeign(t *testing.T) {
	itConfiguredScenario(t, itConfigureForeign, func(c *itCase) {
		if c.b.PG {
			c.exec("CREATE EXTENSION postgres_fdw", "CREATE SERVER it_live FOREIGN DATA WRAPPER postgres_fdw OPTIONS(host '127.0.0.1',port '1',dbname 'stuntgres_reference')", "CREATE USER MAPPING FOR CURRENT_USER SERVER it_live OPTIONS(user 'stuntgres',password 'stuntgres')")
		}
		for _, tc := range []struct{ name, option, flag string }{{"read", "false", "NO"}, {"insert", "true", "YES"}} {
			if c.b.PG {
				c.exec("CREATE FOREIGN TABLE it_scope." + tc.name + "(a integer) SERVER it_live OPTIONS(schema_name 'public',table_name 'absent_remote',updatable '" + tc.option + "')")
			} else {
				c.exec("CREATE FOREIGN DATA WRAPPER it_"+tc.name+" HANDLER public.it_"+tc.name, "CREATE SERVER it_"+tc.name+" FOREIGN DATA WRAPPER it_"+tc.name)
				c.exec("CREATE FOREIGN TABLE it_scope." + tc.name + "(a integer) SERVER it_" + tc.name)
			}
			c.check(tc.name, c.row(tc.name, "FOREIGN", tc.flag))
			c.run(c.hold(itSelect), itStep(c.b.PG, itSelect, [][]any{c.row(tc.name, "FOREIGN", tc.flag)}, "it_scope", tc.name))
		}
		c.bag(c.row("read", "FOREIGN", "NO"), c.row("insert", "FOREIGN", "YES"))
	})
}
