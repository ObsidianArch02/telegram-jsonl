package main

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type rpcGate struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
	cooldown time.Time
	path     string
	failure  *failure
}

func openGate(path string, interval time.Duration, fail *failure) (*rpcGate, error) {
	g := &rpcGate{path: path, interval: interval, failure: fail}
	if err := readJSON(path, &g.cooldown); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if time.Until(g.cooldown) > 0 {
		log.Printf("Resuming saved FLOOD_WAIT; RPC calls paused until %s", g.cooldown.In(time.Local).Format(time.RFC3339))
	}
	return g, nil
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (g *rpcGate) Handle(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		g.mu.Lock()
		defer g.mu.Unlock()
		for {
			if err := g.failure.check(); err != nil {
				return err
			}
			until := g.next
			if g.cooldown.After(until) {
				until = g.cooldown
			}
			if err := waitContext(ctx, time.Until(until)); err != nil {
				return err
			}
			g.next = time.Now().Add(g.interval)
			err := next.Invoke(ctx, in, out)
			delay, flood := tgerr.AsFloodWait(err)
			if !flood {
				return err
			}
			g.cooldown = time.Now().Add(delay + time.Second)
			if err := writeJSON(g.path, g.cooldown); err != nil {
				return g.failure.report(err)
			}
			log.Printf("Telegram requested FLOOD_WAIT; pausing RPC calls for %s until %s", delay+time.Second, g.cooldown.In(time.Local).Format(time.RFC3339))
		}
	}
}
