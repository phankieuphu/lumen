# Lint rules

The editor marks SQL that is likely wrong, dangerous, or slow while you type.
Rules run in `internal/lint` on the shared syntax tree from `internal/sqlparse`,
so each rule works the same on every dialect with a syntax tree adapter:
PostgreSQL (libpg_query), MySQL (the TiDB parser), and SQLite (read with the
PostgreSQL parser, best effort). Statements that do not parse get syntax errors
instead.

> **Status:** this is a proposed first set, chosen while the design document
> was not at hand (the Phase 2 lint task names it as the source). Check it
> against the design document before ticking that task in docs/plan.md.

| Rule | Severity | Marks | Finds |
| --- | --- | --- | --- |
| L001 | error | `UPDATE` / `DELETE` | `UPDATE` or `DELETE` without `WHERE`: every row changes. |
| L002 | error | `NULL` | `= NULL`, `<> NULL`, `!= NULL`: never true; use `IS [NOT] NULL`. |
| L003 | warning | `NOT IN` | `NOT IN (subquery)`: no rows at all if the subquery returns a NULL; use `NOT EXISTS`. A warning, not an error, because whether the subquery can return NULL depends on the schema. |
| L004 | error | `FROM` | A comma join with no `WHERE`: a cross join. Explicit `JOIN ... ON` and `CROSS JOIN` are not flagged on PostgreSQL; on MySQL a `JOIN` without `ON` reads like a comma join to the parser and is not flagged either. |
| L101 | warning | `*` | `SELECT *` and `t.*`, except inside `EXISTS (...)` and in `count(*)`. |
| L102 | warning | the pattern | `LIKE` / `ILIKE` with a pattern starting with `%`: no index use. |
| L103 | warning | the function | A function around a column in a `WHERE` comparison, `LIKE`, or `IN`: no index use on that column. |
| L104 | warning | `RAND` / `RANDOM` | `ORDER BY RAND()` / `RANDOM()`: sorts the whole table. |
| L105 | warning | the offset | `OFFSET` above 10,000: rows read and thrown away; use keyset paging. |
| L106 | warning | `INSERT` | `INSERT` with `VALUES` or `SELECT` but no column list. |
| L201 | info | the verb | `DROP`, `ALTER`, or `TRUNCATE` on a production connection: it will ask for confirmation and be audited. |

Each rule has positive and negative cases in `internal/lint/lint_test.go`,
run on PostgreSQL and MySQL.
