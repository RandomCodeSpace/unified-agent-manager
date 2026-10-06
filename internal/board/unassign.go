package board

import (
	"context"
	"fmt"
)

// Unassign moves every card of projectID, a Project being removed, to the
// read-only Unassigned list, keeping its tree, comments and history. The
// Project's Tasks are gone with it, so its pending requests are withdrawn
// and a hold still open ends as an ended attempt. Its epics' approvals and
// its pauses were given for its repository, so they go too. It returns the
// number of cards moved.
func (s *Store) Unassign(ctx context.Context, projectID string) (int, error) {
	if projectID == "" {
		return 0, errReadOnly
	}
	count := 0
	_, err := s.write(ctx, func(t *txn) error {
		count = 0
		o, err := t.outline(projectID)
		if err != nil {
			return err
		}
		for _, n := range o.order {
			if n.HeldBy != "" {
				hold, err := t.openHold(n)
				if err != nil {
					return err
				}
				if err := t.releaseHold(n, ReleaseEnded, StatusTodo, ""); err != nil {
					return err
				}
				if _, err := t.addComment(n, AuthorUAM, "", fmt.Sprintf("attempt #%d ended", hold.Attempt), true, false); err != nil {
					return err
				}
			}
			if err := t.withdraw(n, false); err != nil {
				return err
			}
			if err := t.exec(`UPDATE cards SET project_id = '', paused = '', updated_at = ? WHERE id = ?`, stamp(t.now), n.ID); err != nil {
				return err
			}
			if err := t.exec(`DELETE FROM runs WHERE epic_id = ?`, n.ID); err != nil {
				return err
			}
			t.removed(projectID, n.ID)
			t.changed("", n.ID)
			count++
		}
		return nil
	})
	return count, err
}
