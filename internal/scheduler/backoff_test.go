package scheduler

import (
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

func TestRetryDelay(t *testing.T) {
	sec := func(n int) time.Duration { return time.Duration(n) * time.Second }
	cases := []struct {
		name  string
		task  model.Task
		tries int
		want  time.Duration
	}{
		{"fixed default", model.Task{RetryDelay: 30}, 1, sec(30)},
		{"fixed later try unchanged", model.Task{RetryDelay: 30, RetryBackoff: model.BackoffFixed}, 5, sec(30)},
		{"zero delay stays zero even exponential", model.Task{RetryDelay: 0, RetryBackoff: model.BackoffExponential}, 3, 0},
		{"exp first retry = base", model.Task{RetryDelay: 30, RetryBackoff: model.BackoffExponential}, 1, sec(30)},
		{"exp second retry = 2x", model.Task{RetryDelay: 30, RetryBackoff: model.BackoffExponential}, 2, sec(60)},
		{"exp fourth retry = 8x", model.Task{RetryDelay: 30, RetryBackoff: model.BackoffExponential}, 4, sec(240)},
		{"exp capped by retry_delay_max", model.Task{RetryDelay: 30, RetryBackoff: model.BackoffExponential, RetryDelayMax: 100}, 4, sec(100)},
		{"cap above growth is inert", model.Task{RetryDelay: 30, RetryBackoff: model.BackoffExponential, RetryDelayMax: 1000}, 2, sec(60)},
		{"tries 0 clamps to base", model.Task{RetryDelay: 30, RetryBackoff: model.BackoffExponential}, 0, sec(30)},
		{"huge tries does not overflow", model.Task{RetryDelay: 30, RetryBackoff: model.BackoffExponential, RetryDelayMax: 3600}, 63, time.Hour},
	}
	for _, c := range cases {
		if got := retryDelay(c.task, c.tries); got != c.want {
			t.Errorf("%s: retryDelay = %v, want %v", c.name, got, c.want)
		}
	}
}
