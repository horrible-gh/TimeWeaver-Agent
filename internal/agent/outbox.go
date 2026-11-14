package agent

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/api"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/config"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
)

type Delivery interface {
	Report(context.Context, string, protocol.Result) (protocol.Ack, error)
}
type TokenProvider interface {
	Token(context.Context) (string, error)
}
type Outbox struct {
	mu       sync.Mutex
	ctx      context.Context
	cfg      config.API
	state    *State
	sender   Delivery
	tokens   TokenProvider
	reserved int
	count    int
	queues   map[string][]protocol.Result
	active   map[string]bool
	workers  chan struct{}
	latched  bool
	wg       sync.WaitGroup
	Ack      func(protocol.Result, protocol.Ack)
	Reject   func(protocol.Result, string)
	Event    func(string, string, string)
}

func NewOutbox(ctx context.Context, c config.API, state *State, sender Delivery, tokens TokenProvider) *Outbox {
	return &Outbox{ctx: ctx, cfg: c, state: state, sender: sender, tokens: tokens, queues: map[string][]protocol.Result{}, active: map[string]bool{}, workers: make(chan struct{}, c.OutboxSenderWorkers)}
}
func (o *Outbox) Size() int { o.mu.Lock(); defer o.mu.Unlock(); return o.reserved + o.count }
func (o *Outbox) Reserve(n int) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if n < 1 || o.ctx.Err() != nil || n > o.cfg.OutboxCapacity-o.reserved-o.count {
		return false
	}
	o.reserved += n
	o.watermark()
	return true
}
func (o *Outbox) Release(n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if n < 0 || n > o.reserved {
		panic("outbox reservation underflow")
	}
	o.reserved -= n
	o.watermark()
}
func (o *Outbox) watermark() {
	size := o.reserved + o.count
	if !o.latched && size >= o.cfg.OutboxHighWatermark {
		o.latched = true
		o.state.Backlog(true)
		if o.Event != nil {
			go o.Event("outbox_backlog", "warning", "Result outbox entered backlog mode.")
		}
	} else if o.latched && size <= o.cfg.OutboxLowWatermark {
		o.latched = false
		o.state.Backlog(false)
	}
}
func (o *Outbox) Enqueue(r protocol.Result) {
	o.mu.Lock()
	if o.reserved < 1 {
		o.mu.Unlock()
		panic("result without reservation")
	}
	o.reserved--
	o.count++
	o.queues[r.ExecutionID] = append(o.queues[r.ExecutionID], r)
	start := !o.active[r.ExecutionID]
	if start {
		o.active[r.ExecutionID] = true
		o.wg.Add(1)
	}
	o.watermark()
	o.mu.Unlock()
	if start {
		go o.drain(r.ExecutionID)
	}
}
func (o *Outbox) head(group string) (protocol.Result, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	q := o.queues[group]
	if len(q) == 0 {
		delete(o.active, group)
		delete(o.queues, group)
		return protocol.Result{}, false
	}
	return q[0], true
}
func (o *Outbox) remove(group string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.queues[group] = o.queues[group][1:]
	o.count--
	o.watermark()
}
func (o *Outbox) drain(group string) {
	defer o.wg.Done()
	attempt := 0
	for {
		r, ok := o.head(group)
		if !ok {
			return
		}
		if o.ctx.Err() != nil {
			return
		}
		if o.state.View().State == "HALTED" {
			if !sleep(o.ctx, time.Second) {
				return
			}
			continue
		}
		select {
		case o.workers <- struct{}{}:
		case <-o.ctx.Done():
			return
		}
		token, err := o.tokens.Token(o.ctx)
		var ack protocol.Ack
		if err == nil {
			o.state.Success("credential")
			ack, err = o.sender.Report(o.ctx, token, r)
		}
		<-o.workers
		if err == nil {
			o.remove(group)
			attempt = 0
			if o.Ack != nil {
				o.Ack(r, ack)
			}
			continue
		}
		if o.ctx.Err() != nil {
			return
		}
		if api.AuthFailure(err) || stringsCredential(err) {
			if api.Code(err) == "invalid_token" || api.Code(err) == "token_expired" {
				if invalidator, ok := o.tokens.(interface{ Reject(string) }); ok {
					invalidator.Reject(token)
				}
			}
			o.state.Failure("result", err)
			if !sleep(o.ctx, time.Second) {
				return
			}
			continue
		}
		if api.Retryable(err) {
			delay := retryDelay(o.cfg, attempt, err)
			attempt++
			if !sleep(o.ctx, delay) {
				return
			}
			continue
		}
		o.remove(group)
		if o.Reject != nil {
			o.Reject(r, api.Code(err))
		}
		if o.Event != nil {
			o.Event("sync_error", "error", "Execution result was permanently rejected.")
		}
	}
}
func stringsCredential(err error) bool {
	code := api.Code(err)
	return len(code) >= 11 && code[:11] == "credential_"
}
func retryDelay(c config.API, attempt int, err error) time.Duration {
	seconds := math.Min(c.RetryMaxDelay, c.RetryInitialDelay*math.Pow(c.RetryMultiplier, float64(min(attempt, 32))))
	seconds *= 1 + c.RetryJitterRatio*(rand.Float64()*2-1)
	d := config.Seconds(seconds)
	var ae *api.Error
	if errors.As(err, &ae) && ae.RetryAfter > d {
		d = ae.RetryAfter
	}
	return d
}
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func (o *Outbox) WaitEmpty(ctx context.Context) bool {
	for o.Size() != 0 {
		if !sleep(ctx, 20*time.Millisecond) {
			return false
		}
	}
	return true
}
