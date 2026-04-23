// internal/util/sql_validator_test.go
package util

import (
	"testing"
)

func TestValidateSQL(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		wantError bool
	}{
		// Valid queries
		{"simple select", "SELECT * FROM users", false},
		{"select with where", "SELECT id, name FROM users WHERE id = 1", false},
		{"show databases", "SHOW DATABASES", false},
		{"show tables", "SHOW TABLES", false},
		{"describe table", "DESCRIBE users", false},
		{"desc table", "DESC users", false},
		{"explain query", "EXPLAIN SELECT * FROM users", false},
		{"select lowercase", "select * from users", false},
		{"trailing semicolon", "SELECT * FROM users;", false},
		{"semicolon inside string literal", "SELECT ';' AS semi", false},
		{"comment marker inside string literal", "SELECT '/* not a comment */' AS txt", false},
		{"double hyphen inside string literal", "SELECT '-- not a comment' AS txt", false},

		// Invalid queries - DDL
		{"create table", "CREATE TABLE users (id INT)", true},
		{"alter table", "ALTER TABLE users ADD column email VARCHAR(255)", true},
		{"drop table", "DROP TABLE users", true},
		{"truncate table", "TRUNCATE TABLE users", true},
		{"rename table", "RENAME TABLE users TO old_users", true},

		// Invalid queries - DML
		{"insert", "INSERT INTO users (name) VALUES ('test')", true},
		{"update", "UPDATE users SET name = 'test'", true},
		{"delete", "DELETE FROM users WHERE id = 1", true},
		{"replace", "REPLACE INTO users (id, name) VALUES (1, 'test')", true},

		// Invalid queries - Administrative
		{"grant", "GRANT SELECT ON *.* TO 'user'@'localhost'", true},
		{"revoke", "REVOKE SELECT ON *.* FROM 'user'@'localhost'", true},
		{"flush", "FLUSH PRIVILEGES", true},
		{"kill", "KILL 1234", true},
		{"shutdown", "SHUTDOWN", true},
		{"set global", "SET GLOBAL max_connections = 100", true},
		{"set session", "SET SESSION wait_timeout = 300", true},

		// Invalid queries - Transactions
		{"begin", "BEGIN", true},
		{"commit", "COMMIT", true},
		{"rollback", "ROLLBACK", true},
		{"start transaction", "START TRANSACTION", true},

		// Invalid queries - Dangerous functions
		{"sleep function", "SELECT SLEEP(10)", true},
		{"benchmark function", "SELECT BENCHMARK(1000000, SHA1('test'))", true},
		{"get_lock function", "SELECT GET_LOCK('test', 10)", true},
		{"load_file function", "SELECT LOAD_FILE('/etc/passwd')", true},

		// Invalid queries - File operations
		{"into outfile", "SELECT * FROM users INTO OUTFILE '/tmp/test.csv'", true},
		{"into dumpfile", "SELECT * FROM users INTO DUMPFILE '/tmp/test.bin'", true},
		{"load data", "LOAD DATA INFILE '/tmp/test.csv' INTO TABLE users", true},

		// Invalid queries - System schema access
		{"mysql.user access", "SELECT * FROM mysql.user", true},
		{"information_schema access", "SELECT * FROM information_schema.tables", true},
		{"performance_schema access", "SELECT * FROM performance_schema.events_statements_summary_by_digest", true},
		{"sys schema access", "SELECT * FROM sys.session", true},

		// Invalid queries - Multi-statement
		{"multi-statement", "SELECT 1; DROP TABLE users", true},
		{"multi with comment", "SELECT 1; -- DROP TABLE users", true},

		// Invalid queries - Stored procedures
		{"call procedure", "CALL my_procedure()", true},
		{"prepare statement", "PREPARE stmt FROM 'SELECT ?'", true},
		{"execute statement", "EXECUTE stmt USING @var", true},

		// Edge cases
		{"empty query", "", true},
		{"whitespace only", "   ", true},
		{"random text", "hello world", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSQL(tt.sql)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateSQL(%q) error = %v, wantError %v", tt.sql, err, tt.wantError)
			}
		})
	}
}

func TestIsReadOnlySQL(t *testing.T) {
	if !IsReadOnlySQL("SELECT * FROM users") {
		t.Error("IsReadOnlySQL should return true for SELECT")
	}
	if IsReadOnlySQL("DROP TABLE users") {
		t.Error("IsReadOnlySQL should return false for DROP")
	}
}

func TestValidateSelectColumns(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      string
		wantError bool
	}{
		{"empty string", "", "*", false},
		{"single column", "id", "`id`", false},
		{"multiple columns", "id, name, email", "`id`, `name`, `email`", false},
		{"with spaces", "  id  ,  name  ", "`id`, `name`", false},
		{"star", "*", "*", false},
		{"column with alias", "id AS user_id", "`id` AS `user_id`", false},
		{"table.column", "users.id", "`users`.`id`", false},
		{"table.column with alias", "users.id AS uid", "`users`.`id` AS `uid`", false},

		// Invalid patterns
		{"with parentheses", "COUNT(id)", "", true},
		{"with semicolon", "id; DROP TABLE", "", true},
		{"with comment", "id -- comment", "", true},
		{"with union", "id UNION SELECT", "", true},
		{"with sleep", "SLEEP(10)", "", true},
		{"with benchmark", "BENCHMARK(1000, 1)", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateSelectColumns(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateSelectColumns(%q) error = %v, wantError %v", tt.input, err, tt.wantError)
				return
			}
			if !tt.wantError && got != tt.want {
				t.Errorf("ValidateSelectColumns(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateWhereClause(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{"empty string", "", false},
		{"simple condition", "id = 1", false},
		{"multiple conditions", "id = 1 AND status = 'active'", false},
		{"with parentheses", "(id = 1 OR id = 2) AND status = 'active'", false},
		{"with IN clause", "id IN (1, 2, 3)", false},
		{"semicolon in string literal", "note = ';' AND id = 1", false},
		{"comment marker in string literal", "note = '/* ok */' AND id = 1", false},
		{"double hyphen in string literal", "note = '-- ok' AND id = 1", false},

		// Invalid patterns
		{"with semicolon", "id = 1; DROP TABLE users", true},
		{"with comment --", "id = 1 -- DROP TABLE", true},
		{"with comment /*", "id = 1 /* comment */", true},
		{"with union", "id = 1 UNION SELECT", true},
		{"with sleep", "id = SLEEP(10)", true},
		{"with benchmark", "id = BENCHMARK(1000, 1)", true},
		{"with load_file", "name = LOAD_FILE('/etc/passwd')", true},
		{"with system variable", "id = @@version", true},
		{"unbalanced parens", "(id = 1", true},
		{"too long", string(make([]byte, 1001)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWhereClause(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateWhereClause(%q) error = %v, wantError %v", tt.input, err, tt.wantError)
			}
		})
	}
}

func TestSQLValidationError(t *testing.T) {
	// Test with pattern
	err1 := &SQLValidationError{Reason: "blocked", Pattern: "DROP"}
	if err1.Error() != "blocked: DROP" {
		t.Errorf("unexpected error message: %s", err1.Error())
	}

	// Test without pattern
	err2 := &SQLValidationError{Reason: "empty query"}
	if err2.Error() != "empty query" {
		t.Errorf("unexpected error message: %s", err2.Error())
	}
}

func TestValidateWriteSQL(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		wantError bool
	}{
		// Allowed DML
		{"insert", "INSERT INTO users (name) VALUES ('test')", false},
		{"insert multiple rows", "INSERT INTO users (name) VALUES ('a'), ('b')", false},
		{"update with where", "UPDATE users SET name = 'test' WHERE id = 1", false},
		{"delete with where", "DELETE FROM users WHERE id = 1", false},
		{"replace", "REPLACE INTO users (id, name) VALUES (1, 'test')", false},
		{"insert lowercase", "insert into users (name) values ('test')", false},
		{"trailing semicolon", "INSERT INTO users (name) VALUES ('test');", false},

		// Rejected read-only statements (belong in run_query)
		{"select", "SELECT * FROM users", true},
		{"show databases", "SHOW DATABASES", true},
		{"describe table", "DESCRIBE users", true},

		// Rejected DDL
		{"create table", "CREATE TABLE users (id INT)", true},
		{"alter table", "ALTER TABLE users ADD column email VARCHAR(255)", true},
		{"drop table", "DROP TABLE users", true},
		{"truncate table", "TRUNCATE TABLE users", true},

		// Rejected admin
		{"grant", "GRANT SELECT ON *.* TO 'user'@'localhost'", true},
		{"flush", "FLUSH PRIVILEGES", true},
		{"kill", "KILL 1234", true},
		{"set global", "SET GLOBAL max_connections = 100", true},

		// Rejected transactions
		{"begin", "BEGIN", true},
		{"commit", "COMMIT", true},
		{"rollback", "ROLLBACK", true},
		{"start transaction", "START TRANSACTION", true},

		// Rejected multi-statement
		{"multi insert drop", "INSERT INTO t (a) VALUES (1); DROP TABLE t", true},
		{"multi update select", "UPDATE t SET a=1; SELECT * FROM t", true},

		// Rejected dangerous functions
		{"update with sleep", "UPDATE users SET name = 'x' WHERE SLEEP(1)", true},
		{"insert with load_file", "INSERT INTO t (data) VALUES (LOAD_FILE('/etc/passwd'))", true},
		{"update with benchmark", "UPDATE t SET a = BENCHMARK(1000, MD5('x'))", true},

		// Rejected system schema access
		{"insert into mysql.user", "INSERT INTO mysql.user (User) VALUES ('x')", true},
		{"update information_schema", "UPDATE INFORMATION_SCHEMA.tables SET t = 1", true},
		{"delete from mysql.user", "DELETE FROM mysql.user WHERE user = 'x'", true},

		// Rejected INTO OUTFILE/DUMPFILE
		{"select into outfile", "SELECT * FROM users INTO OUTFILE '/tmp/x'", true},

		// Rejected comments
		{"insert with line comment", "INSERT INTO users (name) VALUES ('x') -- comment", true},
		{"insert with block comment", "INSERT INTO users /* comment */ (name) VALUES ('x')", true},

		// Empty / invalid
		{"empty", "", true},
		{"whitespace only", "   ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWriteSQL(tt.sql)
			if tt.wantError && err == nil {
				t.Errorf("expected error for %q but got none", tt.sql)
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected error for %q: %v", tt.sql, err)
			}
		})
	}
}

func TestValidateWriteSQLCombined(t *testing.T) {
	// Focused integration test: ensure parser + regex validators agree.
	tests := []struct {
		name      string
		sql       string
		wantError bool
	}{
		{"insert values", "INSERT INTO users (id, name) VALUES (1, 'a')", false},
		{"insert select", "INSERT INTO archive SELECT * FROM users WHERE active = 0", false},
		{"update with subquery", "UPDATE users SET name = (SELECT name FROM source WHERE id = 1) WHERE id = 1", false},
		{"delete with join", "DELETE u FROM users u JOIN old o ON u.id = o.id", false},
		{"insert on duplicate", "INSERT INTO users (id, name) VALUES (1, 'a') ON DUPLICATE KEY UPDATE name = 'a'", false},

		// Rejected
		{"ddl create", "CREATE TABLE x (id INT)", true},
		{"select", "SELECT 1", true},
		{"insert subquery to mysql schema", "INSERT INTO archive SELECT * FROM mysql.user", true},
		{"insert with nested subquery to mysql schema", "INSERT INTO archive SELECT id FROM users WHERE id IN (SELECT User FROM mysql.user)", true},
		{"update with sleep in set", "UPDATE t SET a = SLEEP(1)", true},
		{"insert on duplicate with sleep", "INSERT INTO t (a) VALUES (1) ON DUPLICATE KEY UPDATE a = SLEEP(1)", true},
		{"insert values with sleep", "INSERT INTO t (a) VALUES (SLEEP(1))", true},
		{"replace into mysql schema", "REPLACE INTO mysql.user (User) VALUES ('x')", true},
		{"multi-table delete from mysql", "DELETE t1 FROM users t1 JOIN mysql.user mu ON t1.name = mu.User", true},
		// Prefix boundary: INSERTX should not be accepted as INSERT.
		{"prefix boundary insertx", "INSERTX INTO t (a) VALUES (1)", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWriteSQLCombined(tt.sql)
			if tt.wantError && err == nil {
				t.Errorf("expected error for %q but got none", tt.sql)
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected error for %q: %v", tt.sql, err)
			}
		})
	}
}
