package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/scylladb/scylla-manager/v3/pkg/util/uuid"
)

// TODO: should we introduce separate restore metrics?

// UpdateRestoreProgress updates "files_{uploaded,skipped,failed}_bytes" metrics.
// Host is understood as the host on which the manifest was taken.
func (m BackupMetrics) UpdateRestoreProgress(clusterID uuid.UUID, keyspace, table, host string, size, uploaded, skipped, failed int64) {
	l := prometheus.Labels{
		"cluster":  clusterID.String(),
		"keyspace": keyspace,
		"table":    table,
		"host":     host,
	}
	m.filesSizeBytes.With(l).Add(float64(size))
	m.filesUploadedBytes.With(l).Add(float64(uploaded))
	m.filesSkippedBytes.With(l).Add(float64(skipped))
	m.filesFailedBytes.With(l).Add(float64(failed))
}
