package leader

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

// DBLeaseOptions configures the DB-backed leader election.
type DBLeaseOptions struct {
	Table     string
	LeaseID   string
	HolderID  string
	TTL       time.Duration
	Heartbeat time.Duration
	Retry     time.Duration
	OnStart   func(context.Context)
	OnStop    func()
}

// DBLease coordinates leadership using a shared Postgres table with TTL-based heartbeats.
type DBLease struct {
	db        *sql.DB
	logger    mlog.LoggerIFace
	table     string
	leaseID   string
	holderID  string
	ttl       time.Duration
	heartbeat time.Duration
	retry     time.Duration
	onStart   func(context.Context)
	onStop    func()

	acquireStmt string
	releaseStmt string

	cancel    context.CancelFunc
	done      chan struct{}
	isLeading atomic.Bool
}

// NewDBLease constructs a DBLease with sane defaults.
func NewDBLease(db *sql.DB, logger mlog.LoggerIFace, opts DBLeaseOptions) (*DBLease, error) {
	if db == nil {
		return nil, errors.New("db lease requires a database handle")
	}
	if opts.LeaseID == "" {
		return nil, errors.New("db lease requires a lease id")
	}
	if opts.HolderID == "" {
		return nil, errors.New("db lease requires a holder id")
	}
	if opts.TTL <= 0 {
		return nil, errors.New("db lease TTL must be positive")
	}

	table, err := normalizeIdentifier(opts.Table)
	if err != nil {
		return nil, err
	}

	heartbeat := opts.Heartbeat
	if heartbeat <= 0 {
		heartbeat = opts.TTL / 3
		if heartbeat <= 0 {
			heartbeat = time.Second
		}
	}

	retry := opts.Retry
	if retry <= 0 {
		retry = heartbeat / 2
		if retry <= 0 {
			retry = 500 * time.Millisecond
		}
	}

	acquireStmt := fmt.Sprintf(`
INSERT INTO %s (leaseid, holderid, renewedat)
VALUES ($1, $2, $3)
ON CONFLICT (leaseid) DO UPDATE
    SET holderid = EXCLUDED.holderid,
        renewedat = EXCLUDED.renewedat
    WHERE %s.holderid = EXCLUDED.holderid
       OR %s.renewedat < $4
`, table, table, table)

	releaseStmt := fmt.Sprintf(`DELETE FROM %s WHERE leaseid = $1 AND holderid = $2`, table)

	return &DBLease{
		db:         db,
		logger:     logger,
		table:      table,
		leaseID:    opts.LeaseID,
		holderID:   opts.HolderID,
		ttl:        opts.TTL,
		heartbeat:  heartbeat,
		retry:      retry,
		onStart:    opts.OnStart,
		onStop:     opts.OnStop,
		acquireStmt: acquireStmt,
		releaseStmt: releaseStmt,
		done:       make(chan struct{}),
	}, nil
}

// Start begins the heartbeat loop.
func (l *DBLease) Start(parent context.Context) error {
	if l.cancel != nil {
		return errors.New("db lease already started")
	}

	ctx, cancel := context.WithCancel(parent)
	l.cancel = cancel

	go l.loop(ctx)
	return nil
}

// Stop stops heartbeating and releases the lease if held.
func (l *DBLease) Stop() {
	if l.cancel != nil {
		l.cancel()
	}
	if l.done != nil {
		<-l.done
	}
}

func (l *DBLease) loop(ctx context.Context) {
	ticker := time.NewTicker(l.heartbeat)
	defer ticker.Stop()
	defer close(l.done)
	defer l.release(context.Background())

	l.evaluate(ctx)

	for {
		select {
		case <-ctx.Done():
			l.updateLeadership(false, ctx)
			return
		case <-ticker.C:
			l.evaluate(ctx)
		}
	}
}

func (l *DBLease) evaluate(ctx context.Context) {
	acquired, err := l.tryAcquire(ctx)
	if err != nil {
		if l.logger != nil {
			l.logger.Warn("db lease heartbeat failed", mlog.Err(err))
		}
		l.updateLeadership(false, ctx)
		l.sleep(ctx, l.retry)
		return
	}

	l.updateLeadership(acquired, ctx)
}

func (l *DBLease) tryAcquire(ctx context.Context) (bool, error) {
	now := time.Now()
	nowMillis := now.UnixMilli()
	expireBefore := now.Add(-l.ttl).UnixMilli()

	res, err := l.db.ExecContext(ctx, l.acquireStmt, l.leaseID, l.holderID, nowMillis, expireBefore)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (l *DBLease) updateLeadership(acquired bool, ctx context.Context) {
	if acquired {
		if l.isLeading.CompareAndSwap(false, true) {
			if l.logger != nil {
				l.logger.Info("db lease acquired", mlog.String("lease_id", l.leaseID))
			}
			if l.onStart != nil {
				go l.onStart(ctx)
			}
		}
		return
	}

	if l.isLeading.CompareAndSwap(true, false) {
		if l.logger != nil {
			l.logger.Info("db lease released", mlog.String("lease_id", l.leaseID))
		}
		if l.onStop != nil {
			l.onStop()
		}
	}
}

func (l *DBLease) release(ctx context.Context) {
	if l.db == nil {
		return
	}
	if _, err := l.db.ExecContext(ctx, l.releaseStmt, l.leaseID, l.holderID); err != nil {
		if l.logger != nil {
			l.logger.Debug("db lease release failed", mlog.Err(err))
		}
	}
}

func (l *DBLease) sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func normalizeIdentifier(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("db lease table name cannot be empty")
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, r := range lower {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return "", fmt.Errorf("db lease table name %q contains invalid character %q", name, r)
		}
	}
	return lower, nil
}

