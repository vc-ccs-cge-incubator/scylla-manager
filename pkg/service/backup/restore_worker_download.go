package backup

import (
	"context"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/scylladb/go-set/strset"
	"github.com/scylladb/scylla-manager/v3/pkg/scyllaclient"
	. "github.com/scylladb/scylla-manager/v3/pkg/service/backup/backupspec"
	"github.com/scylladb/scylla-manager/v3/pkg/util/parallel"
)

// restoreData restores files from every location specified in restore target.
func (w *restoreWorker) restoreData(ctx context.Context, run *RestoreRun, target RestoreTarget) error {
	w.AwaitSchemaAgreement(ctx, w.clusterSession)

	for _, w.location = range target.Location {
		w.Logger.Info(ctx, "Restoring location",
			"location", w.location,
		)
		// Jobs should be initialized once per location
		w.jobs = nil

		err := w.forEachRestoredManifest(ctx, w.location, func(miwc ManifestInfoWithContent) error {
			w.miwc = miwc
			w.Logger.Info(ctx, "Restoring manifest",
				"manifest", miwc.ManifestInfo,
			)
			// Check if manifest has already been processed in previous run
			if !w.resumed && run.ManifestPath != miwc.Path() {
				return nil
			}
			run.ManifestPath = miwc.Path()

			return miwc.ForEachIndexIterWithError(w.restoreFiles(ctx, run, target))
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// restoreFiles returns function that restores files from manifest's table.
func (w *restoreWorker) restoreFiles(ctx context.Context, run *RestoreRun, target RestoreTarget) func(fm FilesMeta) error {
	return func(fm FilesMeta) error {
		// Skip system, filtered out or empty tables
		if isSystemKeyspace(fm.Keyspace) || !w.filter.Check(fm.Keyspace, fm.Table) || len(fm.Files) == 0 {
			return nil
		}
		// Progress of the previous table will only be initialized if
		// previous run has ended it's work on that table.
		w.prevTableProgress = nil
		if !w.resumed {
			// Check if table has already been processed in previous run
			if run.KeyspaceName != fm.Keyspace || run.TableName != fm.Table {
				return nil
			}
			// Last run ended it's work on this table
			w.resumed = true

			w.initPrevTableProgress(ctx, run)
		} else {
			run.TableName = fm.Table
			run.KeyspaceName = fm.Keyspace

			w.InsertRun(ctx, run)
		}

		w.Logger.Info(ctx, "Restoring table",
			"keyspace", fm.Keyspace,
			"table", fm.Table,
		)

		w.initBundles(fm.Files)
		w.initBundlePool()

		// Jobs have to be initialized only once for every location
		if w.jobs == nil {
			err := w.initJobUnits(ctx, run)
			if err != nil {
				return errors.Wrap(err, "initialize jobs")
			}
		}

		err := w.ExecOnDisabledTable(ctx, fm.Keyspace, fm.Table, func() error {
			return w.workFunc(ctx, run, target, fm)
		})

		if len(w.bundleIDPool) > 0 {
			return errors.Wrapf(err, "not restored bundles %v", w.drainBundleIDPool())
		}
		if err != nil {
			// Log errors instead of returning them if all files have been restored
			w.Logger.Error(ctx, "Restore files errors",
				"errors", err,
			)
		}

		return nil
	}
}

// workFunc is responsible for creating and restoring batches on multiple hosts (possibly in parallel).
// It requires previous configuration of restore worker components.
func (w *restoreWorker) workFunc(ctx context.Context, run *RestoreRun, target RestoreTarget, fm FilesMeta) error {
	version, err := w.RecordTableVersion(ctx, fm.Keyspace, fm.Table)
	if err != nil {
		return err
	}

	var (
		srcDir = w.location.RemotePath(w.miwc.SSTableVersionDir(fm.Keyspace, fm.Table, fm.Version))
		dstDir = uploadTableDir(fm.Keyspace, fm.Table, version)
	)

	w.Logger.Info(ctx, "Found source and destination directory",
		"src_dir", srcDir,
		"dst_dir", dstDir,
	)

	// Every host has its personal goroutine which is responsible
	// for creating and downloading batches.
	return parallel.Run(len(w.jobs), target.Parallel, func(n int) error {
		for {
			if ctx.Err() != nil {
				return parallel.Abort(ctx.Err())
			}
			// Current goroutine's host
			j := w.jobs[n]

			pr, err := w.createRunProgress(ctx, run, target, j, srcDir, dstDir)
			if err != nil {
				return errors.Wrapf(err, "create run progress for host: %s", j.Host)
			}
			if pr == nil {
				w.Logger.Info(ctx, "Empty batch",
					"host", j.Host,
				)
				return nil
			}

			batch := w.batchFromIDs(pr.SstableID)

			w.Logger.Info(ctx, "Waiting for job",
				"host", j.Host,
				"job_id", pr.AgentJobID,
			)

			if err := w.waitJob(ctx, pr); err != nil {
				// In case of context cancellation restore is interrupted
				if ctx.Err() != nil {
					return parallel.Abort(ctx.Err())
				}
				// Undo
				w.deleteRunProgress(ctx, pr)
				w.returnBatchToPool(pr.SstableID)

				return errors.Wrapf(err, "wait on rclone job, id: %d, host: %s", j.JobID, j.Host)
			}

			w.Logger.Info(ctx, "Calling agent's restore",
				"host", j.Host,
			)

			if err := w.Client.Restore(ctx, j.Host, fm.Keyspace, fm.Table, version, batch); err != nil {
				w.deleteRunProgress(ctx, pr)
				w.returnBatchToPool(pr.SstableID)

				return errors.Wrap(err, "call agent's restore")
			}

			w.Logger.Info(ctx, "Restored batch",
				"host", j.Host,
				"batch", batch,
			)
		}
	})
}

// createRunProgress either creates new run progress by creating batch and downloading it to host's upload dir,
// or it returns unfinished run progress from previous run.
func (w *restoreWorker) createRunProgress(ctx context.Context, run *RestoreRun, target RestoreTarget, j jobUnit, srcDir, dstDir string) (*RestoreRunProgress, error) {
	// Check if host has an unfinished job
	if j.JobID != 0 {
		pr := w.prevTableProgress[j.JobID]
		// Mark that previously started job is resumed
		for n, u := range w.jobs {
			if u.Host == j.Host {
				w.jobs[n].JobID = 0
				break
			}
		}

		return pr, nil
	}

	if err := w.checkAvailableDiskSpace(ctx, hostInfo{IP: j.Host}); err != nil {
		return nil, errors.Wrap(err, "validate free disk space")
	}

	takenIDs := w.chooseIDsForBatch(j.Shards, target.BatchSize)
	if takenIDs == nil {
		return nil, nil //nolint: nilnil
	}

	batch := w.batchFromIDs(takenIDs)

	w.Logger.Info(ctx, "Created batch",
		"host", j.Host,
		"batch", batch,
	)

	// Download batch to host
	jobID, err := w.Client.RcloneCopyPaths(ctx, j.Host, dstDir, srcDir, batch)
	if err != nil {
		w.returnBatchToPool(takenIDs)

		return nil, errors.Wrap(err, "download batch to upload dir")
	}

	w.Logger.Info(ctx, "Created rclone job",
		"host", j.Host,
		"job_id", jobID,
		"batch", batch,
	)

	return &RestoreRunProgress{
		ClusterID:    run.ClusterID,
		TaskID:       run.TaskID,
		RunID:        run.ID,
		ManifestPath: run.ManifestPath,
		KeyspaceName: run.KeyspaceName,
		TableName:    run.TableName,
		Host:         j.Host,
		AgentJobID:   jobID,
		ManifestIP:   w.miwc.IP,
		SstableID:    takenIDs,
	}, nil
}

func (w *restoreWorker) waitJob(ctx context.Context, pr *RestoreRunProgress) (err error) {
	defer func() {
		// Running stop procedure in a different context because original may be canceled
		stopCtx := context.Background()

		// On error stop job
		if err != nil {
			w.Logger.Info(ctx, "Stop job", "host", pr.Host, "id", pr.AgentJobID)
			if e := w.Client.RcloneJobStop(stopCtx, pr.Host, pr.AgentJobID); e != nil {
				w.Logger.Error(ctx, "Failed to stop job",
					"host", pr.Host,
					"id", pr.AgentJobID,
					"error", e,
				)
			}
		}

		// On exit clear stats
		if e := w.clearJobStats(stopCtx, pr.AgentJobID, pr.Host); e != nil {
			w.Logger.Error(ctx, "Failed to clear job stats",
				"host", pr.Host,
				"id", pr.AgentJobID,
				"error", e,
			)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			job, err := w.Client.RcloneJobProgress(ctx, pr.Host, pr.AgentJobID, w.Config.LongPollingTimeoutSeconds)
			if err != nil {
				return errors.Wrap(err, "fetch job info")
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			switch scyllaclient.RcloneJobStatus(job.Status) {
			case scyllaclient.JobError:
				return errors.Errorf("job error (%d): %s", pr.AgentJobID, job.Error)
			case scyllaclient.JobSuccess:
				w.updateProgress(ctx, pr, job)
				return nil
			case scyllaclient.JobRunning:
				w.updateProgress(ctx, pr, job)
			case scyllaclient.JobNotFound:
				return errJobNotFound
			}
		}
	}
}

func (w *restoreWorker) updateProgress(ctx context.Context, pr *RestoreRunProgress, job *scyllaclient.RcloneJobProgress) {
	pr.StartedAt = nil
	// Set StartedAt and CompletedAt based on Job
	if t := time.Time(job.StartedAt); !t.IsZero() {
		pr.StartedAt = &t
	}
	pr.CompletedAt = nil
	if t := time.Time(job.CompletedAt); !t.IsZero() {
		pr.CompletedAt = &t
	}

	var (
		deltaUploaded = job.Uploaded - pr.Uploaded
		deltaSkipped  = job.Skipped - pr.Skipped
		deltaFailed   = job.Failed - pr.Failed
	)

	pr.Error = job.Error
	pr.Uploaded = job.Uploaded
	pr.Skipped = job.Skipped
	pr.Failed = job.Failed

	w.Metrics.UpdateRestoreProgress(w.ClusterID, pr.KeyspaceName, pr.TableName, pr.ManifestIP,
		0, deltaUploaded, deltaSkipped, deltaFailed)

	w.InsertRunProgress(ctx, pr)
}

// initJobUnits creates jobs with hosts with access to currently restored location
// and living in either location's dc or local dc.
// All running jobs are located at the beginning of the result slice.
func (w *restoreWorker) initJobUnits(ctx context.Context, run *RestoreRun) error {
	status, err := w.Client.Status(ctx)
	if err != nil {
		return errors.Wrap(err, "get client status")
	}

	var (
		liveNodes      scyllaclient.NodeStatusInfoSlice
		remotePath     = w.location.RemotePath("")
		locationStatus = status.Datacenter([]string{w.location.DC})
	)

	if liveNodes, err = w.Client.GetLiveNodesWithLocationAccess(ctx, locationStatus, remotePath); err != nil {
		w.Logger.Error(ctx, "Couldn't find any live nodes in location's dc",
			"location", w.location,
			"error", err,
		)

		localStatus := status.Datacenter([]string{w.Config.LocalDC})
		if liveNodes, err = w.Client.GetLiveNodesWithLocationAccess(ctx, localStatus, remotePath); err != nil {
			return errors.Wrap(err, "no live nodes in location's and local dc")
		}
	}

	w.jobs = make([]jobUnit, 0)
	hostsInPool := strset.New()

	// Collect table progress info from previous run
	if w.prevTableProgress != nil {
		cb := func(pr *RestoreRunProgress) {
			// Ignore progress created in restoreSize
			if pr.AgentJobID == 0 {
				return
			}
			// Place hosts with unfinished jobs at the beginning
			if pr.CompletedAt == nil {
				sh, err := w.Client.ShardCount(ctx, pr.Host)
				if err != nil {
					w.Logger.Info(ctx, "Couldn't get host shard count",
						"host", pr.Host,
						"error", err,
					)
					return
				}

				w.jobs = append(w.jobs, jobUnit{
					Host:   pr.Host,
					Shards: sh,
					JobID:  pr.AgentJobID,
				})

				hostsInPool.Add(pr.Host)
			}
		}

		w.ForEachTableProgress(run, cb)
	}

	for _, n := range liveNodes {
		// Place free hosts in the pool
		if !hostsInPool.Has(n.Addr) {
			sh, err := w.Client.ShardCount(ctx, n.Addr)
			if err != nil {
				w.Logger.Info(ctx, "Couldn't get host shard count",
					"host", n.Addr,
					"error", err,
				)
				continue
			}

			w.jobs = append(w.jobs, jobUnit{
				Host:   n.Addr,
				Shards: sh,
			})

			hostsInPool.Add(n.Addr)
		}
	}

	return nil
}

func (w *restoreWorker) initPrevTableProgress(ctx context.Context, run *RestoreRun) {
	w.prevTableProgress = make(tableRunProgress)

	cb := func(pr *RestoreRunProgress) {
		// Don't include progress created in restoreSize
		if pr.AgentJobID != 0 {
			w.prevTableProgress[pr.AgentJobID] = pr
		}
	}

	w.ForEachTableProgress(run, cb)

	w.Logger.Info(ctx, "Previous table progress",
		"keyspace", run.KeyspaceName,
		"table", run.TableName,
		"table_progress", w.prevTableProgress,
	)
}

func (w *restoreWorker) initBundles(sstables []string) {
	w.bundles = make(map[string]bundle)

	for _, f := range sstables {
		id := sstableID(f)
		w.bundles[id] = append(w.bundles[id], f)
	}
}

// initBundlePool creates pool of SSTable IDs that have yet to be restored.
// (It does not include ones that are currently being restored).
func (w *restoreWorker) initBundlePool() {
	w.bundleIDPool = make(chan string, len(w.bundles))
	processed := strset.New()

	for _, pr := range w.prevTableProgress {
		processed.Add(pr.SstableID...)
	}

	for id := range w.bundles {
		if !processed.Has(id) {
			w.bundleIDPool <- id
		}
	}
}

// chooseIDsForBatch returns slice of IDs of SSTables that the batch consists of.
func (w *restoreWorker) chooseIDsForBatch(shards uint, size int) []string {
	var (
		batchSize = size * int(shards)
		takenIDs  []string
		done      bool
	)

	// Create batch
	for i := 0; i < batchSize; i++ {
		select {
		case id := <-w.bundleIDPool:
			takenIDs = append(takenIDs, id)
		default:
			done = true
		}

		if done {
			break
		}
	}

	return takenIDs
}

// batchFromIDs creates batch of SSTables with IDs present in ids.
func (w *restoreWorker) batchFromIDs(ids []string) []string {
	var batch []string

	for _, id := range ids {
		batch = append(batch, w.bundles[id]...)
	}

	return batch
}

func (w *restoreWorker) returnBatchToPool(ids []string) {
	for _, i := range ids {
		w.bundleIDPool <- i
	}
}

func (w *restoreWorker) drainBundleIDPool() []string {
	content := make([]string, 0)

	for len(w.bundleIDPool) > 0 {
		content = append(content, <-w.bundleIDPool)
	}

	return content
}

func isSystemKeyspace(keyspace string) bool {
	return strings.HasPrefix(keyspace, "system")
}

func sstableID(file string) string {
	return strings.SplitN(file, "-", 3)[1]
}
