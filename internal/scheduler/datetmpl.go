package scheduler

import (
	"time"

	"github.com/zoyluo/cronova/internal/datetmpl"
	"github.com/zoyluo/cronova/internal/model"
)

// Date expression templates ({{ logical_date - 7d | %Y%m%d }} and friends)
// are evaluated by the shared internal/datetmpl grammar — shared because the
// parser also validates depends_on_dag offsets against it.

// evalDateExpr evaluates expr ("logical_date..." / "logical_datetime...")
// against t, the run's logical date already shifted into the DAG's timezone.
func evalDateExpr(t time.Time, expr string) (string, bool) {
	return datetmpl.Eval(t, expr)
}

// dagLocation resolves a DAG's IANA timezone for template rendering. The
// parser validated the name at save time; if the zone database still cannot
// load it here, rendering falls back to UTC rather than failing dispatch.
func dagLocation(d *model.DAG) *time.Location {
	if d == nil || d.Timezone == "" {
		return time.UTC
	}
	if loc, err := time.LoadLocation(d.Timezone); err == nil {
		return loc
	}
	return time.UTC
}
