package locks

import (
	"context"
	"fmt"
	"strings"

	"github.com/phankieuphu/lumen/internal/driver"
)

const mysqlNote = "InnoDB row and table locks come from performance_schema.data_locks, " +
	"metadata locks (which hold off ALTER and DROP) from performance_schema.metadata_locks."

const (
	mysqlDataSQL = `SELECT LOCK_TYPE, LOCK_MODE, LOCK_STATUS, OBJECT_SCHEMA, OBJECT_NAME, INDEX_NAME, LOCK_DATA
FROM performance_schema.data_locks
WHERE THREAD_ID = PS_THREAD_ID(?)
ORDER BY LOCK_STATUS = 'GRANTED', LOCK_TYPE = 'RECORD', OBJECT_NAME, INDEX_NAME`
	mysqlMetaSQL = `SELECT LOCK_TYPE, LOCK_STATUS, OBJECT_SCHEMA, OBJECT_NAME
FROM performance_schema.metadata_locks
WHERE OWNER_THREAD_ID = PS_THREAD_ID(?) AND OBJECT_TYPE = 'TABLE'
ORDER BY LOCK_STATUS = 'GRANTED', OBJECT_NAME`
)

func mysqlLocks(ctx context.Context, s driver.Session, id int64) (Report, error) {
	if err := mysqlCheck(ctx, s); err != nil {
		return Report{}, err
	}
	data, err := query(ctx, s, mysqlDataSQL, id)
	if err != nil {
		return Report{}, fmt.Errorf("locks: read data_locks: %w", err)
	}
	meta, err := query(ctx, s, mysqlMetaSQL, id)
	if err != nil {
		return Report{}, fmt.Errorf("locks: read metadata_locks: %w", err)
	}
	r := Report{Note: mysqlNote}
	for _, row := range data {
		lockType, mode := text(row[0]), text(row[1])
		l := Lock{Mode: mode, Granted: text(row[2]) == "GRANTED", Object: qualified(text(row[3]), text(row[4]))}
		if lockType == "TABLE" {
			l.Type, l.Meaning = TypeTable, myTableMeaning(mode)
		} else {
			l.Type, l.Meaning = TypeRow, myRowMeaning(mode)
			l.Detail = strings.TrimSpace("index " + text(row[5]) + " " + rowKey(text(row[6])))
		}
		if !l.Granted {
			l.Meaning = "Waiting: another transaction holds a conflicting lock. " + l.Meaning
		}
		r.Locks = append(r.Locks, l)
	}
	for _, row := range meta {
		l := Lock{
			Type: TypeMetadata, Mode: text(row[0]), Granted: text(row[1]) == "GRANTED",
			Object: qualified(text(row[2]), text(row[3])), Meaning: myMetaMeaning(text(row[0])),
		}
		if !l.Granted {
			l.Meaning = "Waiting for other transactions using the table to finish. " + l.Meaning
		}
		r.Locks = append(r.Locks, l)
	}
	return r, nil
}

// mysqlCheck says why the lock tables cannot be read, with the fix. The
// tables only show in information_schema to users who may read them.
func mysqlCheck(ctx context.Context, s driver.Session) error {
	rows, err := query(ctx, s, `SELECT @@performance_schema`)
	if err != nil {
		return fmt.Errorf("locks: check performance_schema: %w", err)
	}
	if len(rows) == 0 || !truthy(rows[0][0]) {
		return fmt.Errorf("%w: start the server with performance_schema=ON", ErrNotEnabled)
	}
	rows, err = query(ctx, s, `SELECT COUNT(*) FROM information_schema.TABLES
WHERE TABLE_SCHEMA = 'performance_schema' AND TABLE_NAME IN ('data_locks', 'metadata_locks')`)
	if err != nil {
		return fmt.Errorf("locks: check performance_schema: %w", err)
	}
	if len(rows) == 0 || text(rows[0][0]) != "2" {
		return fmt.Errorf("%w: ask an administrator to run GRANT SELECT ON performance_schema.data_locks, and the same for performance_schema.metadata_locks, TO your user", ErrNoAccess)
	}
	return nil
}

func qualified(schema, name string) string {
	if schema == "" {
		return name
	}
	return schema + "." + name
}

// rowKey shows a row lock's key; InnoDB names the gap above the last row
// "supremum pseudo-record".
func rowKey(data string) string {
	switch {
	case data == "":
		return ""
	case strings.Contains(data, "supremum"):
		return "after the last row"
	default:
		return "key " + data
	}
}

func myTableMeaning(mode string) string {
	switch mode {
	case "IX":
		return "Intention to lock rows for writing. Others can still lock other rows; it blocks LOCK TABLES ... WRITE and table rebuilds."
	case "IS":
		return "Intention to lock rows for reading (SELECT ... FOR SHARE). Blocks only LOCK TABLES ... WRITE."
	case "X":
		return "The whole table, for writing. Blocks everyone else until you commit."
	case "S":
		return "The whole table, for reading. Blocks writers until you commit."
	}
	return "A table lock."
}

func myRowMeaning(mode string) string {
	shared := strings.HasPrefix(mode, "S")
	what := "for writing: others wait to change it or lock it"
	if shared {
		what = "for reading: others can read it but wait to change it"
	}
	switch {
	case strings.Contains(mode, "INSERT_INTENTION"):
		return "Waiting to insert into a gap another transaction has locked."
	case strings.Contains(mode, "GAP") && !strings.Contains(mode, "REC_NOT_GAP"):
		return "The gap before this row: others cannot insert into it until you commit."
	case strings.Contains(mode, "REC_NOT_GAP"):
		return "This row only, " + what + "."
	}
	return "This row and the gap before it, " + what + "; others cannot insert into that gap either."
}

func myMetaMeaning(t string) string {
	switch t {
	case "SHARED_READ":
		return "The table is in use for reading: ALTER TABLE and DROP wait until you commit."
	case "SHARED_WRITE":
		return "The table is in use for writing: ALTER TABLE and DROP wait until you commit."
	case "EXCLUSIVE":
		return "A schema change on the table: everyone else waits."
	}
	return "Keeps the table's definition from changing while you use it."
}
