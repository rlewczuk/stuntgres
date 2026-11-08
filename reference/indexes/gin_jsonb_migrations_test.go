package reference_test

import (
	_ "embed"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Verbatim copies of the upstream coder migrations; provenance:
// grind/coder/coder/coderd/database/migrations/000451_chat_labels.up.sql and
// grind/coder/coder/coderd/database/migrations/000623_chat_project_sharing.up.sql
//
//go:embed testdata/000451_chat_labels.up.sql
var ginJSONBmigration451 string

//go:embed testdata/000623_chat_project_sharing.up.sql
var ginJSONBmigration623 string

// TestReferenceGINJSONBMigrations applies the exact coder migrations that add
// jsonb columns with default jsonb_ops GIN indexes. Only objects referenced by
// those statements are created as prerequisites.
func TestReferenceGINJSONBMigrations(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		_, run := classConn(t, p, b)
		execMigration := func(migration string) {
			t.Helper()
			for _, statement := range strings.Split(migration, ";") {
				statement = strings.TrimSpace(statement)
				if statement == "" {
					continue
				}
				run(fixture.Exec(statement, 0))
			}
		}
		run(fixture.Exec("CREATE TABLE chats(id integer)", 0))
		run(fixture.Exec("CREATE TYPE api_key_scope AS ENUM ('read','write')", 0))
		run(fixture.Exec("CREATE TABLE chat_projects(id integer)", 0))

		execMigration(ginJSONBmigration451)
		run(fixture.Exec(`INSERT INTO chats(id,labels) VALUES(1,'{"a":"b"}'),(2,'{}')`, 2))
		run(ginIDs(`SELECT id FROM chats WHERE labels @> '{"a":"b"}' ORDER BY id`, []int64{1}))
		run(fixture.Select("SELECT indexdef FROM pg_indexes WHERE tablename='chats' AND indexname='idx_chats_labels'", []string{"indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX idx_chats_labels ON public.chats USING gin (labels)"}}))
		run(fixture.Select("SELECT indclass::text FROM pg_index WHERE indexrelid='idx_chats_labels'::regclass", []string{"indclass"}, []string{"TEXT"}, [][]any{{"10090"}}))

		execMigration(ginJSONBmigration623)
		run(fixture.Exec(`INSERT INTO chat_projects(id,user_acl,group_acl) VALUES(1,'{"u":1}','{}')`, 1))
		run(ginIDs(`SELECT id FROM chat_projects WHERE user_acl @> '{"u":1}' ORDER BY id`, []int64{1}))
		run(ginIDs("SELECT id FROM chat_projects WHERE group_acl ?& '{}'::text[] ORDER BY id", []int64{1}))
		run(fixture.Select("SELECT indexname,indexdef FROM pg_indexes WHERE tablename='chat_projects' ORDER BY indexname", []string{"indexname", "indexdef"}, []string{"NAME", "TEXT"}, [][]any{
			{"idx_chat_projects_group_acl", "CREATE INDEX idx_chat_projects_group_acl ON public.chat_projects USING gin (group_acl)"},
			{"idx_chat_projects_user_acl", "CREATE INDEX idx_chat_projects_user_acl ON public.chat_projects USING gin (user_acl)"},
		}))
		run(fixture.Select("SELECT c.relname,i.indclass::text FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='chat_projects'::regclass ORDER BY c.relname", []string{"relname", "indclass"}, []string{"NAME", "TEXT"}, [][]any{
			{"idx_chat_projects_group_acl", "10090"},
			{"idx_chat_projects_user_acl", "10090"},
		}))
	})
}