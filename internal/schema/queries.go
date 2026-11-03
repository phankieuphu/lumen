package schema

import "github.com/phankieuphu/lumen/internal/driver"

// catalog holds the two queries each engine answers. Both return seven
// columns in the order documented on readColumns and readForeignKeys.
type catalog struct {
	columns     string
	foreignKeys string
}

var queries = map[driver.Dialect]catalog{
	driver.Postgres: {
		columns: `SELECT n.nspname, c.relname,
       CASE WHEN c.relkind IN ('v', 'm') THEN 'view' ELSE 'table' END,
       a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
       EXISTS (SELECT 1 FROM pg_index i
               WHERE i.indrelid = c.oid AND i.indisprimary AND a.attnum = ANY (i.indkey))
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f')
  AND NOT c.relispartition
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
  AND n.nspname NOT LIKE 'pg\_temp\_%'
ORDER BY n.nspname, c.relname, a.attnum`,
		foreignKeys: `SELECT con.conname, fn.nspname, fc.relname, fa.attname, tn.nspname, tc.relname, ta.attname
FROM pg_constraint con
JOIN pg_class fc ON fc.oid = con.conrelid
JOIN pg_namespace fn ON fn.oid = fc.relnamespace
JOIN pg_class tc ON tc.oid = con.confrelid
JOIN pg_namespace tn ON tn.oid = tc.relnamespace
CROSS JOIN LATERAL unnest(con.conkey, con.confkey) WITH ORDINALITY AS k(fcol, tcol, ord)
JOIN pg_attribute fa ON fa.attrelid = con.conrelid AND fa.attnum = k.fcol
JOIN pg_attribute ta ON ta.attrelid = con.confrelid AND ta.attnum = k.tcol
WHERE con.contype = 'f'
  AND NOT fc.relispartition
  AND fn.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY fn.nspname, fc.relname, con.conname, k.ord`,
	},
	// MySQL reads the connection's database, or every user schema when the
	// profile names none.
	driver.MySQL: {
		columns: `SELECT c.TABLE_SCHEMA, c.TABLE_NAME,
       CASE WHEN t.TABLE_TYPE = 'VIEW' THEN 'view' ELSE 'table' END,
       c.COLUMN_NAME, c.COLUMN_TYPE, c.IS_NULLABLE = 'YES', c.COLUMN_KEY = 'PRI'
FROM information_schema.COLUMNS c
JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME
WHERE c.TABLE_SCHEMA = COALESCE(DATABASE(), c.TABLE_SCHEMA)
  AND c.TABLE_SCHEMA NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
ORDER BY c.TABLE_SCHEMA, c.TABLE_NAME, c.ORDINAL_POSITION`,
		foreignKeys: `SELECT k.CONSTRAINT_NAME, k.TABLE_SCHEMA, k.TABLE_NAME, k.COLUMN_NAME,
       k.REFERENCED_TABLE_SCHEMA, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME
FROM information_schema.KEY_COLUMN_USAGE k
WHERE k.REFERENCED_TABLE_NAME IS NOT NULL
  AND k.TABLE_SCHEMA = COALESCE(DATABASE(), k.TABLE_SCHEMA)
  AND k.TABLE_SCHEMA NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
ORDER BY k.TABLE_SCHEMA, k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`,
	},
	driver.SQLite: {
		columns: `SELECT 'main', m.name, m.type, p.name, p.type, p."notnull" = 0, p.pk > 0
FROM sqlite_master m
JOIN pragma_table_info(m.name) p
WHERE m.type IN ('table', 'view') AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
ORDER BY m.name, p.cid`,
		foreignKeys: `SELECT 'fk_' || m.name || '_' || f.id, 'main', m.name, f."from", 'main', f."table", COALESCE(f."to", '')
FROM sqlite_master m
JOIN pragma_foreign_key_list(m.name) f
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
ORDER BY m.name, f.id, f.seq`,
	},
}
