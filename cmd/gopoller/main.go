package main

import (
	"context"
	"fmt"
	"github.com/seankndy/gopoller"
	"github.com/seankndy/gopoller/check"
	"github.com/seankndy/gopoller/command/dns"
	"github.com/seankndy/gopoller/command/junsubpool"
	"github.com/seankndy/gopoller/command/ping"
	"github.com/seankndy/gopoller/command/smtp"
	"github.com/seankndy/gopoller/command/snmp"
	dummy2 "github.com/seankndy/gopoller/handler/dummy"
	"github.com/seankndy/gopoller/handler/rrdcached"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	checkQueue := check.NewMemoryCheckQueue()
	server := gopoller.NewServer(checkQueue)
	server.MaxRunningChecks = 2
	server.AutoReEnqueue = true
	server.OnCheckExecuting = func(chk check.Check) {
		//fmt.Printf("Check beginning execution: %v\n", check)
	}
	server.OnCheckErrored = func(chk check.Check, err error) {
		fmt.Printf("CHECK ERROR: %v", err)
	}
	server.OnCheckFinished = func(chk check.Check, runDuration time.Duration) {
		fmt.Printf("Check finished execution: %v (%.3f seconds)\n", chk, runDuration.Seconds())
	}

	// signal handler
	handleSignals(cancel)

	lastCheck1 := time.Now().Add(-100 * time.Second)
	check1 := check.NewCheck(
		"check1",
		check.WithCommand(&ping.Command{
			Addr:                    "209.193.82.100",
			Count:                   5,
			Interval:                100 * time.Millisecond,
			Size:                    64,
			PacketLossWarnThreshold: 90,
			PacketLossCritThreshold: 95,
			AvgRttWarnThreshold:     20 * time.Millisecond,
			AvgRttCritThreshold:     50 * time.Millisecond,
		}),
		check.WithPeriodicSchedule(10),
		check.WithHandlers([]check.Handler{dummy2.Handler{}}),
	)
	check1.LastCheck = &lastCheck1
	checkQueue.Enqueue(*check1)

	lastCheck2 := time.Now().Add(-90 * time.Second)
	check2 := check.NewCheck(
		"check2",
		check.WithCommand(snmp.NewCommand("209.193.82.100", "public", []snmp.OidMonitor{
			*snmp.NewOidMonitor(".1.3.6.1.2.1.2.2.1.7.554", "ifAdminStatus"),
		})),
		check.WithPeriodicSchedule(10),
		check.WithHandlers([]check.Handler{dummy2.Handler{}}),
	)
	check2.LastCheck = &lastCheck2
	checkQueue.Enqueue(*check2)

	check3 := check.NewCheck(
		"check3",
		check.WithCommand(&dns.Command{
			ServerIp:              "209.193.72.2",
			ServerPort:            53,
			ServerTimeout:         3 * time.Second,
			Query:                 "www.vcn.com",
			QueryType:             dns.Host,
			Expected:              &[]string{"209.193.72.54"},
			WarnRespTimeThreshold: 20 * time.Millisecond,
			CritRespTimeThreshold: 40 * time.Millisecond,
		}),
		check.WithPeriodicSchedule(10),
		check.WithHandlers([]check.Handler{dummy2.Handler{}}),
	)
	checkQueue.Enqueue(*check3)

	check4 := check.NewCheck(
		"check4",
		check.WithCommand(&smtp.Command{
			Addr:                  "smtp.vcn.com",
			Port:                  25,
			Timeout:               5 * time.Second,
			WarnRespTimeThreshold: 25 * time.Millisecond,
			CritRespTimeThreshold: 50 * time.Millisecond,
			Send:                  "HELO gopoller.local",
			ExpectedResponseCode:  250,
		}),
		check.WithPeriodicSchedule(10),
		check.WithHandlers([]check.Handler{dummy2.Handler{}}),
	)
	checkQueue.Enqueue(*check4)

	check5 := check.NewCheck(
		"check5",
		check.WithCommand(junsubpool.NewCommand("209.193.82.44", "public", []int{1000002, 1000003, 1000004, 1000005, 1000006, 1000007, 1000008, 1000012, 1000015, 1000017, 1000019}, 95, 99)),
		check.WithPeriodicSchedule(10),
		check.WithHandlers([]check.Handler{dummy2.Handler{}}),
	)
	checkQueue.Enqueue(*check5)

	// run the server
	server.Run(ctx)

	// flush the queue prior to shut down
	checkQueue.Flush()

	fmt.Println("Exiting.")
}

func handleSignals(cancel func()) {
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT)

		defer func() {
			signal.Stop(sigCh)
			close(sigCh)
		}()

		for {
			select {
			case sig := <-sigCh:
				if sig == syscall.SIGINT {
					fmt.Println("Stopping Server...")
					cancel()

					return
				}
			}
		}
	}()
}

// example getRrdFileDefs func:
func getRrdFileDefs(chk check.Check, result check.Result) []rrdcached.RrdFileDef {
	_, isPeriodic := chk.Schedule.(check.PeriodicSchedule)
	// no spec if no metrics or if the underlying check isn't on an interval schedule
	if result.Metrics == nil || !isPeriodic {
		return nil
	}

	interval := chk.Schedule.(check.PeriodicSchedule).IntervalSeconds

	var rrdFileDefs []rrdcached.RrdFileDef
	for _, metric := range result.Metrics {
		var dst rrdcached.DST
		if metric.Type == check.ResultMetricCounter {
			dst = rrdcached.Counter
		} else {
			dst = rrdcached.Gauge
		}
		ds := rrdcached.NewDS(metric.Label, dst, interval*2, "U", "U")

		weeklyAvg := 1800
		monthlyAvg := 7200
		yearlyAvg := 43200

		rrdFileDefs = append(rrdFileDefs, rrdcached.RrdFileDef{
			Filename:    "/Users/sean/rrd_test/" + chk.Id + "/" + ds.Name(),
			DataSources: []rrdcached.DS{ds},
			RoundRobinArchives: []rrdcached.RRA{
				rrdcached.NewMinRRA(0.5, 1, 86400/interval),
				rrdcached.NewMinRRA(0.5, weeklyAvg/interval, 86400*7/interval/(weeklyAvg/interval)),
				rrdcached.NewMinRRA(0.5, monthlyAvg/interval, 86400*31/interval/(monthlyAvg/interval)),
				rrdcached.NewMinRRA(0.5, yearlyAvg/interval, 86400*366/interval/(yearlyAvg/interval)),

				rrdcached.NewAverageRRA(0.5, 1, 86400/interval),
				rrdcached.NewAverageRRA(0.5, weeklyAvg/interval, 86400*7/interval/(weeklyAvg/interval)),
				rrdcached.NewAverageRRA(0.5, monthlyAvg/interval, 86400*31/interval/(monthlyAvg/interval)),
				rrdcached.NewAverageRRA(0.5, yearlyAvg/interval, 86400*366/interval/(yearlyAvg/interval)),

				rrdcached.NewMaxRRA(0.5, 1, 86400/interval),
				rrdcached.NewMaxRRA(0.5, weeklyAvg/interval, 86400*7/interval/(weeklyAvg/interval)),
				rrdcached.NewMaxRRA(0.5, monthlyAvg/interval, 86400*31/interval/(monthlyAvg/interval)),
				rrdcached.NewMaxRRA(0.5, yearlyAvg/interval, 86400*366/interval/(yearlyAvg/interval)),
			},
			Step: time.Duration(interval) * time.Second,
		})
	}
	return rrdFileDefs
}