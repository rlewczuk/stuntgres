package reference_test

import (
	_ "embed"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Verbatim copy of the upstream coder migration; provenance:
// grind/coder/coder/coderd/database/migrations/000297_notifications_inbox.up.sql
//
//go:embed testdata/000297_notifications_inbox.up.sql
var btreeArrayMigration297 string

// TestReferenceBtreeArrayMigrations replays the exact coder migration that
// adds the multicolumn (uuid, uuid, uuid[]) btree index. Only the referenced
// users and notification_templates tables are created as prerequisites.
func TestReferenceBtreeArrayMigrations(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		_, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE users(id uuid PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE notification_templates(id uuid PRIMARY KEY)", 0))
		for _, statement := range strings.Split(btreeArrayMigration297, ";") {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			run(fixture.Exec(statement, 0))
		}

		run(fixture.Select("SELECT enumlabel FROM pg_enum WHERE enumtypid='inbox_notification_read_status'::regtype ORDER BY enumsortorder", []string{"enumlabel"}, []string{"NAME"}, [][]any{{"all"}, {"unread"}, {"read"}}))
		run(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='inbox_notifications' ORDER BY indexname", []string{"indexname"}, []string{"NAME"}, [][]any{
			{"idx_inbox_notifications_user_id_read_at"},
			{"idx_inbox_notifications_user_id_template_id_targets"},
			{"inbox_notifications_pkey"},
		}))
		run(fixture.Select("SELECT indexdef FROM pg_indexes WHERE indexname='idx_inbox_notifications_user_id_template_id_targets'", []string{"indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX idx_inbox_notifications_user_id_template_id_targets ON public.inbox_notifications USING btree (user_id, template_id, targets)"}}))
		run(fixture.Select("SELECT indclass::text FROM pg_index WHERE indexrelid='idx_inbox_notifications_user_id_template_id_targets'::regclass", []string{"indclass"}, []string{"TEXT"}, [][]any{{"10065 10065 10000"}}))

		run(fixture.Exec("INSERT INTO users(id) VALUES('00000000-0000-0000-0000-000000000001')", 1))
		run(fixture.Exec("INSERT INTO notification_templates(id) VALUES('00000000-0000-0000-0000-000000000002')", 1))
		run(fixture.Exec(`INSERT INTO inbox_notifications(id,user_id,template_id,targets,title,content,icon,actions) VALUES(
			'00000000-0000-0000-0000-000000000010',
			'00000000-0000-0000-0000-000000000001',
			'00000000-0000-0000-0000-000000000002',
			ARRAY['00000000-0000-0000-0000-00000000000a'::uuid,'00000000-0000-0000-0000-00000000000b'::uuid],
			'title','content','icon','{}')`, 1))
		run(fixture.Select("SELECT id FROM inbox_notifications ORDER BY targets", []string{"id"}, []string{"UUID"}, [][]any{{"00000000-0000-0000-0000-000000000010"}}))
		run(fixture.Select("SELECT id FROM inbox_notifications WHERE targets = ARRAY['00000000-0000-0000-0000-00000000000a'::uuid,'00000000-0000-0000-0000-00000000000b'::uuid]", []string{"id"}, []string{"UUID"}, [][]any{{"00000000-0000-0000-0000-000000000010"}}))
	})
}