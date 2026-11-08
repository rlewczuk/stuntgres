package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Final comment literals produced by the 25 named coder migrations.
const (
	cokLoginTypeComment      = `Specifies the method of authentication. "none" is a special case in which no authentication method is allowed.`
	cokTagsetComment         = `A set of tags that match provisioner daemons to provisioner jobs, which can originate from workspaces or templates. tagset is a narrowed type over jsonb. It is expected to be the JSON representation of map[string]string. That is, {"key1": "value1", "key2": "value2"}. We need the narrowed type instead of just using jsonb so that we can give sqlc a type hint, otherwise it defaults to json.RawMessage. json.RawMessage is a suboptimal type to use in the context that we need tagset for.`
	cokTagsetFunctionComment = `Returns true if the provisioner_tags contains the job_tags, or if the job_tags represents an untagged provisioner and the superset is exactly equal to the subset.`
	cokChatSearchComment     = `Extracts searchable content from chat_messages. Returns NULL for scalar JSON strings (content_version=0). Immutable as it is used in indexes.`
	cokChatSearchIndexText   = `Partial index over chat_messages used for populating search_tsv in the background. Only defined over 'searchable' rows of chat_messages where search_tsv is NULL.`
	cokProvisionerIndexText  = `Allow unique provisioner daemon names by user`
	cokTriggerText           = `When a custom_role is deleted, this trigger removes the role from all organization members.`
	cokTemplateUsageText     = `Records aggregated usage statistics for templates/users. All usage is rounded up to the nearest minute.`
	cokStartTimeText         = `Start time of the usage period.`
	cokTimingStatusText      = `What the exit status of the script is.`
	cokTimingsText           = `Timing and execution information about a script run.`
)

// cokRepro is the exact round-2 statement sequence, in order.
var cokRepro = []string{
	"CREATE TYPE grin_kind AS ENUM ('a')",
	"COMMENT ON TYPE grin_kind IS 'kind'",
	"CREATE TABLE grin_kind_t (id int)",
	"CREATE INDEX grin_kind_idx ON grin_kind_t (id)",
	"COMMENT ON INDEX grin_kind_idx IS 'idx'",
	"CREATE DOMAIN grin_dom AS jsonb",
	"COMMENT ON DOMAIN grin_dom IS 'dom'",
	"CREATE FUNCTION grin_kind_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
	"CREATE TRIGGER grin_kind_tr AFTER INSERT ON grin_kind_t FOR EACH ROW EXECUTE PROCEDURE grin_kind_fn()",
	"COMMENT ON TRIGGER grin_kind_tr ON grin_kind_t IS 'tr'",
	"COMMENT ON FUNCTION grin_kind_fn() IS 'fn'",
	"COMMENT ON COLUMN grin_kind_t.id IS 'col'",
}

// cokMigrationScaffolding creates the minimal synthetic schema the coder
// migrations' COMMENT statements address. Object names are literal.
var cokMigrationScaffolding = []string{
	"CREATE TYPE new_logintype AS ENUM ('password','github','oidc','token','none')",
	"CREATE TYPE new_user_status AS ENUM ('active','suspended','dormant')",
	"CREATE TYPE provisioner_job_status AS ENUM ('pending','running','succeeded')",
	"CREATE TYPE workspace_agent_script_timing_stage AS ENUM ('start','stop','cron')",
	"CREATE TYPE workspace_agent_script_timing_status AS ENUM ('ok','exit_failure','timed_out','pipes_left_open')",
	"CREATE TYPE provisioner_daemon_status AS ENUM ('offline','idle','busy')",
	"CREATE TYPE audit_action AS ENUM ('connect','disconnect')",
	"CREATE TYPE chat_title_source AS ENUM ('fallback','generated','user')",
	"CREATE TABLE provisioner_jobs(id int, job_status text)",
	"CREATE TABLE oauth2_provider_app_codes(id int)",
	"CREATE TABLE oauth2_provider_app_tokens(refresh_hash text)",
	"CREATE TABLE template_usage_stats(start_time text, end_time text, template_id int, user_id int, median_latency_ms int, usage_mins int, ssh_mins int, sftp_mins int, reconnecting_pty_mins int, vscode_mins int, jetbrains_mins int, app_usage_mins text)",
	"CREATE TABLE workspace_agent_stats(id int, session_counts text)",
	"CREATE TABLE workspace_agent_scripts(id int)",
	"CREATE TABLE provisioner_daemons(id int)",
	"CREATE TABLE workspace_app_audit_sessions(agent_id text, app_id text, user_id text, ip text, user_agent text, slug_or_port text, status_code int, started_at text, updated_at text)",
	"CREATE TABLE workspace_agent_devcontainers(id int, workspace_agent_id int, created_at text, workspace_folder text, config_path text)",
	"CREATE TABLE connection_logs(code int, user_agent text, user_id text, slug_or_port text, connection_id text, disconnect_time text, disconnect_reason text)",
	"CREATE TABLE api_keys(id int)",
	"CREATE TABLE tasks(id int, name text)",
	"CREATE TABLE chat_messages(id int, search_tsv text)",
	"CREATE TABLE chats(id int, title_source text, title_updated_at text)",
	"CREATE TABLE custom_roles(id int, organization_id int)",
	"CREATE TABLE workspace_agent_script_timings(script_id int, stage workspace_agent_script_timing_stage, status workspace_agent_script_timing_status)",
	"CREATE INDEX workspace_agent_stats_template_id_created_at_user_id_idx ON workspace_agent_stats(id)",
	"CREATE INDEX idx_provisioner_daemons_name_owner_key ON provisioner_daemons(id)",
	"CREATE INDEX idx_provisioner_daemons_org_name_owner_key ON provisioner_daemons(id)",
	"CREATE INDEX workspace_agent_scripts_workspace_agent_id_idx ON workspace_agent_scripts(id)",
	"CREATE INDEX template_usage_stats_start_time_template_id_user_id_idx ON template_usage_stats(start_time)",
	"CREATE INDEX template_usage_stats_start_time_idx ON template_usage_stats(start_time)",
	"CREATE INDEX workspace_app_audit_sessions_unique_index ON workspace_app_audit_sessions(agent_id)",
	"CREATE INDEX workspace_agent_devcontainers_workspace_agent_id ON workspace_agent_devcontainers(workspace_agent_id)",
	"CREATE INDEX idx_connection_logs_connection_id_workspace_id_agent_name ON connection_logs(connection_id)",
	"CREATE INDEX provisioner_jobs_worker_id_organization_id_completed_at_idx ON provisioner_jobs(id)",
	"CREATE INDEX api_keys_last_used_idx ON api_keys(id)",
	"CREATE INDEX tasks_owner_id_name_unique_idx ON tasks(name)",
	"CREATE INDEX idx_chat_messages_search_tsv ON chat_messages(id)",
	"CREATE INDEX idx_chats_title_fts ON chats(id)",
	"CREATE DOMAIN tagset AS jsonb",
	"CREATE FUNCTION provisioner_tagset_contains(provisioner_tags tagset, job_tags tagset) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN RETURN true; END$$",
	"CREATE FUNCTION chat_message_search_text(content jsonb) RETURNS text LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
	"CREATE FUNCTION set_chat_message_revision_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
	"CREATE FUNCTION update_chat_history_after_message_update() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
	"CREATE FUNCTION remove_organization_member_role() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN OLD; END$$",
	"CREATE TRIGGER remove_organization_member_custom_role BEFORE DELETE ON custom_roles FOR EACH ROW EXECUTE PROCEDURE remove_organization_member_role()",
}

// cokMigrationComments replays every COMMENT statement occurrence of the 25
// named coder migrations, byte-for-byte: *.up.sql then *.down.sql, in numeric
// order, duplicates included. The multi-line and lowercase spellings are the
// migration text, not test formatting.
var cokMigrationComments = []string{
	// 000126_login_type_none.up.sql
	"COMMENT ON TYPE new_logintype IS 'Specifies the method of authentication. \"none\" is a special case in which no authentication method is allowed.';",
	// 000144_user_status_dormant.up.sql
	"COMMENT ON TYPE new_user_status IS 'Defines the users status: active, dormant, or suspended.';",
	// 000159_workspace_agent_stats_template_id_created_at_user_id_idx.up.sql
	"COMMENT ON INDEX workspace_agent_stats_template_id_created_at_user_id_idx IS 'Support index for template insights endpoint to build interval reports faster.';",
	// 000160_provisioner_job_status.up.sql
	"COMMENT ON TYPE provisioner_job_status IS 'Computed status of a provisioner job. Jobs could be stuck in a hung state, these states do not guarantee any transition to another state.';",
	"COMMENT ON COLUMN provisioner_jobs.job_status IS 'Computed column to track the status of the job.';",
	// 000161_..._include_sessions.up.sql and .down.sql
	"COMMENT ON INDEX workspace_agent_stats_template_id_created_at_user_id_idx IS 'Support index for template insights endpoint to build interval reports faster.';",
	"COMMENT ON INDEX workspace_agent_stats_template_id_created_at_user_id_idx IS 'Support index for template insights endpoint to build interval reports faster.';",
	// 000178_provisioner_daemon_idx_owner.up.sql
	"COMMENT ON INDEX idx_provisioner_daemons_name_owner_key\n\tIS 'Relax uniqueness constraint for provisioner daemon names';",
	// 000181_coalesce_provisioner_daemon_idx_owner.up.sql and .down.sql
	"COMMENT ON INDEX idx_provisioner_daemons_name_owner_key\n\tIS 'Allow unique provisioner daemon names by user';",
	"COMMENT ON INDEX idx_provisioner_daemons_name_owner_key\n\tIS 'Relax uniqueness constraint for provisioner daemon names';",
	// 000195_oauth2_provider_codes.up.sql
	"COMMENT ON TABLE oauth2_provider_app_codes IS 'Codes are meant to be exchanged for access tokens.';",
	"COMMENT ON COLUMN oauth2_provider_app_tokens.refresh_hash IS 'Refresh tokens provide a way to refresh an access token (API key). An expired API key can be refreshed if this token is not yet expired, meaning this expiry can outlive an API key.';",
	"COMMENT ON TYPE new_logintype IS 'Specifies the method of authentication. \"none\" is a special case in which no authentication method is allowed.';",
	// 000203_template_usage_stats.up.sql
	"COMMENT ON TABLE template_usage_stats IS 'Records aggregated usage statistics for templates/users. All usage is rounded up to the nearest minute.';",
	"COMMENT ON COLUMN template_usage_stats.start_time IS 'Start time of the usage period.';",
	"COMMENT ON COLUMN template_usage_stats.end_time IS 'End time of the usage period.';",
	"COMMENT ON COLUMN template_usage_stats.template_id IS 'ID of the template being used.';",
	"COMMENT ON COLUMN template_usage_stats.user_id IS 'ID of the user using the template.';",
	"COMMENT ON COLUMN template_usage_stats.median_latency_ms IS 'Median latency the user is experiencing, in milliseconds. Null means no value was recorded.';",
	"COMMENT ON COLUMN template_usage_stats.usage_mins IS 'Total minutes the user has been using the template.';",
	"COMMENT ON COLUMN template_usage_stats.ssh_mins IS 'Total minutes the user has been using SSH.';",
	"COMMENT ON COLUMN template_usage_stats.sftp_mins IS 'Total minutes the user has been using SFTP.';",
	"COMMENT ON COLUMN template_usage_stats.reconnecting_pty_mins IS 'Total minutes the user has been using the reconnecting PTY.';",
	"COMMENT ON COLUMN template_usage_stats.vscode_mins IS 'Total minutes the user has been using VSCode.';",
	"COMMENT ON COLUMN template_usage_stats.jetbrains_mins IS 'Total minutes the user has been using JetBrains.';",
	"COMMENT ON COLUMN template_usage_stats.app_usage_mins IS 'Object with app names as keys and total minutes used as values. Null means no app usage was recorded.';",
	"COMMENT ON INDEX template_usage_stats_start_time_template_id_user_id_idx IS 'Index for primary key.';",
	"COMMENT ON INDEX template_usage_stats_start_time_idx IS 'Index for querying MAX(start_time).';",
	// 000204_add_workspace_agent_scripts_fk_index.up.sql
	"COMMENT ON INDEX workspace_agent_scripts_workspace_agent_id_idx IS 'Foreign key support index for faster lookups';",
	// 000239_provisioner_daemon_org_constraint.up.sql and .down.sql
	"COMMENT ON INDEX idx_provisioner_daemons_org_name_owner_key IS 'Allow unique provisioner daemon names by organization and user';",
	"COMMENT ON INDEX idx_provisioner_daemons_name_owner_key IS 'Allow unique provisioner daemon names by user';",
	// 000241_delete_user_roles.up.sql
	"COMMENT ON TRIGGER\n\tremove_organization_member_custom_role\n\tON custom_roles IS\n\t\t'When a custom_role is deleted, this trigger removes the role from all organization members.';",
	// 000257_workspace_agent_script_timings.up.sql
	"COMMENT ON TYPE workspace_agent_script_timing_stage IS 'What stage the script was ran in.';",
	"COMMENT ON TYPE workspace_agent_script_timing_status IS 'What the exit status of the script is.';",
	"COMMENT ON TYPE workspace_agent_script_timings IS 'Timing and execution information about a script run.';",
	// 000275_check_tags.up.sql
	"COMMENT ON DOMAIN tagset IS '" + cokTagsetComment + "';",
	"COMMENT ON FUNCTION provisioner_tagset_contains(tagset, tagset) IS '" + cokTagsetFunctionComment + "';",
	// 000286_provisioner_daemon_status.up.sql
	"COMMENT ON TYPE provisioner_daemon_status IS 'The status of a provisioner daemon.';",
	// 000301_add_workspace_app_audit_sessions.up.sql
	"COMMENT ON TABLE workspace_app_audit_sessions IS 'Audit sessions for workspace apps, the data in this table is ephemeral and is used to deduplicate audit log entries for workspace apps. While a session is active, the same data will not be logged again. This table does not store historical data.';",
	"COMMENT ON COLUMN workspace_app_audit_sessions.agent_id IS 'The agent that the workspace app or port forward belongs to.';",
	"COMMENT ON COLUMN workspace_app_audit_sessions.app_id IS 'The app that is currently in the workspace app. This is may be uuid.Nil because ports are not associated with an app.';",
	"COMMENT ON COLUMN workspace_app_audit_sessions.user_id IS 'The user that is currently using the workspace app. This is may be uuid.Nil if we cannot determine the user.';",
	"COMMENT ON COLUMN workspace_app_audit_sessions.ip IS 'The IP address of the user that is currently using the workspace app.';",
	"COMMENT ON COLUMN workspace_app_audit_sessions.user_agent IS 'The user agent of the user that is currently using the workspace app.';",
	"COMMENT ON COLUMN workspace_app_audit_sessions.slug_or_port IS 'The slug or port of the workspace app that the user is currently using.';",
	"COMMENT ON COLUMN workspace_app_audit_sessions.status_code IS 'The HTTP status produced by the token authorization. Defaults to 200 if no status is provided.';",
	"COMMENT ON COLUMN workspace_app_audit_sessions.started_at IS 'The time the user started the session.';",
	"COMMENT ON COLUMN workspace_app_audit_sessions.updated_at IS 'The time the session was last updated.';",
	"COMMENT ON INDEX workspace_app_audit_sessions_unique_index IS 'Unique index to ensure that we do not allow duplicate entries from multiple transactions.';",
	// 000303_add_workspace_agent_devcontainers.up.sql
	"COMMENT ON TABLE workspace_agent_devcontainers IS 'Workspace agent devcontainer configuration';",
	"COMMENT ON COLUMN workspace_agent_devcontainers.id IS 'Unique identifier';",
	"COMMENT ON COLUMN workspace_agent_devcontainers.workspace_agent_id IS 'Workspace agent foreign key';",
	"COMMENT ON COLUMN workspace_agent_devcontainers.created_at IS 'Creation timestamp';",
	"COMMENT ON COLUMN workspace_agent_devcontainers.workspace_folder IS 'Workspace folder';",
	"COMMENT ON COLUMN workspace_agent_devcontainers.config_path IS 'Path to devcontainer.json.';",
	"COMMENT ON INDEX workspace_agent_devcontainers_workspace_agent_id IS 'Workspace agent foreign key and query index';",
	// 000349_connection_logs.up.sql
	"COMMENT ON COLUMN connection_logs.code IS 'Either the HTTP status code of the web request, or the exit code of an SSH connection. For non-web connections, this is Null until we receive a disconnect event for the same connection_id.';",
	"COMMENT ON COLUMN connection_logs.user_agent IS 'Null for SSH events. For web connections, this is the User-Agent header from the request.';",
	"COMMENT ON COLUMN connection_logs.user_id IS 'Null for SSH events. For web connections, this is the ID of the user that made the request.';",
	"COMMENT ON COLUMN connection_logs.slug_or_port IS 'Null for SSH events. For web connections, this is the slug of the app or the port number being forwarded.';",
	"COMMENT ON COLUMN connection_logs.connection_id IS 'The SSH connection ID. Used to correlate connections and disconnections. As it originates from the agent, it is not guaranteed to be unique.';",
	"COMMENT ON COLUMN connection_logs.disconnect_time IS 'The time the connection was closed. Null for web connections. For other connections, this is null until we receive a disconnect event for the same connection_id.';",
	"COMMENT ON COLUMN connection_logs.disconnect_reason IS 'The reason the connection was closed. Null for web connections. For other connections, this is null until we receive a disconnect event for the same connection_id.';",
	"COMMENT ON TYPE audit_action IS 'NOTE: `connect`, `disconnect`, `open`, and `close` are deprecated and no longer used - these events are now tracked in the connection_logs table.';",
	"COMMENT ON INDEX idx_connection_logs_connection_id_workspace_id_agent_name IS 'Connection ID is NULL for web events, but present for SSH events. Therefore, this index allows multiple web events for the same workspace & agent. For SSH events, the upsertion query handles duplicates on this index by upserting the disconnect_time and disconnect_reason for the same connection_id when the connection is closed.';",
	// 000364_optimize_getprovisionerdaemonswithstatusbyorganization_query.up.sql
	"COMMENT ON INDEX provisioner_jobs_worker_id_organization_id_completed_at_idx IS 'Support index for finding the latest completed jobs for a worker (and organization), nulls first so that active jobs have priority; targets: GetProvisionerDaemonsWithStatusByOrganization';",
	// 000365_add_index_for_getapikeyslastusedafter.up.sql
	"COMMENT ON INDEX api_keys_last_used_idx IS 'Index for optimizing api_keys queries filtering by last_used';",
	// 000380_task_name_unique.up.sql
	"COMMENT ON INDEX tasks_owner_id_name_unique_idx IS 'Index to ensure uniqueness for task owner/name';",
	// 000545_chat_search_schema.up.sql
	"COMMENT ON FUNCTION chat_message_search_text IS '" + cokChatSearchComment + "';",
	"COMMENT ON COLUMN chat_messages.search_tsv IS 'Used for full text search. NULL initially, populated async via background job.';",
	"COMMENT ON INDEX idx_chat_messages_search_tsv IS 'Partial index over chat_messages used for full text search. Only defined over ''searchable'' rows of chat_messages.';",
	"COMMENT ON INDEX idx_chat_messages_search_tsv IS 'Partial index over chat_messages used for populating search_tsv in the background. Only defined over ''searchable'' rows of chat_messages where search_tsv is NULL.';",
	"COMMENT ON index idx_chats_title_fts IS 'Used for full text search. Defined over all rows of the chats table.';",
	"COMMENT ON index idx_chats_title_fts IS 'Used for full text search. Defined over all rows of the chats table.';",
	"COMMENT ON FUNCTION set_chat_message_revision_before IS 'Component of chatd. Updates chat_snapshot_version when any fields of chat_messages change. Excludes changes to search_tsv as it is not relevant to chatd''s processing loop.';",
	"COMMENT ON FUNCTION update_chat_history_after_message_update IS 'Component of chatd. Updates history_version and generation_attempt on chats when chat_messages is updated. Excludes changes to search_tsv.';",
	// 000590_workspace_agent_session_counts.up.sql and .down.sql
	"COMMENT ON COLUMN workspace_agent_stats.session_counts IS 'Positive session counts keyed by the canonical app name reported by the agent.';",
	"COMMENT ON INDEX workspace_agent_stats_template_id_created_at_user_id_idx IS 'Support index for template insights endpoint to build interval reports faster.';",
	"COMMENT ON INDEX workspace_agent_stats_template_id_created_at_user_id_idx IS 'Support index for template insights endpoint to build interval reports faster.';",
	// 000608_chat_title_source.up.sql
	"COMMENT ON TYPE chat_title_source IS 'Where a chat title came from, in ascending rank. A title write applies only when its source ranks at or above the current source. fallback: derived from the first prompt, or the default title of a chat created without one. generated: written by automatic title generation. user: supplied by the caller at creation or by rename.';",
	"COMMENT ON COLUMN chats.title_source IS 'Rows from before this column existed are fallback regardless of who set their title.';",
	"COMMENT ON COLUMN chats.title_updated_at IS 'Orders title events, because title writes do not change updated_at. Rows from before this column existed have the migration time.';",
	// 000627_workspace_agent_script_timing_skipped_canceled.up.sql and .down.sql
	"COMMENT ON TYPE workspace_agent_script_timing_status IS 'The terminal outcome of a script execution.';",
	"COMMENT ON TYPE workspace_agent_script_timing_status IS '" + cokTimingStatusText + "';",
}

// TestReferenceAdminCommentObjectKindsFeatureSmoke replays the exact round-2
// repro and then every COMMENT statement of the 25 named coder migrations on a
// minimal synthetic schema, asserting the exact final literals afterwards.
func TestReferenceAdminCommentObjectKindsFeatureSmoke(t *testing.T) {
	cokScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, s *cokSession) {
		for _, q := range cokRepro {
			s.exec(q)
		}
		for _, q := range cokMigrationScaffolding {
			s.exec(q)
		}
		for _, q := range cokMigrationComments {
			s.exec(q)
		}

		s.run(cokText("SELECT obj_description('new_logintype'::regtype,'pg_type') AS text", cokLoginTypeComment))
		s.run(cokText("SELECT obj_description('workspace_agent_script_timing_status'::regtype,'pg_type') AS text", cokTimingStatusText))
		s.run(cokText("SELECT obj_description('workspace_agent_script_timings'::regtype,'pg_type') AS text", cokTimingsText))
		s.run(cokText("SELECT obj_description('tagset'::regtype,'pg_type') AS text", cokTagsetComment))
		s.run(cokText("SELECT obj_description('idx_chat_messages_search_tsv'::regclass,'pg_class') AS text", cokChatSearchIndexText))
		s.run(cokText("SELECT obj_description('idx_provisioner_daemons_name_owner_key'::regclass,'pg_class') AS text", cokProvisionerIndexText))
		s.run(cokText("SELECT obj_description(oid,'pg_trigger') AS text FROM pg_catalog.pg_trigger WHERE tgrelid='custom_roles'::regclass AND tgname='remove_organization_member_custom_role'", cokTriggerText))
		s.run(cokText("SELECT obj_description(p.oid,'pg_proc') AS text FROM pg_catalog.pg_proc p WHERE p.proname='provisioner_tagset_contains'", cokTagsetFunctionComment))
		s.run(cokText("SELECT obj_description(p.oid,'pg_proc') AS text FROM pg_catalog.pg_proc p WHERE p.proname='chat_message_search_text'", cokChatSearchComment))
		s.run(cokText("SELECT obj_description('template_usage_stats'::regclass,'pg_class') AS text", cokTemplateUsageText))
		s.run(cokText("SELECT col_description('template_usage_stats'::regclass,1) AS text", cokStartTimeText))

		typeOID := s.oid("SELECT 'workspace_agent_script_timings'::regtype::oid")
		domainOID := s.oid("SELECT 'tagset'::regtype::oid")
		indexOID := s.oid("SELECT 'idx_chat_messages_search_tsv'::regclass::oid")
		triggerOID := s.oid("SELECT oid FROM pg_catalog.pg_trigger WHERE tgrelid='custom_roles'::regclass AND tgname='remove_organization_member_custom_role'")
		routineOID := s.oid("SELECT p.oid FROM pg_catalog.pg_proc p WHERE p.proname='provisioner_tagset_contains'")
		s.description(cokDescription(typeOID, cokTypeClass, cokTimingsText))
		s.description(cokDescription(domainOID, cokTypeClass, cokTagsetComment))
		s.description(cokDescription(indexOID, cokRelationClass, cokChatSearchIndexText))
		s.description(cokDescription(triggerOID, cokTriggerClass, cokTriggerText))
		s.description(cokDescription(routineOID, cokProcedureClass, cokTagsetFunctionComment))
		s.run(fixture.Select("SELECT count(*) FROM pg_catalog.pg_shdescription WHERE classoid IN (1247,1255,1259,2620)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}