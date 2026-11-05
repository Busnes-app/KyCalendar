package store

import (
	"context"
	"database/sql"
)

// Resolve the receipt's address to the same unique active user used by delivery. Stored
// sponsorship follows that stable user ID, not an old email or current calendar grant.
const cancellationRecipient = `EXISTS (SELECT 1 FROM users r WHERE r.id = ? AND r.status = 'active' AND r.role = 'user'
 AND m.recipient = ('mailto:' || LOWER(r.email))
 AND 1 = (SELECT COUNT(*) FROM users v WHERE LOWER(v.email) = LOWER(r.email) AND v.status = 'active' AND v.role = 'user'))`

func (c *calendarStore) SponsoredCancellationCopies(ctx context.Context, deliveryID, recipientID, uid string) ([]*CalendarObject, error) {
	return c.queryObjects(ctx, `SELECT `+objectCols+` FROM calendar_objects
WHERE invitation_sender <> '' AND invitation_recipient = ? AND uid = ?
AND EXISTS (SELECT 1 FROM scheduling_outbox m WHERE m.id = ? AND m.status = 'pending'
 AND m.method = 'CANCEL' AND m.sender_id = invitation_sender AND `+cancellationRecipient+`)
ORDER BY calendar_id, name`, recipientID, uid, deliveryID, recipientID)
}

// CancellationChange contains a loaded snapshot and prepared cancellation data; nil Next deletes.
type CancellationChange struct{ Previous, Next *CalendarObject }

// An ETag alone is insufficient: a paid rewrite can return to identical bytes. Recheck charge
// metadata and identity under calendar-total, and bind sponsored cleanup to its pending receipt.
func (c *calendarStore) checkCancellationCopy(ctx context.Context, tx *sql.Tx, deliveryID, recipientID string, o *CalendarObject) error {
	var n int
	err := tx.QueryRowContext(ctx, c.q(`SELECT COUNT(*) FROM calendar_objects o
JOIN scheduling_outbox m ON m.id = ? AND m.status = 'pending' AND m.method = 'CANCEL'
WHERE o.calendar_id = ? AND o.name = ? AND o.uid = ? AND o.etag = ?
AND o.invitation_sender = ? AND o.invitation_recipient = ?
AND (o.invitation_sender = '' OR (o.invitation_sender = m.sender_id AND o.invitation_recipient = ?))
AND `+cancellationRecipient),
		deliveryID, o.CalendarID, o.Name, o.UID, o.ETag, o.InvitationSender, o.InvitationRecipient, recipientID, recipientID).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPreconditionFailed
	}
	return nil
}

func (c *calendarStore) ApplyInternalCancellation(ctx context.Context, deliveryID, recipientID string, changes []CancellationChange, lim OwnerLimits) error {
	tx, err := c.store.lockedTx(ctx, "calendar-total")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, change := range changes {
		if err := c.checkCancellationCopy(ctx, tx, deliveryID, recipientID, change.Previous); err != nil {
			return err
		}
		if next := change.Next; next != nil && (next.CalendarID != change.Previous.CalendarID || next.Name != change.Previous.Name || next.UID != change.Previous.UID) {
			return ErrPreconditionFailed
		}
	}
	result, err := tx.ExecContext(ctx, c.q(`UPDATE scheduling_outbox AS m SET status = 'delivered', attempts = attempts + 1, body = ?, last_error = ''
WHERE m.id = ? AND m.status = 'pending' AND m.method = 'CANCEL' AND `+cancellationRecipient), []byte{}, deliveryID, recipientID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPreconditionFailed
	}
	// Release deletions before growing partial copies. Each ordinary quota check sees the
	// one queue-body exchange plus all preceding mutations, and any failure rolls it all back.
	for _, change := range changes {
		if change.Next == nil {
			old := change.Previous
			if err := c.deleteObjectPlanned(ctx, old.CalendarID, old.Name, old.ETag, lim, nil, tx); err != nil {
				return err
			}
		}
	}
	for _, change := range changes {
		if next := change.Next; next != nil {
			next.WriterID, next.DeliveryID, next.DeliveryRecipient = "", "", ""
			if _, err := c.putObjectPlanned(ctx, next, change.Previous.ETag, false, lim, nil, tx); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
