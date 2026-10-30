package sqlscan

import "testing"

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		in      string
		want    string
	}{
		{"pg_alter_user", Postgres, "ALTER USER app PASSWORD 's3cr''et'", "ALTER USER app PASSWORD '***'"},
		{"pg_create_role_encrypted", Postgres, "CREATE ROLE a LOGIN ENCRYPTED PASSWORD 'x' VALID UNTIL '2027-01-01'",
			"CREATE ROLE a LOGIN ENCRYPTED PASSWORD '***' VALID UNTIL '2027-01-01'"},
		{"mysql_identified_by", MySQL, "CREATE USER 'u'@'%' IDENTIFIED BY 'pw'", "CREATE USER 'u'@'%' IDENTIFIED BY '***'"},
		{"mysql_identified_with_by", MySQL, "ALTER USER u IDENTIFIED WITH caching_sha2_password BY 'pw'",
			"ALTER USER u IDENTIFIED WITH caching_sha2_password BY '***'"},
		{"mysql_set_password", MySQL, "SET PASSWORD FOR u = 'pw'", "SET PASSWORD FOR u = '***'"},
		{"mysql_set_password_eq", MySQL, "SET PASSWORD = 'pw'", "SET PASSWORD = '***'"},
		{"unrelated_strings_untouched", Postgres, "SELECT 'password', name FROM t WHERE x = 'y'",
			"SELECT 'password', name FROM t WHERE x = 'y'"},
		{"password_then_other_statement", Postgres, "ALTER USER a PASSWORD 'x'; SELECT * FROM t WHERE c = 'keep'",
			"ALTER USER a PASSWORD '***'; SELECT * FROM t WHERE c = 'keep'"},
		{"column_named_password", Postgres, "SELECT password FROM users WHERE name = 'bob'",
			"SELECT password FROM users WHERE name = 'bob'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactSecrets(tt.in, tt.dialect); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}
