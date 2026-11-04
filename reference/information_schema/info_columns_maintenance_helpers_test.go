package reference_test

import "strings"

func icMaintenanceInventory(inventory map[string][]string) {
	stats := strings.Fields("relid schemaname relname seq_scan last_seq_scan seq_tup_read idx_scan last_idx_scan idx_tup_fetch n_tup_ins n_tup_upd n_tup_del n_tup_hot_upd n_tup_newpage_upd n_live_tup n_dead_tup n_mod_since_analyze n_ins_since_vacuum last_vacuum last_autovacuum last_analyze last_autoanalyze vacuum_count autovacuum_count analyze_count autoanalyze_count total_vacuum_time total_autovacuum_time total_analyze_time total_autoanalyze_time")
	for _, name := range []string{"pg_stat_all_tables", "pg_stat_sys_tables", "pg_stat_user_tables"} {
		inventory[name] = stats
	}
	inventory["pg_stat_xact_all_tables"] = strings.Fields("relid schemaname relname seq_scan seq_tup_read idx_scan idx_tup_fetch n_tup_ins n_tup_upd n_tup_del n_tup_hot_upd n_tup_newpage_upd")
	inventory["pg_stat_xact_sys_tables"] = inventory["pg_stat_xact_all_tables"]
	inventory["pg_stat_all_indexes"] = strings.Fields("relid indexrelid schemaname relname indexrelname idx_scan last_idx_scan idx_tup_read idx_tup_fetch")
	inventory["pg_stat_sys_indexes"] = inventory["pg_stat_all_indexes"]
	inventory["pg_statio_all_indexes"] = strings.Fields("relid indexrelid schemaname relname indexrelname idx_blks_read idx_blks_hit")
	inventory["pg_statio_user_indexes"] = inventory["pg_statio_all_indexes"]
	inventory["pg_statio_all_tables"] = strings.Fields("relid schemaname relname heap_blks_read heap_blks_hit idx_blks_read idx_blks_hit toast_blks_read toast_blks_hit tidx_blks_read tidx_blks_hit")
	inventory["pg_statio_sys_tables"] = strings.Fields("relid schemaname relname heap_blks_read heap_blks_hit idx_blks_read idx_blks_hit toast_blks_read toast_blks_hit tidx_blks_read tidx_blks_hit")
	inventory["pg_statio_user_tables"] = strings.Fields("relid schemaname relname heap_blks_read heap_blks_hit idx_blks_read idx_blks_hit toast_blks_read toast_blks_hit tidx_blks_read tidx_blks_hit")
	inventory["pg_stat_progress_vacuum"] = strings.Fields("pid datid datname relid phase heap_blks_total heap_blks_scanned heap_blks_vacuumed index_vacuum_count max_dead_tuple_bytes dead_tuple_bytes num_dead_item_ids indexes_total indexes_processed delay_time")
	inventory["pg_stat_progress_basebackup"] = strings.Fields("pid phase backup_total backup_streamed tablespaces_total tablespaces_streamed")
	inventory["pg_stat_progress_copy"] = strings.Fields("pid datid datname relid command type bytes_processed bytes_total tuples_processed tuples_excluded tuples_skipped")
	inventory["pg_stat_progress_create_index"] = strings.Fields("pid datid datname relid index_relid command phase lockers_total lockers_done current_locker_pid blocks_total blocks_done tuples_total tuples_done partitions_total partitions_done")
	inventory["pg_stat_progress_cluster"] = strings.Fields("pid datid datname relid command phase cluster_index_relid heap_tuples_scanned heap_tuples_written heap_blks_total heap_blks_scanned index_rebuild_count")
}
