package gollector

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Server struct {
	ctx           context.Context
	cancelContext func()
	checkQueue    CheckQueue

	// Should server re-enqueue checks back to the check queue after they finish running
	AutoReEnqueue bool

	// The maximum number of concurrently executing checks
	MaxRunningChecks uint64

	// Callback triggerred just prior to check execution (useful for logging)
	OnCheckExecuting func(check Check)

	// Callback triggerred if check command errors (useful for logging)
	OnCheckErrored func(check Check, err error)

	// Callback triggered just after a check finishes execution (useful for logging)
	OnCheckFinished func(check Check, runDuration time.Duration)
}

func NewServer(ctx context.Context, checkQueue CheckQueue) *Server {
	ctx, cancel := context.WithCancel(ctx)

	return &Server{
		ctx:              ctx,
		cancelContext:    cancel,
		checkQueue:       checkQueue,
		MaxRunningChecks: 100,
		AutoReEnqueue:    true,
	}
}

func (s *Server) Run() {
	runningLimiter := make(chan struct{}, s.MaxRunningChecks)
	defer close(runningLimiter)

	pendingChecks := make(chan *Check, s.MaxRunningChecks)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // populate pendingCheck channel from queue indefinitely
		defer wg.Done()
		defer close(pendingChecks)

		// we have this in its own goroutine in case checkQueue.Dequeue() takes some time to return and the main
		// check loop (below) is blocking by the runningLimiter.  this allows us to continue to fill the pending
		// checks queue and make the system more responsive

		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}

			var check *Check
			if len(pendingChecks) < cap(pendingChecks) {
				check = s.checkQueue.Dequeue()
				if check != nil {
					pendingChecks <- check
				}
			}

			if check == nil {
				time.Sleep(250 * time.Millisecond)
			}
		}
	}()

loop:
	for check := range pendingChecks {
		select {
		case <-s.ctx.Done():
			s.checkQueue.Enqueue(*check) // put the check back, we're shutting down
			break loop
		case runningLimiter <- struct{}{}:
			wg.Add(1)
			go func(check *Check) {
				defer wg.Done()
				defer func() {
					if s.AutoReEnqueue {
						s.checkQueue.Enqueue(*check)
					}
					<-runningLimiter
				}()

				onCheckExecuting := s.OnCheckExecuting
				if onCheckExecuting != nil {
					onCheckExecuting(*check)
				}
				startTime := time.Now()
				if err := check.Execute(); err != nil {
					onCheckErrored := s.OnCheckErrored
					if onCheckErrored != nil {
						onCheckErrored(*check, err)
					}
				}
				onCheckFinished := s.OnCheckFinished
				if onCheckFinished != nil {
					onCheckFinished(*check, time.Now().Sub(startTime))
				}
			}(check)
		}
	}

	wg.Wait()

	if s.AutoReEnqueue {
		// put any pending checks back into the queue prior to shut down
		for check := range pendingChecks {
			fmt.Println("yay")
			s.checkQueue.Enqueue(*check)
		}
	}
}

func (s *Server) Stop() {
	s.cancelContext()
}
