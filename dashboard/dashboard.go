package main

/*
Report for each service from the perspective of the Load Balancer (LB).
Every LB will send its statistics.

Service nodes |     State   |   Success % (10s)   |   Location
:-------------|:-----------:|:-------------------:|----------------
hello         |   OK        |   (15990/143) 99%   | 127.0.0.1:9001
hello         |   OK        |                     | 127.0.0.1:9002
hello         |   **NOK**   |         0%          | 127.0.0.1:9003
hi            |   **NOK**   |                     | 127.0.0.1:9011
hi            |   **NOK**   |                     | 127.0.0.1:9012


Global report for each service, from the perspective of a Circuit Breaker (CB).

Service nodes | CB State  | close/all | Global Success % (10s)
--------------|:---------:|:---------:|-----------------------
hello         |  Closed   |    2/3    | (785457/85) 100%
hi            | **Open**  |    0/2    |
*/

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quintans/gomsg"
	"github.com/quintans/grapevine"
	"github.com/quintans/grapevine-microservices-poc/common"
	"github.com/quintans/maze"
	"github.com/quintans/toolkit"
	"github.com/quintans/toolkit/log"
)

func init() {
	log.Register("/", log.DEBUG).ShowCaller(true)
}

var peer *grapevine.Peer

func main() {
	var logger = log.LoggerFor("dashboard")

	var httpAddr = flag.String("http", ":8070", "http address [ip]:port")
	var gvAddr = flag.String("gv", ":7070", "grapevine address [ip]:port")
	flag.Parse()

	//===================
	// Grapevine config
	//===================

	peer = grapevine.NewPeer(grapevine.Config{
		BeaconName: common.ClusterName,
	})
	peer.SetLogger(logger)

	var servStatsLBCurr = make(map[string]*common.MyLBMetrics)
	var servStatsLBNext = servStatsLBCurr
	var lbmu sync.RWMutex
	// collects statistics of all LBs
	peer.Handle(common.ServiceStatsLB, func(stats []common.MyLBMetrics) {
		lbmu.Lock()
		// merge stats from diferente grapevine peers
		for _, stat := range stats {
			var key = stat.Name + common.StatsKeySep + stat.Location
			// use bucket #2
			var ss = servStatsLBNext[key]
			if ss != nil {
				// compute accumulated average: (W0 * V0 + V1) / (W0 + 1)
				var weight = ss.Weight + 1
				ss.Fails = (ss.Weight*ss.Fails + stat.Fails) / weight
				ss.Successes = (ss.Weight*ss.Successes + stat.Successes) / weight
				ss.Weight = weight
				ss.Quarantine = stat.Quarantine
			}
		}
		lbmu.Unlock()
	})

	var servStatsCBCurr = make(map[string]*common.BreakerStats)
	var servStatsCBNext = servStatsCBCurr
	var cbmu sync.RWMutex
	// collects statistics of all CBs
	peer.Handle(common.ServiceStatsCB, func(stats []common.BreakerStats) {
		cbmu.Lock()
		// merge stats from diferente grapevine peers
		for _, stat := range stats {
			// use bucket #2
			var ss = servStatsCBNext[stat.Name]
			if ss != nil {
				// compute accumulated average: (W0 * V0 + V1) / (W0 + 1)
				var weight = ss.Weight + 1
				ss.Fails = (ss.Weight*ss.Fails + stat.Fails) / weight
				ss.Successes = (ss.Weight*ss.Successes + stat.Successes) / weight
				ss.Weight = weight
				ss.State = stat.State
			}
		}
		cbmu.Unlock()
	})

	// clean statistics. Move statistics from bucket next to current
	toolkit.NewTicker(time.Second*common.StatsPeriod, func(t time.Time) {
		lbmu.Lock()
		cbmu.Lock()
		servStatsLBCurr = servStatsLBNext
		servStatsLBNext = make(map[string]*common.MyLBMetrics)
		// inti
		for k, stat := range servStatsLBCurr {
			servStatsLBNext[k] = &common.MyLBMetrics{
				Name:     stat.Name,
				Location: stat.Location,
			}
		}

		servStatsCBCurr = servStatsCBNext
		servStatsCBNext = make(map[string]*common.BreakerStats)
		// inti
		for k, stat := range servStatsCBCurr {
			servStatsCBNext[k] = &common.BreakerStats{
				Name: stat.Name,
			}
		}

		lbmu.Unlock()
		cbmu.Unlock()
	})
	peer.AddNewTopicListener(func(event gomsg.TopicEvent) {
		// for statistics, we only consider services under "api/"
		if strings.HasPrefix(event.Name, "api/") {
			var addr = event.Wire.RemoteMetadata()[grapevine.PeerAddressKey].(string)
			var key = event.Name + common.StatsKeySep + addr
			lbmu.Lock()
			cbmu.Lock()
			// LB
			servStatsLBNext[key] = &common.MyLBMetrics{
				Name:     event.Name,
				Location: addr,
			}

			// CB
			servStatsCBNext[event.Name] = &common.BreakerStats{
				Name: event.Name,
			}

			lbmu.Unlock()
			cbmu.Unlock()
		}
	})
	peer.AddDropTopicListener(func(event gomsg.TopicEvent) {
		if strings.HasPrefix(event.Name, "api/") {
			lbmu.Lock()
			cbmu.Lock()
			// LB
			var addr = event.Wire.RemoteMetadata()[grapevine.PeerAddressKey].(string)
			delete(servStatsLBNext, event.Name+common.StatsKeySep+addr)
			// CB
			delete(servStatsCBNext, event.Name)
			lbmu.Unlock()
			cbmu.Unlock()
		}
	})

	go func() {
		if err := <-peer.Bind(*gvAddr); err != nil {
			panic(err)
		}
	}()

	//===================
	// Maze config
	//===================
	maze.SetLogger(logger)
	// creates maze with the default context factory.
	var mz = maze.NewMaze(nil)

	mz.Push("/ssedemo", func(c maze.IContext) error {
		var w = c.GetResponse()
		var f, ok = w.(http.Flusher)
		if !ok {
			return errors.New("No flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Expires", "-1")
		for t := range time.Tick(time.Second) {
			var _, err = w.Write([]byte(fmt.Sprintf("data: The server time is: %s\n\n", t)))
			if err != nil {
				return err
			}
			f.Flush()
			fmt.Println("PING")
		}

		return nil
	})

	mz.Push("/stats", func(c maze.IContext) error {
		var w = c.GetResponse()
		var f, ok = w.(http.Flusher)
		if !ok {
			return errors.New("No flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Expires", "-1")
		for {
			lbmu.Lock()
			cbmu.Lock()
			// collect stats
			// LB Stats
			var arrLb = make([]*common.MyLBMetrics, len(servStatsLBCurr))
			var i = 0
			for _, v := range servStatsLBCurr {
				arrLb[i] = v
				i++
			}
			// CB Stats
			var arrCb = make([]*common.BreakerStats, len(servStatsCBCurr))
			i = 0
			for _, v := range servStatsCBCurr {
				arrCb[i] = v
				i++
			}
			lbmu.Unlock()
			cbmu.Unlock()
			// sort LB
			sort.Slice(arrLb, func(i, j int) bool {
				return strings.Compare(arrLb[i].Name, arrLb[j].Name) < 0 ||
					strings.Compare(arrLb[i].Location, arrLb[j].Location) < 0
			})

			// sort CB
			sort.Slice(arrCb, func(i, j int) bool {
				return strings.Compare(arrCb[i].Name, arrCb[j].Name) < 0
			})

			var value = struct {
				Lb []*common.MyLBMetrics
				Cb []*common.BreakerStats
			}{
				arrLb,
				arrCb,
			}

			result, err := json.Marshal(value)
			if err != nil {
				return err
			}
			// writing sets status to OK
			_, err = w.Write([]byte(fmt.Sprintf("data: %s\n\n", string(result))))
			if err != nil {
				return err
			}

			f.Flush()
			time.Sleep(time.Second * common.StatsPeriod)
		}

		return nil
	})

	mz.Static("/*", "./static")

	if err := mz.ListenAndServe(*httpAddr); err != nil {
		panic(err)
	}

}
