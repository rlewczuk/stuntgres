package settings

const (
	replicationSending = "Replication / Sending Servers"
	replicationPrimary = "Replication / Primary Server"
	replicationStandby = "Replication / Standby Servers"
	replicationSubscribers = "Replication / Subscribers"
)

// Released replication configuration descriptors, without physical consumers.
// track_commit_timestamp remains in builtins with its genuine native consumer.
var replicationBuiltins = []Definition{
	{Name: "hot_standby", Type: Boolean, Access: StartupOnly, BootDefault: "on", Category: replicationStandby, Description: "Allows connections and queries during recovery."},
	{Name: "hot_standby_feedback", Type: Boolean, Access: Sighup, BootDefault: "off", Category: replicationStandby, Description: "Allows feedback from a hot standby to the primary that will avoid query conflicts."},
	{Name: "idle_replication_slot_timeout", Type: Integer, Access: Sighup, BootDefault: "0", Unit: Seconds, IntegerMax: 2147483647, Category: replicationSending, Description: "Sets the duration a replication slot can remain idle before it is invalidated."},
	{Name: "max_active_replication_origins", Type: Integer, Access: StartupOnly, BootDefault: "10", IntegerMax: 262143, Category: replicationSubscribers, Description: "Sets the maximum number of active replication origins."},
	{Name: "max_logical_replication_workers", Type: Integer, Access: StartupOnly, BootDefault: "4", IntegerMax: 262143, Category: replicationSubscribers, Description: "Maximum number of logical replication worker processes."},
	{Name: "max_parallel_apply_workers_per_subscription", Type: Integer, Access: Sighup, BootDefault: "2", IntegerMax: 1024, Category: replicationSubscribers, Description: "Maximum number of parallel apply workers per subscription."},
	{Name: "max_replication_slots", Type: Integer, Access: StartupOnly, BootDefault: "10", IntegerMax: 262143, Category: replicationSending, Description: "Sets the maximum number of simultaneously defined replication slots."},
	{Name: "max_slot_wal_keep_size", Type: Integer, Access: Sighup, BootDefault: "-1", Unit: Megabytes, IntegerMin: -1, IntegerMax: 2147483647, Category: replicationSending, Description: "Sets the maximum WAL size that can be reserved by replication slots.", ExtraDescription: "Replication slots will be marked as failed, and segments released for deletion or recycling, if this much space is occupied by WAL on disk. -1 means no maximum."},
	{Name: "max_standby_archive_delay", Type: Integer, Access: Sighup, BootDefault: "30000", Unit: Milliseconds, IntegerMin: -1, IntegerMax: 2147483647, Category: replicationStandby, Description: "Sets the maximum delay before canceling queries when a hot standby server is processing archived WAL data.", ExtraDescription: "-1 means wait forever."},
	{Name: "max_standby_streaming_delay", Type: Integer, Access: Sighup, BootDefault: "30000", Unit: Milliseconds, IntegerMin: -1, IntegerMax: 2147483647, Category: replicationStandby, Description: "Sets the maximum delay before canceling queries when a hot standby server is processing streamed WAL data.", ExtraDescription: "-1 means wait forever."},
	{Name: "max_sync_workers_per_subscription", Type: Integer, Access: Sighup, BootDefault: "2", IntegerMax: 262143, Category: replicationSubscribers, Description: "Maximum number of table synchronization workers per subscription."},
	{Name: "max_wal_senders", Type: Integer, Access: StartupOnly, BootDefault: "10", IntegerMax: 262143, Category: replicationSending, Description: "Sets the maximum number of simultaneously running WAL sender processes."},
	{Name: "output_plugin_libraries", Type: String, Access: Privileged, BootDefault: "pgoutput, test_decoding", ReadRestricted: true, Category: replicationSending, Description: "Lists libraries that may be named as logical decoding output plugins.", ExtraDescription: "Users with REPLICATION privileges may only use plugins in this list when creating logical replication slots."},
	{Name: "primary_conninfo", Type: String, Access: Sighup, ReadRestricted: true, Category: replicationStandby, Description: "Sets the connection string to be used to connect to the sending server."},
	{Name: "primary_slot_name", Type: String, Access: Sighup, Category: replicationStandby, Description: "Sets the name of the replication slot to use on the sending server."},
	{Name: "recovery_min_apply_delay", Type: Integer, Access: Sighup, BootDefault: "0", Unit: Milliseconds, IntegerMax: 2147483647, Category: replicationStandby, Description: "Sets the minimum delay for applying changes during recovery."},
	{Name: "sync_replication_slots", Type: Boolean, Access: Sighup, BootDefault: "off", Category: replicationStandby, Description: "Enables a physical standby to synchronize logical failover replication slots from the primary server."},
	{Name: "synchronized_standby_slots", Type: String, Access: Sighup, Category: replicationPrimary, Description: "Lists streaming replication standby server replication slot names that logical WAL sender processes will wait for.", ExtraDescription: "Logical WAL sender processes will send decoded changes to output plugins only after the specified replication slots have confirmed receiving WAL."},
	{Name: "synchronous_standby_names", Type: String, Access: Sighup, Category: replicationPrimary, Description: "Number of synchronous standbys and list of names of potential synchronous ones."},
	{Name: "wal_keep_size", Type: Integer, Access: Sighup, BootDefault: "0", Unit: Megabytes, IntegerMax: 2147483647, Category: replicationSending, Description: "Sets the size of WAL files held for standby servers."},
	{Name: "wal_receiver_create_temp_slot", Type: Boolean, Access: Sighup, BootDefault: "off", Category: replicationStandby, Description: "Sets whether a WAL receiver should create a temporary replication slot if no permanent slot is configured."},
	{Name: "wal_receiver_status_interval", Type: Integer, Access: Sighup, BootDefault: "10", Unit: Seconds, IntegerMax: 2147483, Category: replicationStandby, Description: "Sets the maximum interval between WAL receiver status reports to the sending server."},
	{Name: "wal_receiver_timeout", Type: Integer, Access: Sighup, BootDefault: "60000", Unit: Milliseconds, IntegerMax: 2147483647, Category: replicationStandby, Description: "Sets the maximum wait time to receive data from the sending server.", ExtraDescription: "0 disables the timeout."},
	{Name: "wal_retrieve_retry_interval", Type: Integer, Access: Sighup, BootDefault: "5000", Unit: Milliseconds, IntegerMin: 1, IntegerMax: 2147483647, Category: replicationStandby, Description: "Sets the time to wait before retrying to retrieve WAL after a failed attempt."},
	{Name: "wal_sender_timeout", Type: Integer, Access: Session, BootDefault: "60000", Unit: Milliseconds, IntegerMax: 2147483647, Category: replicationSending, Description: "Sets the maximum time to wait for WAL replication."},
}
