package notifier

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grafana/grafana/pkg/infra/kvstore"
)

const KVNamespace = "alertmanager"

// State represents any of the two 'states' of the alertmanager. Notification log or Silences.
// MarshalBinary returns the binary representation of this internal state based on the protobuf.
type State interface {
	MarshalBinary() ([]byte, error)
}

// FileStore is in charge of persisting the alertmanager files to the database.
// It uses the KVstore table and encodes the files as a base64 string.
type FileStore struct {
	kv             *kvstore.NamespacedKVStore
	orgID          int64
	workingDirPath string
}

func NewFileStore(orgID int64, store kvstore.KVStore, workingDirPath string) *FileStore {
	return &FileStore{
		workingDirPath: workingDirPath,
		orgID:          orgID,
		kv:             kvstore.WithNamespace(store, orgID, KVNamespace),
	}
}

// FilepathFor returns the filepath to an Alertmanager file.
// If the file is already present on disk it no-ops.
// If not, it tries to read the database and if there's no file it no-ops.
// If there is a file in the database, it decodes it and writes to disk for Alertmanager consumption.
func (fs *FileStore) FilepathFor(ctx context.Context, filename string) (string, error) {
	// If a file is already present, we'll use that one and eventually save it to the database.
	// We don't need to do anything else.
	if fs.IsExists(filename) {
		return fs.pathFor(filename), nil
	}

	// Then, let's attempt to read it from the database.
	content, exists, err := fs.kv.Get(ctx, filename)
	if err != nil {
		return "", fmt.Errorf("error reading file '%s' from database: %w", filename, err)
	}

	// if it doesn't exist, let's no-op and let the Alertmanager create one. We'll eventually save it to the database.
	if !exists {
		return fs.pathFor(filename), nil
	}

	// If we have a file stored in the database, let's decode it and write it to disk to perform that initial load to memory.
	bytes, err := decode(content)
	if err != nil {
		return "", fmt.Errorf("error decoding file '%s': %w", filename, err)
	}

	if err := fs.WriteFileToDisk(filename, bytes); err != nil {
		return "", fmt.Errorf("error writing file %s: %w", filename, err)
	}

	return fs.pathFor(filename), err
}

// Persist takes care of persisting the binary representation of internal state to the database as a base64 encoded string.
func (fs *FileStore) Persist(ctx context.Context, filename string, st State) (int64, error) {
	var size int64

	bytes, err := st.MarshalBinary()
	if err != nil {
		return size, err
	}

	if err = fs.kv.Set(ctx, filename, encode(bytes)); err != nil {
		return size, err
	}

	return int64(len(bytes)), err
}

// IsExists verifies if the file exists or not.
func (fs *FileStore) IsExists(fn string) bool {
	_, err := os.Stat(fs.pathFor(fn))
	return os.IsExist(err)
}

// WriteFileToDisk writes a file with the provided name and contents to the Alertmanager working directory with the default grafana permission.
func (fs *FileStore) WriteFileToDisk(fn string, content []byte) error {
	// Ensure the working directory is created
	err := os.MkdirAll(fs.workingDirPath, 0750)
	if err != nil {
		return fmt.Errorf("unable to create the working directory %q: %s", fs.workingDirPath, err)
	}

	return os.WriteFile(fs.pathFor(fn), content, 0644)
}

func (fs *FileStore) pathFor(fn string) string {
	return filepath.Join(fs.workingDirPath, fn)
}

func decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

func encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
