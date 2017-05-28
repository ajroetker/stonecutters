package lock

import (
	"context"
	"errors"
	"time"

	"github.com/coreos/etcd/clientv3"
)

var (
	TxnError            = errors.New("lock: error running put-lease txn")
	PutSucceededFailure = errors.New("lock: key already registered")
	GetIdFailure        = errors.New("lock: failed to get identifier from list")
	VerificationError   = errors.New("lock: k-v values do not match txn request") // very unlikely but strange error
	LeaseFailure        = errors.New("lock: error creating lease keep alive for key")
	defaultTimeout      = int64(60)
)

type Member struct {
	Key   string // Identifier granted
	Value string // Hosname
}

// GetID iterates over the passed 'ids' and attempts to claim one in
// etcd with a Lease which is persisted until the context is closed.
// If the list of ids are all claimed, returns GetIdFailure error with the
// expectation the caller will handle managing the id list retrys.
func GetID(c *clientv3.Client, ctx context.Context, leaseID clientv3.LeaseID,
	name string, ids []string) (string, error) {
	for _, id := range ids {
		txn, err := kvPutLease(c, ctx, leaseID, id, name)
		if err != nil {
			// skip to next id
			continue
		} else if txn.Succeeded {
			v := verifyKvPair(c, id, name)
			if v {
				return id, nil
			} else {
				return "", VerificationError
			}
		}
	}
	return "", GetIdFailure
}

func Members(c *clientv3.Client, ids []string) ([]Member, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	members := make([]Member, 0)

	for _, id := range ids {
		got, err := c.Get(ctx, id)
		if err == nil {
			if len(got.Kvs) > 0 {
				m := Member{Key: id, Value: string(got.Kvs[0].Value)}
				members = append(members, m)
			}
		} else {
			return nil, err
		}

	}
	return members, nil
}

// Lease Functionality
func createKeepAliveLease(c *clientv3.Client, ctx context.Context) (clientv3.LeaseID, <-chan *clientv3.LeaseKeepAliveResponse, error) {
	lease := clientv3.NewLease(c)

	id, err := acquireLeaseID(lease, ctx, defaultTimeout)

	keepAlive, err := lease.KeepAlive(ctx, id)
	if err != nil {
		return 0, nil, err
	}

	return id, keepAlive, nil
}

func acquireLeaseID(lease clientv3.Lease, ctx context.Context, timeout int64) (clientv3.LeaseID, error) {
	res, err := lease.Grant(ctx, timeout)
	if err != nil {
		return 0, err
	}
	return res.ID, nil
}

func revokeLease(client *clientv3.Client, ctx context.Context, leaseID clientv3.LeaseID) error {
	_, err := client.Revoke(ctx, leaseID)
	return err
}

// kvPutLease writes a key-val pair with a lease given that the key is not already in use.
// If the key exists the Txn fails, if it does not exist they key-val is Put.
func kvPutLease(kvc clientv3.KV, ctx context.Context, leaseID clientv3.LeaseID, key, val string) (*clientv3.TxnResponse, error) {
	resp, err := kvc.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), "=", 0)).
		Then(clientv3.OpPut(key, val, clientv3.WithLease(leaseID))).
		Commit()
	if err != nil {
		return nil, err
	}
	if resp.Succeeded == false {
		return nil, PutSucceededFailure
	}
	return resp, nil
}

// verifyKvPair returns true if expected key-value strings match their expected values
func verifyKvPair(client *clientv3.Client, ek, ev string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := client.Get(ctx, ek)
	if err != nil {
		return false
	}
	if len(got.Kvs) > 0 {
		if string(got.Kvs[0].Value) == ev {
			return true
		}
	}
	return false
}
