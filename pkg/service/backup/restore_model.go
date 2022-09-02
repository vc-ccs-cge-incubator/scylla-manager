package backup

import . "github.com/scylladb/scylla-manager/v3/pkg/service/backup/backupspec"

// RestoreTarget specifies what data should be restored and from which locations.
type RestoreTarget struct {
	Location    []Location `json:"location"`
	Keyspace    []string   `json:"keyspace"`
	SnapshotTag string     `json:"snapshot_tag"`
	BatchSize   int        `json:"batch_size"`
	Parallel    int        `json:"parallel"`
	Continue    bool       `json:"continue"`

	Units []Unit `json:"units,omitempty"`
}
