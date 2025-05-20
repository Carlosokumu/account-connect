package persistence

import (
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

type AccountConnectDb struct {
	Db *bolt.DB
}

func (blt *AccountConnectDb) Create() error {
	db, err := bolt.Open("account_connect.db", 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return fmt.Errorf("Failed to open bbolt db %v", err)
	}
	blt.Db = db
	return err
}

func (blt *AccountConnectDb) Close() error {
	return blt.Db.Close()
}

func (blt *AccountConnectDb) RegisterBucket(bucketname string) error {
	return blt.Db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucketname))
		return err
	})
}

func (blt *AccountConnectDb) Put(bucket string, key string, value string) error {
	return blt.Db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s not found", bucket)
		}
		err := b.Put([]byte(key), []byte(value))
		if err != nil {
			return err
		}
		return nil
	})
}

func (blt *AccountConnectDb) Get(bucket string, key string) ([]byte, error) {
	var value []byte
	err := blt.Db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s not found", bucket)
		}
		v := b.Get([]byte(key))
		if v != nil {
			value = make([]byte, len(v))
			copy(value, v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return value, nil
}
