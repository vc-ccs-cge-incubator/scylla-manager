package backup

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/pkg/errors"
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

func (s *Service) GetRestoreTargetSize(ctx context.Context, clusterID uuid.UUID, target RestoreTarget) (int64, error) {
	return 0, errors.New("TODO - implement")
}

func (s *Service) Restore(ctx context.Context, clusterID, taskID, runID uuid.UUID, target RestoreTarget) error {
	return errors.New("TODO - implement")
}
