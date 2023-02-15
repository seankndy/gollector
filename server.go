package gollector

import (
	"fmt"
	"time"
)

type Server struct {
	checkQueue       CheckQueue
	maxRunningChecks uint64
	stop             chan struct{}
}

type ServerConfig struct {
	MaxRunningChecks uint64
}

func NewServer(config ServerConfig, checkQueue CheckQueue) *Server {
	return &Server{
		checkQueue:       checkQueue,
		maxRunningChecks: config.MaxRunningChecks,
	}
}

func (s *Server) Run() {
	s.stop = make(chan struct{})

	runningLimiter := make(chan struct{}, s.maxRunningChecks)
	defer close(runningLimiter)

	pendingChecks := make(chan *Check, s.maxRunningChecks)
	go func() { // populate pendingCheck channel from queue indefinitely
		defer close(pendingChecks)

		for loop := true; loop; {
			select {
			case _, ok := <-s.stop:
				if !ok {
					loop = false
					break
				}
			default:
			}

			if check := s.checkQueue.Dequeue(); check != nil {
				pendingChecks <- check
			} else {
				time.Sleep(250 * time.Millisecond)
			}
		}
	}()

	for loop := true; loop; {
		select {
		case _, ok := <-s.stop:
			if !ok {
				loop = false
				break
			}
		case check := <-pendingChecks:
			runningLimiter <- struct{}{}

			go func() {
				defer func() {
					s.checkQueue.Enqueue(*check)
					<-runningLimiter
				}()

				result, err := check.Execute()
				if err != nil {
					fmt.Printf("failed to execute check: %v\n", err)
				}

				fmt.Println(result)
			}()
		}
	}

	// only get here when server stopped
	// put any pending checks back into the queue
	for check := range pendingChecks {
		s.checkQueue.Enqueue(*check)
	}

	fmt.Println("Server stopped")
}

func (s *Server) Stop() {
	close(s.stop)
}
