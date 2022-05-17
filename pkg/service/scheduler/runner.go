// Copyright (C) 2017 ScyllaDB

package scheduler

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/scylladb/scylla-manager/v3/pkg/util/uuid"
)

// RunResult returns an error and a warning from a run.
type RunResult struct { // nolint: errname
	Err        error
	WarningErr error
}

// Is compares a RunResult's error with a target error.
func (r RunResult) Is(target error) bool {
	return errors.Is(r.Err, target)
}

// Error returns the error as a string.
func (r RunResult) Error() string {
	if r.Err != nil {
		return r.Err.Error()
	}
	return ""
}

// Runner is a glue between scheduler and agents doing work.
// There can be one Runner per TaskType registered in Service.
// Run ID needs to be preserved whenever agent wants to persist the running state.
// The run can be stopped by cancelling the context.
// In that case runner must return error reported by the context.
type Runner interface {
	Run(ctx context.Context, clusterID, taskID, runID uuid.UUID, properties json.RawMessage) *RunResult
}
