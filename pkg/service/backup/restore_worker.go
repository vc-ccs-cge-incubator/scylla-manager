package backup

import (
	"context"

	"github.com/scylladb/gocqlx/v2"
	. "github.com/scylladb/scylla-manager/v3/pkg/service/backup/backupspec"
	"github.com/scylladb/scylla-manager/v3/pkg/util/inexlist/ksfilter"
)

// tableRunProgress maps rclone job ID to corresponding
// restore run progress for a specific table.
type tableRunProgress map[int64]*RestoreRunProgress

// restoreHost represents host that can be used for restoring files.
// If set, JobID is the ID of the unfinished rclone job started on the host.
type restoreHost struct {
	Host   string
	Shards uint
	JobID  int64
}

// bundle represents SSTables with the same ID.
type bundle []string

// restoreWorker extends implementation of backup worker.
type restoreWorker struct {
	worker

	// Fields below are constant among all restore runs of the same restore task.
	managerSession gocqlx.Session
	clusterSession gocqlx.Session
	// Iterates over all manifests in given location with
	// cluster ID and snapshot tag specified in restore target.
	forEachRestoredManifest func(ctx context.Context, location Location, f func(ManifestInfoWithContent) error) error
	filter                  *ksfilter.Filter // Filters keyspaces specified in restore target

	// Fields below are mutable for each restore run
	location          Location                // Currently restored location
	miwc              ManifestInfoWithContent // Currently restored manifest
	hosts             []restoreHost           // Restore units created for currently restored location
	bundles           map[string]bundle       // Maps bundle to it's ID
	bundleIDPool      chan string             // IDs of the bundles that are yet to be restored
	prevTableProgress tableRunProgress        // Progress of the currently restored table from previous run
	resumed           bool                    // Set to true if current run has already skipped all tables restored in previous run
}
