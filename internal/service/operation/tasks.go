package operation

import (
	"context"
	"time"
)

// TaskExecutor schedules work independently of request cancellation.
// A false result means shutdown has started and no task was launched.
type TaskExecutor interface {
	Submit(time.Duration, func(context.Context)) bool
}
