package marketdata

import (
	"context"
	"time"
)

const defaultMaximumQueuedRequests = 8

type limiterRequest struct {
	ctx     context.Context
	granted chan error
}

// requestLimiter admits one request at a time without reserving future slots.
// Its worker observes cancellation while a request is queued or rate-limited,
// and the bounded channel prevents an overloaded caller from growing memory
// without limit.
type requestLimiter struct {
	interval time.Duration
	now      func() time.Time
	queue    chan limiterRequest
}

func newRequestLimiter(interval time.Duration, maximumQueuedRequests int, now func() time.Time) *requestLimiter {
	if interval <= 0 {
		return nil
	}
	limiter := &requestLimiter{
		interval: interval,
		now:      now,
		queue:    make(chan limiterRequest, maximumQueuedRequests),
	}
	go limiter.run()
	return limiter
}

func (l *requestLimiter) wait(ctx context.Context) error {
	request := limiterRequest{ctx: ctx, granted: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case l.queue <- request:
	default:
		return ErrRequestQueueFull
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-request.granted:
		return err
	}
}

func (l *requestLimiter) run() {
	var lastAdmission time.Time
	for request := range l.queue {
		if err := request.ctx.Err(); err != nil {
			request.grant(err)
			continue
		}

		now := l.now()
		wait := time.Duration(0)
		if !lastAdmission.IsZero() {
			wait = lastAdmission.Add(l.interval).Sub(now)
		}
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-request.ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				request.grant(request.ctx.Err())
				continue
			case <-timer.C:
			}
		}

		if err := request.ctx.Err(); err != nil {
			request.grant(err)
			continue
		}
		lastAdmission = l.now()
		request.grant(nil)
	}
}

func (r limiterRequest) grant(err error) {
	select {
	case r.granted <- err:
	default:
	}
}
