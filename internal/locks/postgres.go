package locks

import (
	"context"
	"fmt"

	"github.com/phankieuphu/lumen/internal/driver"
)

// postgresNote says what pg_locks cannot show.
const postgresNote = "PostgreSQL keeps row locks on the rows themselves, not in this list: " +
	"the table lock and your transaction ID stand for them. Other sessions that change a row you changed wait for your transaction ID."

// pg_locks needs no privilege. A session's own virtual transaction ID is
// always held and says nothing, so it is left out.
const postgresSQL = `SELECT l.locktype, l.mode, l.granted,
       CASE WHEN l.relation IS NOT NULL THEN l.relation::regclass::text END,
       l.page, l.tuple, l.transactionid::text, l.objid
FROM pg_locks l
WHERE l.pid = $1 AND l.locktype <> 'virtualxid'
ORDER BY l.granted, l.locktype = 'transactionid', 4, l.mode`

func postgresLocks(ctx context.Context, s driver.Session, id int64) (Report, error) {
	rows, err := query(ctx, s, postgresSQL, id)
	if err != nil {
		return Report{}, fmt.Errorf("locks: read pg_locks: %w", err)
	}
	r := Report{Note: postgresNote}
	for _, row := range rows {
		locktype, mode, rel := text(row[0]), text(row[1]), text(row[3])
		l := Lock{Mode: mode, Granted: truthy(row[2])}
		switch locktype {
		case "relation":
			l.Type, l.Object, l.Meaning = TypeTable, rel, pgTableMeaning(mode)
		case "tuple":
			l.Type, l.Object = TypeRow, rel
			l.Detail = fmt.Sprintf("page %s, row %s", text(row[4]), text(row[5]))
			l.Meaning = "Queued for a row another transaction is changing."
		case "transactionid":
			l.Type, l.Object = TypeTransaction, "transaction "+text(row[6])
			if l.Granted {
				l.Meaning = "Your transaction's own ID. Anyone who changes a row you changed waits on it until you commit or roll back."
			} else {
				l.Meaning = "Waiting for this other transaction to commit or roll back: it changed a row this statement needs."
			}
		case "advisory":
			l.Type, l.Object = TypeAdvisory, "advisory lock "+text(row[7])
			l.Meaning = "Taken by pg_advisory_lock: held for the application, blocks only others asking for the same key."
		default:
			l.Type, l.Object = TypeOther, locktype
			if rel != "" {
				l.Object = locktype + " on " + rel
			}
			l.Meaning = "A lock PostgreSQL takes for its own work."
		}
		if !l.Granted && l.Type == TypeTable {
			l.Meaning = "Waiting: another transaction holds a lock on " + rel + " that conflicts. " + l.Meaning
		}
		r.Locks = append(r.Locks, l)
	}
	return r, nil
}

// pgTableMeaning explains a table lock mode: who takes it and what it
// blocks, from the conflict table in the PostgreSQL manual.
func pgTableMeaning(mode string) string {
	switch mode {
	case "AccessShareLock":
		return "Taken by reads. Blocks only ALTER TABLE, DROP, TRUNCATE, and VACUUM FULL."
	case "RowShareLock":
		return "Taken by SELECT ... FOR UPDATE or FOR SHARE. Blocks EXCLUSIVE locks and schema changes."
	case "RowExclusiveLock":
		return "Taken by INSERT, UPDATE, and DELETE. Others can still read and write other rows; it blocks ALTER, DROP, TRUNCATE, and CREATE INDEX until you commit."
	case "ShareUpdateExclusiveLock":
		return "Taken by VACUUM, ANALYZE, and CREATE INDEX CONCURRENTLY. Blocks schema changes and other maintenance on the table."
	case "ShareLock":
		return "Taken by CREATE INDEX. Blocks every write to the table until you commit."
	case "ShareRowExclusiveLock":
		return "Taken by CREATE TRIGGER and some ALTER TABLE forms. Blocks every write and other SHARE locks."
	case "ExclusiveLock":
		return "Blocks everything except plain reads until you commit."
	case "AccessExclusiveLock":
		return "Taken by ALTER TABLE, DROP, and TRUNCATE. Blocks everyone, reads included, until you commit or roll back."
	}
	return "A table lock."
}
