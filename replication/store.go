package replication

import (
	"encoding/binary"
	"encoding/json"
	"errors"

	"github.com/hashicorp/raft"
	bolt "go.etcd.io/bbolt"
)

// Persistence decision (CP7B-1 spike):
//
// Raft term/vote state, the replicated log and snapshot coordination are
// stored by BoltStore - a small bbolt-backed raft.LogStore + raft.StableStore
// kept physically and logically separate from any product database.
//
// The mature alternative normally paired with hashicorp/raft
// (hashicorp/raft-boltdb) uses the same bbolt engine; we own a minimal store
// here so the persistence abstraction stays swappable and is not part of the
// public replicated-operation contract. A SQLite-backed store (e.g.
// modernc.org/sqlite) was rejected for the spike: it would add a full SQL
// engine to a dependency-light core, and Raft persistence should not inherit
// a product database. bbolt is pure Go, cgo-free, mature and minimal, and it
// keeps durability concerns separate from product state.

const (
	bucketLog    = "log"
	bucketStable = "stable"
)

// BoltStore implements raft.LogStore and raft.StableStore on bbolt.
type BoltStore struct {
	db *bolt.DB
}

var (
	_ raft.LogStore    = (*BoltStore)(nil)
	_ raft.StableStore = (*BoltStore)(nil)
)

// NewBoltStore opens (or creates) a durable Raft store at path.
func NewBoltStore(path string) (*BoltStore, error) {
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketLog)); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists([]byte(bucketStable))
		return err
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &BoltStore{db: db}, nil
}

// Close releases the underlying database.
func (s *BoltStore) Close() error { return s.db.Close() }

// FirstIndex implements raft.LogStore.
func (s *BoltStore) FirstIndex() (uint64, error) {
	var idx uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(bucketLog)).Cursor()
		k, _ := c.First()
		if k == nil {
			return nil
		}
		idx = bytesToUint64(k)
		return nil
	})
	return idx, err
}

// LastIndex implements raft.LogStore.
func (s *BoltStore) LastIndex() (uint64, error) {
	var idx uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(bucketLog)).Cursor()
		k, _ := c.Last()
		if k == nil {
			return nil
		}
		idx = bytesToUint64(k)
		return nil
	})
	return idx, err
}

// GetLog implements raft.LogStore.
func (s *BoltStore) GetLog(index uint64, log *raft.Log) error {
	return s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte(bucketLog)).Get(uint64ToBytes(index))
		if v == nil {
			return raft.ErrLogNotFound
		}
		return json.Unmarshal(v, log)
	})
}

// StoreLog implements raft.LogStore.
func (s *BoltStore) StoreLog(log *raft.Log) error {
	return s.storeLogs([]*raft.Log{log})
}

// StoreLogs implements raft.LogStore.
func (s *BoltStore) StoreLogs(logs []*raft.Log) error {
	return s.storeLogs(logs)
}

func (s *BoltStore) storeLogs(logs []*raft.Log) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketLog))
		for _, l := range logs {
			v, err := json.Marshal(l)
			if err != nil {
				return err
			}
			if err := b.Put(uint64ToBytes(l.Index), v); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteRange implements raft.LogStore.
func (s *BoltStore) DeleteRange(min, max uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketLog))
		c := b.Cursor()
		for k, _ := c.Seek(uint64ToBytes(min)); k != nil && bytesToUint64(k) <= max; k, _ = c.Next() {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// Set implements raft.StableStore.
func (s *BoltStore) Set(key, val []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketStable)).Put(key, val)
	})
}

// Get implements raft.StableStore.
func (s *BoltStore) Get(key []byte) ([]byte, error) {
	var out []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte(bucketStable)).Get(key)
		if v == nil {
			return errors.New("not found")
		}
		out = append([]byte(nil), v...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetUint64 implements raft.StableStore.
func (s *BoltStore) SetUint64(key []byte, val uint64) error {
	return s.Set(key, uint64ToBytes(val))
}

// GetUint64 implements raft.StableStore.
func (s *BoltStore) GetUint64(key []byte) (uint64, error) {
	v, err := s.Get(key)
	if err != nil {
		return 0, err
	}
	return bytesToUint64(v), nil
}

func uint64ToBytes(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func bytesToUint64(b []byte) uint64 {
	if len(b) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}
