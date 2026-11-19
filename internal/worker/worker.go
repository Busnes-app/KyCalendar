// Package worker runs the scheduled background jobs on one server at a time.
package worker

import (
	"context"
	"log"
	"sync"
	"time"
)

// Job is one periodic task. Every job runs on its own goroutine, so a slow one never
// delays another; AtStart runs it once as soon as ownership begins.
type Job struct {
	Name    string
	Every   time.Duration
	AtStart bool
	Run     func(context.Context)
}

// Locker elects the owner: store.CoordinationStore's WithLock.
type Locker interface {
	WithLock(ctx context.Context, name string, work func(context.Context) error) (bool, error)
}

// Options are the election's timings.
type Options struct {
	// Retry is how often a server that does not own the lock tries again.
	Retry time.Duration
	// Grace is how long a new owner waits before its first job: longer than the lock's
	// probe bound, so a predecessor whose session the database dropped has already seen its
	// lock lost and cancelled. Zero on SQLite, whose flock cannot be lost.
	Grace time.Duration
}

// Lock is the election's lock name.
const Lock = "scheduled-worker"

// Run keeps electing an owner of Lock until ctx ends; while this server owns it, it runs
// jobs. Losing the lock cancels every job's context, and Run waits for each to return before
// it tries again, so this server never runs a job beside a later owner's. That is single
// ownership, not exactly-once: a job cancelled mid-call may have finished its remote half,
// so jobs must tolerate a retry (ETag-guarded copies, dedupe keys, reconciling PUTs).
func Run(ctx context.Context, locks Locker, o Options, jobs []Job) {
	for ctx.Err() == nil {
		won, err := locks.WithLock(ctx, Lock, func(owned context.Context) error {
			log.Print("[WORKER] elected to run scheduled jobs")
			if o.Grace > 0 {
				select {
				case <-owned.Done():
					return owned.Err()
				case <-time.After(o.Grace):
				}
			}
			var wg sync.WaitGroup
			for _, j := range jobs {
				wg.Add(1)
				go func() {
					defer wg.Done()
					loop(owned, j)
				}()
			}
			wg.Wait()
			return context.Cause(owned)
		})
		if ctx.Err() != nil {
			return
		}
		if won {
			log.Printf("[WORKER] stopped running scheduled jobs: %v", err)
		} else if err != nil {
			log.Printf("[WORKER] election unavailable: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(o.Retry):
		}
	}
}

func loop(ctx context.Context, j Job) {
	ticker := time.NewTicker(j.Every)
	defer ticker.Stop()
	if j.AtStart {
		j.Run(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		j.Run(ctx)
	}
}
