package operator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	neturl "net/url"
	"strconv"
	"strings"

	gomysql "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib" // "pgx" driver (pure Go)
	_ "modernc.org/sqlite"             // "sqlite" driver (pure Go, no CGO)

	"github.com/zoyluo/cronova/internal/model"
)

// SQLSpec is the resolved spec for a sql-type task: a driver name + DSN (built
// from the task's connection by BuildDSN) and the query (templated). Err carries
// a dispatch-time failure (e.g. connection not found) so run-op reports it and
// fails the task cleanly.
type SQLSpec struct {
	Driver string `json:"driver"`
	DSN    string `json:"dsn"`
	Query  string `json:"query"`
	Err    string `json:"err,omitempty"`
}

const maxRowsLog = 100

// RunSQL opens the database, runs the query, and logs the result. Exit 0 on
// success, 1 on any error. A row-returning statement (SELECT/WITH/SHOW/…) logs
// tab-separated columns then rows (capped at maxRowsLog) and the total count; a
// non-row statement logs rows-affected.
func RunSQL(ctx context.Context, spec SQLSpec, out io.Writer) int {
	if spec.Err != "" {
		fmt.Fprintf(out, "sql: %s\n", spec.Err)
		return 1
	}
	db, err := sql.Open(spec.Driver, spec.DSN)
	if err != nil {
		fmt.Fprintf(out, "sql: open: %v\n", err)
		return 1
	}
	defer db.Close()

	switch leadingVerb(spec.Query) {
	case "SELECT", "WITH", "SHOW", "EXPLAIN", "VALUES", "TABLE", "PRAGMA":
		return runQuery(ctx, db, spec.Query, out)
	default:
		res, err := db.ExecContext(ctx, spec.Query)
		if err != nil {
			fmt.Fprintf(out, "sql: exec: %v\n", err)
			return 1
		}
		aff, _ := res.RowsAffected()
		fmt.Fprintf(out, "(%d rows affected)\n", aff)
		return 0
	}
}

func runQuery(ctx context.Context, db *sql.DB, query string, out io.Writer) int {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		fmt.Fprintf(out, "sql: query: %v\n", err)
		return 1
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	fmt.Fprintln(out, strings.Join(cols, "\t"))
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	n := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			fmt.Fprintf(out, "sql: scan: %v\n", err)
			return 1
		}
		if n < maxRowsLog {
			cells := make([]string, len(vals))
			for i, v := range vals {
				cells[i] = cellStr(v)
			}
			fmt.Fprintln(out, strings.Join(cells, "\t"))
		}
		n++
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintf(out, "sql: rows: %v\n", err)
		return 1
	}
	if n > maxRowsLog {
		fmt.Fprintf(out, "… (%d rows total, first %d shown)\n", n, maxRowsLog)
	}
	fmt.Fprintf(out, "(%d rows)\n", n)
	return 0
}

// leadingVerb returns the uppercased leading SQL keyword, skipping leading
// whitespace, -- line comments, /* */ block comments, and ( — so a commented or
// parenthesized SELECT is still recognised as row-returning and not misrouted to
// Exec (which would run it and silently discard its result set).
func leadingVerb(q string) string {
	i := 0
	for i < len(q) {
		c := q[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '(':
			i++
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			nl := strings.IndexByte(q[i:], '\n')
			if nl < 0 {
				return ""
			}
			i += nl + 1
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			end := strings.Index(q[i+2:], "*/")
			if end < 0 {
				return ""
			}
			i += 2 + end + 2
		default:
			start := i
			for i < len(q) && isSQLWordByte(q[i]) {
				i++
			}
			return strings.ToUpper(q[start:i])
		}
	}
	return ""
}

func isSQLWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func cellStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}

// BuildDSN derives a database/sql driver name and DSN from a connection. The
// connection Type selects the driver; host/port/login/password plus extra.database
// (and driver-specific extras like sslmode) build the DSN. The password is only in
// the returned DSN, never logged by callers.
func BuildDSN(c *model.Connection) (driver, dsn string, err error) {
	extra := parseExtra(c.Extra)
	db := extra["database"]
	switch strings.ToLower(strings.TrimSpace(c.Type)) {
	case "postgres", "postgresql", "pg":
		u := &neturl.URL{
			Scheme: "postgres",
			User:   neturl.UserPassword(c.Login, c.Password),
			Host:   hostPort(c.Host, c.Port, 5432),
			Path:   "/" + db,
		}
		if ssl := extra["sslmode"]; ssl != "" {
			u.RawQuery = neturl.Values{"sslmode": {ssl}}.Encode()
		}
		return "pgx", u.String(), nil
	case "mysql", "mariadb":
		cfg := gomysql.NewConfig()
		cfg.User, cfg.Passwd = c.Login, c.Password
		cfg.Net, cfg.Addr = "tcp", hostPort(c.Host, c.Port, 3306)
		cfg.DBName = db
		return "mysql", cfg.FormatDSN(), nil
	case "sqlite", "sqlite3":
		return "sqlite", c.Host, nil // Host holds the db file path
	default:
		return "", "", fmt.Errorf("unsupported sql connection type %q (want postgres/mysql/sqlite)", c.Type)
	}
}

func hostPort(host string, port, def int) string {
	if port <= 0 {
		port = def
	}
	return host + ":" + strconv.Itoa(port)
}

func parseExtra(raw string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return out
	}
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		} else if v != nil {
			out[k] = fmt.Sprintf("%v", v)
		}
	}
	return out
}
