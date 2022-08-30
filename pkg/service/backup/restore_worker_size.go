package backup

import (
	"context"

	. "github.com/scylladb/scylla-manager/v3/pkg/service/backup/backupspec"
)

// restoreSize records size of every table from every manifest.
// Resuming is implemented on manifest level.
func (w *restoreWorker) restoreSize(ctx context.Context, run *RestoreRun, target RestoreTarget) error {
	pr := &RestoreRunProgress{
		ClusterID: run.ClusterID,
		TaskID:    run.TaskID,
		RunID:     run.ID,
	}

	for _, w.location = range target.Location {
		err := w.forEachRestoredManifest(ctx, w.location, func(miwc ManifestInfoWithContent) error {
			w.miwc = miwc

			if !w.resumed {
				// Check if table has already been processed in previous run
				if run.ManifestPath != miwc.Path() {
					return nil
				}
				w.resumed = true
			} else {
				run.ManifestPath = miwc.Path()

				w.InsertRun(ctx, run)
			}

			pr.ManifestPath = run.ManifestPath
			pr.ManifestIP = miwc.IP

			return miwc.ForEachIndexIter(func(fm FilesMeta) {
				pr.KeyspaceName = fm.Keyspace
				pr.TableName = fm.Table
				pr.Size = fm.Size

				w.Logger.Info(ctx, "Recorded table size",
					"manifest", pr.ManifestPath,
					"keyspace", pr.KeyspaceName,
					"table", pr.TableName,
					"size", pr.Size,
				)

				w.Metrics.UpdateRestoreProgress(run.ClusterID, pr.KeyspaceName, pr.TableName, pr.ManifestIP,
					pr.Size, 0, 0, 0)

				// Record table's size
				w.InsertRunProgress(ctx, pr)
			})
		})
		if err != nil {
			return err
		}
	}

	return nil
}
