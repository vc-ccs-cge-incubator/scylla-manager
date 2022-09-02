package backup

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/pkg/errors"
	. "github.com/scylladb/scylla-manager/v3/pkg/service/backup/backupspec"
	"github.com/scylladb/scylla-manager/v3/pkg/util/inexlist/ksfilter"
	"github.com/scylladb/scylla-manager/v3/pkg/util/uuid"
)

// GetRestoreTarget converts runner properties into RestoreTarget.
func (s *Service) GetRestoreTarget(ctx context.Context, clusterID uuid.UUID, properties json.RawMessage) (RestoreTarget, error) {
	s.logger.Info(ctx, "GetRestoreTarget", "cluster_id", clusterID)

	var t RestoreTarget

	if err := json.Unmarshal(properties, &t); err != nil {
		return t, err
	}

	if t.Location == nil {
		return t, errors.New("missing location")
	}

	// Set default values
	if t.BatchSize == 0 {
		t.BatchSize = 2
	}
	if t.Parallel == 0 {
		t.Parallel = 1
	}

	client, err := s.scyllaClient(ctx, clusterID)
	if err != nil {
		return t, errors.Wrapf(err, "get client")
	}

	keyspaces, err := client.Keyspaces(ctx)
	if err != nil {
		return t, errors.Wrapf(err, "get keyspaces")
	}

	filter, err := ksfilter.NewFilter(t.Keyspace)
	if err != nil {
		return t, errors.Wrap(err, "create filter")
	}

	// Always restore missing schema
	systemSchemaUnit := Unit{
		Keyspace:  systemSchema,
		AllTables: true,
	}

	for _, keyspace := range keyspaces {
		tables, err := client.Tables(ctx, keyspace)
		if err != nil {
			return t, errors.Wrapf(err, "keyspace %s: get tables", keyspace)
		}
		// Do not filter out schema
		if keyspace == systemSchema {
			systemSchemaUnit.Tables = tables
		} else {
			filter.Add(keyspace, tables)
		}
	}

	v, err := filter.Apply(false)
	if err != nil {
		return t, errors.Wrap(err, "create units")
	}

	for _, u := range v {
		t.Units = append(t.Units, Unit{
			Keyspace:  u.Keyspace,
			Tables:    u.Tables,
			AllTables: u.AllTables,
		})
	}
	t.Units = append(t.Units, systemSchemaUnit)

	status, err := client.Status(ctx)
	if err != nil {
		return t, errors.Wrap(err, "get status")
	}
	// Check if for each location there is at least one host
	// living in either location's or local dc with access to it.
	for _, l := range t.Location {
		var (
			locationAndLocalStatus = status.Datacenter([]string{l.DC, s.config.LocalDC})
			remotePath             = l.RemotePath("")
		)
		if _, err := client.GetLiveNodesWithLocationAccess(ctx, locationAndLocalStatus, remotePath); err != nil {
			if strings.Contains(err.Error(), "NoSuchBucket") {
				return t, errors.New("specified bucket does not exist")
			}
			return t, errors.Wrap(err, "location is not accessible")
		}
	}

	return t, nil
}

// forEachRestoredManifest returns a wrapper for forEachManifest that iterates over manifest with
// cluster ID and snapshot tag specified in restore target.
func (s *Service) forEachRestoredManifest(clusterID uuid.UUID, snapshotTag string) func(context.Context, Location, func(ManifestInfoWithContent) error) error {
	return func(ctx context.Context, location Location, f func(content ManifestInfoWithContent) error) error {
		return s.forEachManifest(ctx, clusterID, []Location{location}, ListFilter{SnapshotTag: snapshotTag}, f)
	}
}

// Restore executes restore on a given target.
func (s *Service) Restore(ctx context.Context, clusterID, taskID, runID uuid.UUID, target RestoreTarget) error {
	s.logger.Info(ctx, "Restore",
		"cluster_id", clusterID,
		"task_id", taskID,
		"run_id", runID,
		"target", target,
	)

	run := &RestoreRun{
		ClusterID:   clusterID,
		TaskID:      taskID,
		ID:          runID,
		SnapshotTag: target.SnapshotTag,
		Stage:       StageRestoreInit,
	}

	// Get cluster name
	clusterName, err := s.clusterName(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "invalid cluster")
	}
	// Get the cluster client
	client, err := s.scyllaClient(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "get client proxy")
	}
	// Get cluster session
	clusterSession, err := s.clusterSession(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "get CQL cluster session")
	}
	defer clusterSession.Close()

	filter, err := ksfilter.NewFilter(target.Keyspace)
	if err != nil {
		return errors.Wrap(err, "create filter")
	}

	w := &restoreWorker{
		worker: worker{
			ClusterID:   clusterID,
			ClusterName: clusterName,
			TaskID:      taskID,
			RunID:       runID,
			Client:      client,
			Config:      s.config,
			Metrics:     s.metrics,
			Logger:      s.logger.Named("restore"),
		},
		managerSession:          s.session,
		clusterSession:          clusterSession,
		forEachRestoredManifest: s.forEachRestoredManifest(clusterID, target.SnapshotTag),
		filter:                  filter,
	}

	if target.Continue {
		if err := w.decorateWithPrevRun(ctx, run); err != nil {
			return err
		}

		w.InsertRun(ctx, run)
		// Update run with previous progress.
		if run.PrevID != uuid.Nil {
			w.clonePrevProgress(ctx, run)
		}

		w.Logger.Info(ctx, "Run after decoration",
			"run", *run,
		)
	} else {
		w.InsertRun(ctx, run)
	}

	if !target.Continue || run.PrevID == uuid.Nil {
		w.resumed = true
	}

	guardFunc := func(_ context.Context, _ *RestoreRun, _ RestoreTarget) error {
		return nil
	}
	stageFunc := map[RestoreStage]func(context.Context, *RestoreRun, RestoreTarget) error{
		StageRestoreInit:   guardFunc,
		StageRestoreSchema: w.restoreSchema,
		StageRestoreSize:   w.restoreSize,
		StageRestoreData:   w.restoreData,
		StageRestoreDone:   guardFunc,
	}
	logger := w.Logger

	for _, stage := range RestoreStageOrder() {
		if run.Stage.Index() <= stage.Index() {
			run.Stage = stage
			w.InsertRun(ctx, run)

			name := strings.ToLower(string(stage))
			w.Logger = logger.Named(name)

			if err := stageFunc[stage](ctx, run, target); err != nil {
				return errors.Wrapf(err, "restore failed at stage: %s", name)
			}
		}
	}

	return nil
}

// GetRestoreProgress aggregates progress for the restore run of the task
// and breaks it down by keyspace and table.json.
// If nothing was found scylla-manager.ErrNotFound is returned.
func (s *Service) GetRestoreProgress(ctx context.Context, clusterID, taskID, runID uuid.UUID) (RestoreProgress, error) {
	w := &restoreWorker{
		worker: worker{
			ClusterID: clusterID,
			TaskID:    taskID,
			RunID:     runID,
		},
		managerSession: s.session,
	}

	return w.getProgress(ctx)
}

func (s *Service) GetRestoreTargetSize(ctx context.Context, clusterID uuid.UUID, target RestoreTarget) (int64, error) {
	w := &restoreWorker{
		forEachRestoredManifest: s.forEachRestoredManifest(clusterID, target.SnapshotTag),
	}

	return w.getTargetSize(ctx, target)
}
