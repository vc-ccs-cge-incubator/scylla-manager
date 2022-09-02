package backup

import (
	"context"

	"github.com/gocql/gocql"
	"github.com/pkg/errors"
	"github.com/scylladb/gocqlx/v2"
	"github.com/scylladb/gocqlx/v2/qb"
	"github.com/scylladb/scylla-manager/v3/pkg/schema/table"
	. "github.com/scylladb/scylla-manager/v3/pkg/service/backup/backupspec"
	"github.com/scylladb/scylla-manager/v3/pkg/util/inexlist/ksfilter"
	"github.com/scylladb/scylla-manager/v3/pkg/util/uuid"
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

func (w *restoreWorker) InsertRun(ctx context.Context, run *RestoreRun) {
	if err := table.RestoreRun.InsertQuery(w.managerSession).BindStruct(run).ExecRelease(); err != nil {
		w.Logger.Error(ctx, "Insert run",
			"run", *run,
			"error", err,
		)
	}
}

func (w *restoreWorker) InsertRunProgress(ctx context.Context, pr *RestoreRunProgress) {
	if err := table.RestoreRunProgress.InsertQuery(w.managerSession).BindStruct(pr).ExecRelease(); err != nil {
		w.Logger.Error(ctx, "Insert run progress",
			"progress", *pr,
			"error", err,
		)
	}
}

func (w *restoreWorker) deleteRunProgress(ctx context.Context, pr *RestoreRunProgress) {
	if err := table.RestoreRunProgress.DeleteQuery(w.managerSession).BindStruct(pr).ExecRelease(); err != nil {
		w.Logger.Error(ctx, "Delete run progress",
			"progress", *pr,
			"error", err,
		)
	}
}

// decorateWithPrevRun gets restore task previous run and if it is not done
// sets prev ID on the given run.
func (w *restoreWorker) decorateWithPrevRun(ctx context.Context, run *RestoreRun) error {
	prev, err := w.GetRun(ctx, run.ClusterID, run.TaskID, uuid.Nil)
	if errors.Is(err, gocql.ErrNotFound) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "get run")
	}
	if prev.Stage == StageRestoreDone {
		return nil
	}

	w.Logger.Info(ctx, "Resuming previous run", "prev_run_id", prev.ID)

	run.PrevID = prev.ID
	run.ManifestPath = prev.ManifestPath
	run.Keyspace = prev.Keyspace
	run.Table = prev.Table
	run.Stage = prev.Stage

	return nil
}

// clonePrevProgress copies all the previous run progress into
// current run progress.
func (w *restoreWorker) clonePrevProgress(ctx context.Context, run *RestoreRun) {
	q := table.RestoreRunProgress.InsertQuery(w.managerSession)
	defer q.Release()

	prevRun := &RestoreRun{
		ClusterID: run.ClusterID,
		TaskID:    run.TaskID,
		ID:        run.PrevID,
	}

	w.ForEachProgress(prevRun, func(pr *RestoreRunProgress) {
		pr.RunID = run.ID
		if err := q.BindStruct(pr).Exec(); err != nil {
			w.Logger.Error(ctx, "Couldn't clone run progress",
				"run_progress", *pr,
				"error", err,
			)
		}
	})
}

// GetRun returns run with specified cluster, task and run ID.
// If run ID is not specified, it returns latest run with specified cluster and task ID.
func (w *restoreWorker) GetRun(ctx context.Context, clusterID, taskID, runID uuid.UUID) (*RestoreRun, error) {
	w.Logger.Debug(ctx, "Get run",
		"cluster_id", clusterID,
		"task_id", taskID,
		"run_id", runID,
	)

	var q *gocqlx.Queryx
	if runID != uuid.Nil {
		q = table.RestoreRun.GetQuery(w.managerSession).BindMap(qb.M{
			"cluster_id": clusterID,
			"task_id":    taskID,
			"id":         runID,
		})
	} else {
		q = table.RestoreRun.SelectQuery(w.managerSession).BindMap(qb.M{
			"cluster_id": clusterID,
			"task_id":    taskID,
		})
	}

	var r RestoreRun
	return &r, q.GetRelease(&r)
}
