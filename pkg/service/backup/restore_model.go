package backup

import (
	"reflect"
	"time"

	"github.com/gocql/gocql"
	"github.com/scylladb/gocqlx/v2"
	. "github.com/scylladb/scylla-manager/v3/pkg/service/backup/backupspec"
	"github.com/scylladb/scylla-manager/v3/pkg/util/uuid"
)

// RestoreTarget specifies what data should be restored and from which locations.
type RestoreTarget struct {
	Location    []Location `json:"location"`
	Keyspace    []string   `json:"keyspace"`
	SnapshotTag string     `json:"snapshot_tag"`
	BatchSize   int        `json:"batch_size"`
	Parallel    int        `json:"parallel"`
	Continue    bool       `json:"continue"`
}

// RestoreRun tracks restore progress, shares ID with scheduler.Run that initiated it.
type RestoreRun struct {
	ClusterID uuid.UUID
	TaskID    uuid.UUID
	ID        uuid.UUID

	PrevID       uuid.UUID
	ManifestPath string // marks currently processed manifest
	Keyspace     string `db:"keyspace_name"` // marks currently processed keyspace
	Table        string `db:"table_name"`    // marks currently processed table
	SnapshotTag  string
	Stage        RestoreStage

	Units []RestoreUnit // cache that's initialized once for entire task
}

// RestoreUnit represents restored keyspace and its tables with their size.
type RestoreUnit struct {
	Keyspace string `db:"keyspace_name"`
	Tables   []RestoreTable
}

func (u RestoreUnit) MarshalUDT(name string, info gocql.TypeInfo) ([]byte, error) {
	f := gocqlx.DefaultMapper.FieldByName(reflect.ValueOf(u), name)
	return gocql.Marshal(info, f.Interface())
}

func (u *RestoreUnit) UnmarshalUDT(name string, info gocql.TypeInfo, data []byte) error {
	f := gocqlx.DefaultMapper.FieldByName(reflect.ValueOf(u), name)
	return gocql.Unmarshal(info, data, f.Addr().Interface())
}

// RestoreTable represents restored table and its size.
type RestoreTable struct {
	Table string `db:"table_name"`
	Size  int64
}

func (t RestoreTable) MarshalUDT(name string, info gocql.TypeInfo) ([]byte, error) {
	f := gocqlx.DefaultMapper.FieldByName(reflect.ValueOf(t), name)
	return gocql.Marshal(info, f.Interface())
}

func (t *RestoreTable) UnmarshalUDT(name string, info gocql.TypeInfo, data []byte) error {
	f := gocqlx.DefaultMapper.FieldByName(reflect.ValueOf(t), name)
	return gocql.Unmarshal(info, data, f.Addr().Interface())
}

// RestoreRunProgress describes restore progress (like in RunProgress) of
// already started download of SSTables with specified IDs to host.
type RestoreRunProgress struct {
	ClusterID uuid.UUID
	TaskID    uuid.UUID
	RunID     uuid.UUID

	ManifestPath string
	Keyspace     string `db:"keyspace_name"`
	Table        string `db:"table_name"`
	Host         string // IP of the node to which SSTables are downloaded.
	AgentJobID   int64

	ManifestIP  string   // IP of the node on which manifest was taken.
	SSTableID   []string `db:"sstable_id"`
	StartedAt   *time.Time
	CompletedAt *time.Time
	Error       string
	Size        int64
	Uploaded    int64
	Skipped     int64
	Failed      int64
}
