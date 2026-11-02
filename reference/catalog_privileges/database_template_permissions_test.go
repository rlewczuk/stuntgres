package reference_test

import (
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseTemplatePermissions(t *testing.T) {
	for _,tc:=range []struct{name,grant string; allowed bool}{
		{"owner","",true},
		{"inherited","GRANT dc_owner TO dc_actor WITH INHERIT TRUE, SET FALSE",true},
		{"set_only","GRANT dc_owner TO dc_actor WITH INHERIT FALSE, SET TRUE",false},
		{"unrelated","",false},
	} {
		t.Run(tc.name,func(t *testing.T){databaseCreateScenario(t,func(c *databaseCreateCase){
			c.run(fixture.Exec("CREATE ROLE dc_owner",0));c.run(fixture.Exec("CREATE ROLE dc_actor CREATEDB",0))
			owner:="dc_owner";if tc.name=="owner" {owner="dc_actor"}
			c.create("dt_permission_source","OWNER "+owner,false)
			if tc.grant!="" {c.run(fixture.Exec(tc.grant,0))}
			c.run(fixture.Exec("GRANT CONNECT,CREATE ON DATABASE dt_permission_source TO dc_actor",0))
			c.run(fixture.Exec("SET ROLE dc_actor",0))
			if tc.allowed {c.create("dt_permission_clone","TEMPLATE dt_permission_source",true)} else {
				c.remember("dt_permission_denied");c.error("CREATE DATABASE dt_permission_denied TEMPLATE dt_permission_source","42501",`permission denied to copy database "dt_permission_source"`);c.absent("dt_permission_denied")
				if tc.name=="set_only" {c.error("CREATE DATABASE dt_permission_denied TEMPLATE dt_permission_source OWNER dc_owner","42501",`permission denied to copy database "dt_permission_source"`)}
			}
			c.run(fixture.Exec("RESET ROLE",0))
		})})
	}
	t.Run("public_template",func(t *testing.T){databaseCreateScenario(t,func(c *databaseCreateCase){
		c.run(fixture.Exec("CREATE ROLE dc_actor CREATEDB",0));c.run(fixture.Exec("CREATE ROLE dc_denied",0))
		c.create("dt_public_source","IS_TEMPLATE true ALLOW_CONNECTIONS false",false)
		c.run(fixture.Exec("SET ROLE dc_actor",0));c.create("dt_public_clone","TEMPLATE dt_public_source IS_TEMPLATE true",true);c.run(fixture.Exec("RESET ROLE",0))
		c.run(fixture.Exec("SET ROLE dc_denied",0));c.remember("dt_public_denied");c.error("CREATE DATABASE dt_public_denied TEMPLATE dt_public_source","42501","permission denied to create database");c.absent("dt_public_denied");c.run(fixture.Exec("RESET ROLE",0))
	})})
}
