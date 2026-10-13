package reference_test

import (
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceConversionOwnership(t *testing.T) {
	dsn := "reference/" + t.Name()
	fixture.RoleReferenceCleanup(t, []string{"conv_owner", "conv_other"})
	conversionScenario(t, []string{"conv_owner_schema", "conv_other_schema", "conv_usage_hidden"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conversionExec(t, p, b, `CREATE USER conv_owner`, `CREATE USER conv_other`, `CREATE SCHEMA conv_owner_schema AUTHORIZATION conv_owner`, `CREATE SCHEMA conv_other_schema AUTHORIZATION conv_other`, `CREATE SCHEMA conv_usage_hidden`)
		if b.PG {
			conversionExec(t, p, b, `ALTER ROLE conv_owner PASSWORD 'role-core-fixture'`, `ALTER ROLE conv_other PASSWORD 'role-core-fixture'`)
		}
		ownerDB := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, dsn, "conv_owner")
		otherDB := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, dsn, "conv_other")
		owner, other := b, b
		owner.DB, other.DB = ownerDB, otherDB
		conversionExec(t, p, owner, conversionCreate("conv_owner_schema.conv_owned", false))
		oid := conversionOID(t, p, b, "conv_owned")
		conversionStep(t, p, b, fixture.Select(`SELECT c.conowner=r.oid AS owned FROM pg_catalog.pg_conversion c JOIN pg_catalog.pg_roles r ON r.rolname='conv_owner' WHERE c.oid=$1`, []string{"owned"}, []string{"BOOL"}, [][]any{{true}}, oid))
		conversionError(t, p, other, conversionCreate("conv_owner_schema.conv_rejected", false), conversionDiagnostic("42501", `permission denied for schema conv_owner_schema`), false)
		if b.PG {
			conversionExec(t, p, b, `GRANT USAGE ON SCHEMA conv_owner_schema TO conv_other`)
		} else {
			// Native has no public schema ACL SQL: schema ownership supplies
			// reachability without changing the conversion's independent owner.
			conversionExec(t, p, b, `ALTER SCHEMA conv_owner_schema OWNER TO conv_other`)
		}
		// Rename/schema/owner alteration report the stored conname; DROP's
		// generic object-address ownership check reports the supplied name.
		// These source paths do not select diagnostic spelling by protocol.
		operations := []struct{ name, query, object string }{
			{"rename", `ALTER CONVERSION conv_owner_schema.conv_owned RENAME TO stolen`, "conv_owned"},
			{"set_schema", `ALTER CONVERSION conv_owner_schema.conv_owned SET SCHEMA conv_other_schema`, "conv_owned"},
			{"owner", `ALTER CONVERSION conv_owner_schema.conv_owned OWNER TO conv_other`, "conv_owned"},
			{"drop", `DROP CONVERSION conv_owner_schema.conv_owned`, "conv_owner_schema.conv_owned"},
		}
		for _, op := range operations {
			t.Run("nonowner_"+op.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					route := "direct"
					if prepared {
						route = "prepared"
					}
					t.Run(route, func(t *testing.T) {
						step := fixture.ErrorDiagnostic(op.query, conversionDiagnostic("42501", "must be owner of conversion "+op.object))
						step.Prepared, step.ErrorStage = prepared, "execute"
						conversionStep(t, p, other, step)
					})
				}
			})
		}
		if !b.PG {
			conversionExec(t, p, b, `ALTER SCHEMA conv_owner_schema OWNER TO conv_owner`)
		}
		conversionError(t, p, owner, `ALTER CONVERSION conv_owner_schema.conv_owned SET SCHEMA conv_other_schema`, conversionDiagnostic("42501", `permission denied for schema conv_other_schema`), false)
		conversionError(t, p, owner, `ALTER CONVERSION conv_owner_schema.conv_owned OWNER TO conv_other`, conversionDiagnostic("42501", `must be able to SET ROLE "conv_other"`), false)
		conversionExec(t, p, owner, `ALTER CONVERSION conv_owner_schema.conv_owned OWNER TO CURRENT_ROLE`, `ALTER CONVERSION conv_owner_schema.conv_owned OWNER TO CURRENT_USER`, `ALTER CONVERSION conv_owner_schema.conv_owned OWNER TO SESSION_USER`)
		if b.PG {
			conversionExec(t, p, b, `GRANT CREATE ON SCHEMA conv_owner_schema TO conv_other`)
		} else {
			conversionExec(t, p, b, `ALTER SCHEMA conv_owner_schema OWNER TO conv_other`)
		}
		conversionExec(t, p, b, `ALTER CONVERSION conv_owner_schema.conv_owned OWNER TO conv_other`)
		conversionStep(t, p, b, fixture.Select(`SELECT c.oid::bigint AS oid,c.conowner=r.oid AS owned FROM pg_catalog.pg_conversion c JOIN pg_catalog.pg_roles r ON r.rolname='conv_other' WHERE c.oid=$1`, []string{"oid", "owned"}, []string{"INT8", "BOOL"}, [][]any{{oid, true}}, oid))
		// Isolate the role dependency so Detail asserts SQL semantics, not
		// unrelated schema/ACL dependency-loader order.
		conversionExec(t, p, b, `DROP SCHEMA conv_other_schema`)
		if b.PG {
			conversionExec(t, p, b, `REVOKE USAGE,CREATE ON SCHEMA conv_owner_schema FROM conv_other`)
		} else {
			conversionExec(t, p, b, `ALTER SCHEMA conv_owner_schema OWNER TO conv_owner`)
		}
		conversionError(t, p, b, `DROP ROLE conv_other`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: `role "conv_other" cannot be dropped because some objects depend on it`, Detail: "owner of conversion conv_owner_schema.conv_owned"}, false)
		// Schema dependencies and their full context are independently expected.
		conversionError(t, p, b, `DROP SCHEMA conv_owner_schema RESTRICT`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: `cannot drop schema conv_owner_schema because other objects depend on it`, Detail: "conversion conv_owner_schema.conv_owned depends on schema conv_owner_schema", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}, false)
		conversionExec(t, p, b, `DROP SCHEMA conv_owner_schema CASCADE`)
		conversionStep(t, p, b, conversionCount("conv_owned", 0))
		conversionStep(t, p, b, fixture.Select(`SELECT proname FROM pg_catalog.pg_proc WHERE oid=4375`, []string{"proname"}, []string{"NAME"}, [][]any{{"utf8_to_iso8859_1"}}))
		conversionExec(t, p, b, `CREATE SCHEMA conv_other_schema AUTHORIZATION conv_other`)
		// Effective schema USAGE, not just literal search_path, gates defaults.
		conversionHideSeed(t, p, b, "conv_usage_hidden")
		other.DB.SetMaxOpenConns(1)
		conversionExec(t, p, other, `SET search_path=conv_usage_hidden,public,pg_catalog`)
		conversionError(t, p, other, conversionValue().Query, conversionMissingDefault(), false)
		if b.PG {
			conversionExec(t, p, b, `GRANT USAGE ON SCHEMA conv_usage_hidden TO conv_other`)
		} else {
			conversionExec(t, p, b, `ALTER SCHEMA conv_usage_hidden OWNER TO conv_other`)
		}
		// Authenticated login pools are deliberately unwrapped. Consume their
		// actual public cursor rather than borrowing the admin middleware recorder.
		value := conversionValue()
		rows, err := other.DB.QueryContext(p.Ctx, value.Query)
		fixture.DeleteReferenceConsume(t, b.PG, rows, err, value)
		// The oracle-only ACL setup exercises a real handler, not a fake routine.
		// Native EXECUTE denial belongs at its actual internal authorization seam.
		if b.PG {
			t.Cleanup(func() {
				_, _ = b.DB.Exec(`GRANT EXECUTE ON FUNCTION pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean) TO PUBLIC`)
			})
			conversionExec(t, p, b, `REVOKE EXECUTE ON FUNCTION pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean) FROM PUBLIC`)
			conversionError(t, p, other, conversionCreate("conv_other_schema.conv_noexecute", false), conversionDiagnostic("42501", `permission denied for function pg_catalog.utf8_to_iso8859_1`), false)
		}
	})
}
