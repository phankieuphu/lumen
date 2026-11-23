# Lint rules

The editor marks SQL that is likely wrong, dangerous, or slow while you type.
Rules run in `internal/lint` on the shared syntax tree from `internal/sqlparse`,
so each rule works the same on every dialect with a syntax tree adapter:
PostgreSQL (libpg_query), MySQL (the TiDB parser), and SQLite (read with the
PostgreSQL parser, best effort). Statements that do not parse get syntax errors
instead.

lumen is an interactive client, so the set favours real bugs and dangerous
writes over style: a rule that fires on everyday exploring (`SELECT *`,
`LIMIT` without `ORDER BY`) is noise, and noise gets ignored.

## Correctness (L0xx)

| Rule | Severity | Marks | Finds |
| --- | --- | --- | --- |
| L001 | error | `UPDATE` / `DELETE` | `UPDATE` or `DELETE` without `WHERE`: every row changes. |
| L002 | error | `NULL` | `= NULL`, `<> NULL`, `!= NULL`: never true; use `IS [NOT] NULL`. |
| L003 | warning | `NOT IN` | `NOT IN (subquery)`: no rows at all if the subquery returns a NULL; use `NOT EXISTS`. A warning, not an error, because whether the subquery can return NULL depends on the schema. |
| L004 | error | `FROM` | A comma join with no `WHERE`: a cross join. Explicit `JOIN ... ON` and `CROSS JOIN` are not flagged on PostgreSQL; on MySQL a `JOIN` without `ON` reads like a comma join to the parser and is not flagged either. |
| L005 | error | the `NULL` | A `NULL` in a `NOT IN (...)` list: no row ever matches. |
| L006 | warning | the offset | `OFFSET` without `ORDER BY`: rows come in no set order, so pages repeat or skip rows. `OFFSET 0` is not flagged. |
| L007 | error | the `OR` | `UPDATE` / `DELETE` whose `WHERE` mixes `AND` and `OR` outside parentheses: `a AND b OR c` means `(a AND b) OR c`, often far more rows than meant. Read from the tokens, since the tree keeps no parentheses; `BETWEEN ... AND`, `CASE`, and subqueries are skipped. On MySQL, `&&`, `||`, and `XOR` count too. |

## Performance (L1xx)

| Rule | Severity | Marks | Finds |
| --- | --- | --- | --- |
| L101 | info / warning | `*` | `SELECT *` and `t.*`, except inside `EXISTS (...)` and in `count(*)`. Info on its own, since looking at data this way is normal; a warning in `INSERT ... SELECT *`, which breaks when either table changes. |
| L102 | warning | the pattern | `LIKE` / `ILIKE` with a pattern starting with `%`: no index use. |
| L103 | warning | the function | A function around a column in a `WHERE` comparison, `LIKE`, or `IN`: no index use on that column. |
| L104 | warning | `RAND` / `RANDOM` | `ORDER BY RAND()` / `RANDOM()`: sorts the whole table. |
| L105 | warning | the offset | `OFFSET` above 10,000: rows read and thrown away; use keyset paging. |
| L106 | warning | `INSERT` | `INSERT` with `VALUES` or `SELECT` but no column list. |

## Safety (L2xx)

These depend on the connection's settings, not only the SQL.

| Rule | Severity | Marks | Finds |
| --- | --- | --- | --- |
| L201 | info | the verb | `DROP`, `ALTER`, or `TRUNCATE` on a production connection: it will ask for confirmation and be audited. Not shown when L202 marks the statement. |
| L202 | error | the verb | A statement a read-only connection will refuse. It asks the query classifier, so it marks exactly what read-only mode blocks, including statements the parser cannot read. |

## Known gaps

- L006 and L105 see only literal offsets: `OFFSET $1` is not checked.
- L006 and L105 do not see the `OFFSET` of a `UNION` or other set operation.
- L007 does not look inside a data-changing CTE
  (`WITH d AS (DELETE ... WHERE a AND b OR c RETURNING id) SELECT ...`).

## Later

These need the schema cache and wait for a later phase:

- Unknown table or column.
- Implicit type cast on an indexed column (`varchar_col = 123` on MySQL).
- L103 only when the column has an index.
- A comment to silence a rule for one statement (`-- lumen:ignore L101`).

Left out on purpose as too noisy for ad-hoc SQL: `LIMIT` without `ORDER BY`,
`UNION` where `UNION ALL` would do, and `SELECT` without `LIMIT` (the result
grid already caps rows).

Each rule has positive and negative cases in `internal/lint/lint_test.go`,
run on PostgreSQL and MySQL.
