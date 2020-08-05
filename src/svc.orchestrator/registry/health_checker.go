package registry

import (
	"clients"
	"context"
	"fmt"
	"log"
	"time"

	"github.com/eapache/go-resiliency/retrier"
	"svc.orchestrator/storage"
	"svc.orchestrator/types"
)

type healthChecker struct {
	info       types.RegistrantInfo
	ticker     *time.Ticker
	quit       chan struct{}
	done       chan types.RegistrantInfo
	client     clients.HeartbeatClient
	aggregator *MetricsAggregator
}

func newHealthChecker(info types.RegistrantInfo, done chan types.RegistrantInfo, aggregator *MetricsAggregator) *healthChecker {
	client := clients.NewHeartbeatClient(info.ControlAddress)

	r := healthChecker{
		info:       info,
		done:       done,
		client:     client,
		quit:       make(chan struct{}),
		aggregator: aggregator,
	}

	go r.startHealthCheck()

	return &r
}

func (r *healthChecker) startHealthCheck() {
	r.ticker = time.NewTicker(10 * time.Second)
	retries := maxHeartBeatRetries
	defer func() {
		log.Printf("Stopping healthcheck for %s", r.info.String())
		r.ticker.Stop()
		log.Printf("Done stopping timer for %s", r.info.String())
		r.done <- r.info
		log.Printf("Done stopping healthcheck for %s", r.info.String())
	}()

	log.Printf("Starting healthcheck for %s", r.info.String())

	for retries > 0 {
		select {
		case <-r.quit:
			log.Printf("Quiting healthcheck for %s", r.info.String())
			return
		case <-r.ticker.C:
			log.Printf("Sending heartbeat for %s (%s).......", r.info.ServiceName, r.info.ControlAddress)
			if err := r.sendHeartBeat(); err != nil {
				retries--
				log.Printf("Error sending heartbeat to service=%s (Retries remaining=%d! err=%s",
					r.info.ServiceName, retries, err.Error())
				return
			} else {
				retries = maxHeartBeatRetries
			}
		}
	}
}

func (r *healthChecker) stopHealthCheck() {
	r.quit <- struct{}{}
}

func (r *healthChecker) sendHeartBeat() error {
	var err error
	var resp *clients.HeartbeatResponse
	var expRetrier = retrier.New(retrier.ExponentialBackoff(4, 500*time.Millisecond), nil)

	if err := expRetrier.Run(func() error {
		req := clients.HeartbeatRequest{}

		resp, err = r.client.Heartbeat(context.Background(), &req)
		if err != nil {
			return err
		}

		return nil
	}); err != nil {
		return err
	}

	if resp == nil {
		return fmt.Errorf("hearteat failed for %s", r.info)
	}

	for _, stats := range resp.Stats {
		r.aggregator.AddDataPoint(&clients.DataPoint{
			MetricID:  storage.MetricCPU,
			ServiceID: stats.ServiceID,
			TS:        stats.TS,
			Value:     stats.CPU,
		})
		r.aggregator.AddDataPoint(&clients.DataPoint{
			MetricID:  storage.MetricMemory,
			ServiceID: stats.ServiceID,
			TS:        stats.TS,
			Value:     stats.Mem,
		})
		r.aggregator.AddDataPoint(&clients.DataPoint{
			MetricID:  storage.MetricThreads,
			ServiceID: stats.ServiceID,
			TS:        stats.TS,
			Value:     stats.Threads,
		})
		r.aggregator.AddDataPoint(&clients.DataPoint{
			MetricID:  storage.MetricNumGoroutine,
			ServiceID: stats.ServiceID,
			TS:        stats.TS,
			Value:     stats.NumGoroutines,
		})
	}

	log.Printf("%s: %+v", r.info, resp.Stats)

	return nil
}
