package backup

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/scylladb/scylla-manager/v3/pkg/util/uuid"
)

func (s *Service) GetRestoreTarget(ctx context.Context, clusterID uuid.UUID, properties json.RawMessage) (RestoreTarget, error) {
	return RestoreTarget{}, errors.New("TODO - implement")
}

func (s *Service) GetRestoreTargetSize(ctx context.Context, clusterID uuid.UUID, target RestoreTarget) (int64, error) {
	return 0, errors.New("TODO - implement")
}

func (s *Service) Restore(ctx context.Context, clusterID, taskID, runID uuid.UUID, target RestoreTarget) error {
	return errors.New("TODO - implement")
}
