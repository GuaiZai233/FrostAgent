package storage

import "testing"

func TestSQLiteCheckConstraintTokens(t *testing.T) {
	for _, test := range []struct {
		name, statement string
		want            bool
	}{
		{"table-check", `CREATE TABLE t (enabled INTEGER, CHECK (enabled = 1))`, true},
		{"column-check", `CREATE TABLE t (enabled INTEGER ChEcK(enabled = 1))`, true},
		{"named-check", `CREATE TABLE t (enabled INTEGER, CONSTRAINT "check-name" CHECK(enabled = 1))`, true},
		{"comment-gap", "CREATE TABLE t (enabled INTEGER, CHECK/* ignored */\n(enabled = 1))", true},
		{"after-comments", "CREATE TABLE t (enabled INTEGER /* check ignored */ -- check ignored\n CHECK(enabled=1))", true},
		{"line-comment", "CREATE TABLE t (enabled INTEGER -- CHECK(enabled=1)\n)", false},
		{"block-comment", `CREATE TABLE t (enabled INTEGER /* CHECK(enabled=1) */)`, false},
		{"string", `CREATE TABLE t (value TEXT DEFAULT 'CHECK(enabled=1)')`, false},
		{"escaped-string", `CREATE TABLE t (value TEXT DEFAULT 'it''s CHECK(enabled=1)')`, false},
		{"quoted-identifier", `CREATE TABLE "CHECK" ("CHECK" TEXT)`, false},
		{"escaped-identifier", `CREATE TABLE t ("with""CHECK" TEXT)`, false},
		{"bracket-identifier", `CREATE TABLE [CHECK] ([CHECK] TEXT)`, false},
		{"backtick-identifier", "CREATE TABLE `CHECK` (`CHECK` TEXT)", false},
		{"identifier-substring", `CREATE TABLE check_table (check_value TEXT, value_CHECK TEXT, has$CHECK TEXT, 标识CHECK TEXT)`, false},
		{"trailing-line-comment", "CREATE TABLE t (value TEXT) -- CHECK(value)", false},
		{"trailing-block-comment", "CREATE TABLE t (value TEXT) /* CHECK(value)", false},
		{"string-then-check", `CREATE TABLE t (value TEXT DEFAULT 'CHECK', CHECK(length(value)>0))`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := containsSQLiteCheckConstraint(test.statement); got != test.want {
				t.Fatalf("CHECK detection = %v, want %v for %q", got, test.want, test.statement)
			}
		})
	}
}
