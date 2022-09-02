package backup

import (
	"github.com/scylladb/gocqlx/v2/qb"
	"github.com/scylladb/scylla-manager/v3/pkg/schema/table"
)

// ForEachProgress iterates over all RestoreRunProgress that belong to the run.
func (w *restoreWorker) ForEachProgress(run *RestoreRun, cb func(*RestoreRunProgress)) {
	iter := table.RestoreRunProgress.SelectQuery(w.managerSession).BindMap(qb.M{
		"cluster_id": run.ClusterID,
		"task_id":    run.TaskID,
		"run_id":     run.ID,
	}).Iter()
	defer iter.Close()

	pr := new(RestoreRunProgress)
	for iter.StructScan(pr) {
		cb(pr)
	}
}

// ForEachTableProgress iterates over all RestoreRunProgress that belong to the run
// with the same manifest, keyspace and table as the run.
func (w *restoreWorker) ForEachTableProgress(run *RestoreRun, cb func(*RestoreRunProgress)) {
	iter := qb.Select(table.RestoreRunProgress.Name()).Where(
		qb.Eq("cluster_id"),
		qb.Eq("task_id"),
		qb.Eq("run_id"),
		qb.Eq("manifest_path"),
		qb.Eq("keyspace_name"),
		qb.Eq("table_name"),
	).Query(w.managerSession).BindMap(qb.M{
		"cluster_id":    run.ClusterID,
		"task_id":       run.TaskID,
		"run_id":        run.ID,
		"manifest_path": run.ManifestPath,
		"keyspace_name": run.Keyspace,
		"table_name":    run.Table,
	}).Iter()
	defer iter.Close()

	pr := new(RestoreRunProgress)
	for iter.StructScan(pr) {
		cb(pr)
	}
}
