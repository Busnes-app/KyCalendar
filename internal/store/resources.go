package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Busnes-app/kycalendar/internal/resource"
)

var (
	// ErrUnknownGroup is a resource naming a group that does not exist.
	ErrUnknownGroup = errors.New("store: no such group")
	// ErrBadApprover is a resource approver who is not an active everyday user.
	ErrBadApprover = errors.New("store: approvers must be active everyday users")
)

// ResourceStore keeps the resource definitions. A resource is never deleted.
type ResourceStore interface {
	ListResources(ctx context.Context) ([]*resource.Resource, error)
	GetResource(ctx context.Context, id string) (*resource.Resource, error)
	// CreateResource and UpdateResource refuse a name another resource holds in any case
	// (ErrAlreadyExists), ErrUnknownGroup and ErrBadApprover, and then write nothing.
	CreateResource(ctx context.Context, r *resource.Resource) error
	UpdateResource(ctx context.Context, r *resource.Resource) error

	// Bookable lists the enabled resources userID may book.
	Bookable(ctx context.Context, userID string) ([]*resource.Resource, error)
	// Busy is a resource's busy spans overlapping [from, to), without event uid of calendarID.
	Busy(ctx context.Context, id string, from, to int64, calendarID, uid string) ([]BusySpan, error)
	// BookingsIn is calendarID's bookings by object name, then resource ID.
	BookingsIn(ctx context.Context, calendarID string) (map[string]map[string]*ResourceBooking, error)
	// Requests lists the pending bookings of the resources approverID approves.
	Requests(ctx context.Context, approverID string) ([]*ResourceRequest, error)
	IsApprover(ctx context.Context, userID string) (bool, error)
	// Decide answers a pending booking as one of its resource's approvers (ErrNotFound for
	// anyone else, ErrBookingState when it is no longer pending) and writes the answer into
	// the event.
	Decide(ctx context.Context, id, approverID string, accept bool) error
}

type resourceStore struct{ store *SQLStore }

func (s *resourceStore) q(query string) string { return s.store.rebind(query) }

const resourceCols = `id, kind, name, location, capacity, tags, audience, approval, max_minutes, horizon_days, enabled, created_at, updated_at`

type rowsQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *resourceStore) load(ctx context.Context, q rowsQuerier, where string, args ...any) ([]*resource.Resource, error) {
	rows, err := q.QueryContext(ctx, s.q(`SELECT `+resourceCols+` FROM resources `+where+` ORDER BY lower(name), id`), args...)
	if err != nil {
		return nil, err
	}
	var out []*resource.Resource
	byID := map[string]*resource.Resource{}
	for rows.Next() {
		var r resource.Resource
		var tags string
		if err := rows.Scan(&r.ID, &r.Kind, &r.Name, &r.Location, &r.Capacity, &tags, &r.Audience, &r.Approval,
			&r.MaxMinutes, &r.HorizonDays, &r.Enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(tags), &r.Tags); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, &r)
		byID[r.ID] = &r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, link := range []struct {
		table, col string
		field      func(*resource.Resource) *[]string
	}{
		{"resource_groups", "group_id", func(r *resource.Resource) *[]string { return &r.Groups }},
		{"resource_approvers", "user_id", func(r *resource.Resource) *[]string { return &r.Approvers }},
	} {
		rows, err := q.QueryContext(ctx, s.q(`SELECT resource_id, `+link.col+` FROM `+link.table+` ORDER BY `+link.col))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var rid, id string
			if err := rows.Scan(&rid, &id); err != nil {
				rows.Close()
				return nil, err
			}
			if r := byID[rid]; r != nil {
				*link.field(r) = append(*link.field(r), id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *resourceStore) ListResources(ctx context.Context) ([]*resource.Resource, error) {
	return s.load(ctx, s.store.db, "")
}

func (s *resourceStore) GetResource(ctx context.Context, id string) (*resource.Resource, error) {
	rs, err := s.load(ctx, s.store.db, "WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		return nil, ErrNotFound
	}
	return rs[0], nil
}

func (s *resourceStore) CreateResource(ctx context.Context, r *resource.Resource) error {
	return s.write(ctx, r, true)
}

func (s *resourceStore) UpdateResource(ctx context.Context, r *resource.Resource) error {
	return s.write(ctx, r, false)
}

func (s *resourceStore) write(ctx context.Context, r *resource.Resource, create bool) error {
	tx, err := s.store.lockedTx(ctx, "resources")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var taken int
	if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM resources WHERE lower(name) = lower(?) AND id <> ?`), r.Name, r.ID).Scan(&taken); err != nil {
		return err
	}
	if taken > 0 {
		return ErrAlreadyExists
	}
	for _, g := range r.Groups {
		var n int
		if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM groups WHERE id = ?`), g).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrUnknownGroup
		}
	}
	for _, u := range r.Approvers {
		var n int
		if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM users WHERE id = ? AND status = 'active' AND role <> 'admin'`), u).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrBadApprover
		}
	}
	tags, err := json.Marshal(append([]string{}, r.Tags...))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	r.UpdatedAt = now
	if create {
		r.CreatedAt = now
		_, err = tx.ExecContext(ctx, s.q(`INSERT INTO resources (`+resourceCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			r.ID, r.Kind, r.Name, r.Location, r.Capacity, string(tags), r.Audience, r.Approval, r.MaxMinutes, r.HorizonDays, r.Enabled, now, now)
	} else {
		var res sql.Result
		res, err = tx.ExecContext(ctx, s.q(`UPDATE resources SET kind = ?, name = ?, location = ?, capacity = ?, tags = ?, audience = ?, approval = ?,
max_minutes = ?, horizon_days = ?, enabled = ?, updated_at = ? WHERE id = ?`),
			r.Kind, r.Name, r.Location, r.Capacity, string(tags), r.Audience, r.Approval, r.MaxMinutes, r.HorizonDays, r.Enabled, now, r.ID)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrNotFound
			}
		}
	}
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return err
	}
	for _, link := range []struct {
		table, col string
		ids        []string
	}{{"resource_groups", "group_id", r.Groups}, {"resource_approvers", "user_id", r.Approvers}} {
		if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM `+link.table+` WHERE resource_id = ?`), r.ID); err != nil {
			return err
		}
		for _, id := range link.ids {
			if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO `+link.table+` (resource_id, `+link.col+`) VALUES (?, ?)`), r.ID, id); err != nil {
				return err
			}
		}
	}
	slices.Sort(r.Groups)
	slices.Sort(r.Approvers)
	return tx.Commit()
}
