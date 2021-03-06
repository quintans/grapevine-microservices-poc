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
	"flag"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quintans/gomsg"
	"github.com/quintans/grapevine"
	"github.com/quintans/grapevine-microservices-poc/common"
	"github.com/quintans/maze"
	"github.com/quintans/toolkit"
	"github.com/quintans/toolkit/faults"
	"github.com/quintans/toolkit/log"
)

func init() {
	log.Register("/", log.INFO).ShowCaller(true)
}

var peer *grapevine.Peer
var servStatsLBCurr = make(map[string]*common.MyLBMetrics)
var servStatsLBNext = servStatsLBCurr
var lbmu sync.RWMutex

var servStatsCBCurr = make(map[string]*common.BreakerStats)
var servStatsCBNext = servStatsCBCurr
var cbmu sync.RWMutex

func main() {
	var logger = log.LoggerFor("dashboard")

	var httpAddr = flag.String("http", ":8070", "http address [ip]:port")
	var gvAddr = flag.String("gv", ":7070", "grapevine address [ip]:port")
	flag.Parse()

	//===================
	// Grapevine config
	//===================

	peer = grapevine.NewPeer(grapevine.Config{
		Addr: *gvAddr,
		Beacon: grapevine.Beacon{
			Name: common.ClusterName,
		},
	})
	peer.SetLogger(logger)

	// collects statistics of all LBs
	peer.Handle(common.ServiceStatsLB, func(stats []common.MyLBMetrics) {
		lbmu.Lock()
		// merge stats from diferente grapevine peers
		for _, stat := range stats {
			var key = stat.Name + common.StatsKeySep + stat.Location
			// use bucket #2
			var ss = servStatsLBNext[key]
			if ss == nil {
				var v = stat
				servStatsLBNext[key] = &v
			} else {
				// compute accumulated average: (W0 * V0 + V1) / (W0 + 1)
				var weight = ss.Weight + 1
				ss.Fails = (ss.Weight*ss.Fails + stat.Fails) / weight
				ss.Successes = (ss.Weight*ss.Successes + stat.Successes) / weight
				ss.Weight = weight
				ss.Quarantine = stat.Quarantine
				ss.Type = stat.Type
			}
		}
		lbmu.Unlock()
	})

	// collects statistics of all CBs
	peer.Handle(common.ServiceStatsCB, func(stats []common.BreakerStats) {
		cbmu.Lock()
		// merge stats from diferente grapevine peers
		for _, stat := range stats {
			// use bucket #2
			var ss = servStatsCBNext[stat.Name]
			if ss == nil {
				var v = stat
				servStatsCBNext[stat.Name] = &v
			} else {
				// compute accumulated average: (W0 * V0 + V1) / (W0 + 1)
				var weight = ss.Weight + 1
				ss.Fails = (ss.Weight*ss.Fails + stat.Fails) / weight
				ss.Successes = (ss.Weight*ss.Successes + stat.Successes) / weight
				ss.Weight = weight
				ss.State = stat.State
				ss.Type = stat.Type
			}
		}
		cbmu.Unlock()
	})
	peer.AddNewTopicListener(func(event gomsg.TopicEvent) {
		// for statistics, we only consider services under "api/"
		if strings.HasPrefix(event.Name, "api/") {
			var addr = peerAddress(event.Wire)
			addEndpoint(event.Name, addr)
		}
	})
	peer.AddDropTopicListener(func(event gomsg.TopicEvent) {
		if strings.HasPrefix(event.Name, "api/") {
			var addr = peerAddress(event.Wire)
			dropEndpoint(event.Name, addr)
		}
	})

	// clean statistics. Move statistics from bucket next to current
	toolkit.NewTicker(time.Second*common.StatsPeriod, func(t time.Time) {
		lbmu.Lock()
		servStatsLBCurr = servStatsLBNext
		// reset
		servStatsLBNext = make(map[string]*common.MyLBMetrics)
		lbmu.Unlock()

		cbmu.Lock()
		servStatsCBCurr = servStatsCBNext
		// reset
		servStatsCBNext = make(map[string]*common.BreakerStats)
		cbmu.Unlock()
	})

	go func() {
		if err := <-peer.Bind(); err != nil {
			panic(err)
		}
	}()

	//===================
	// Maze config
	//===================
	maze.SetLogger(logger)
	// creates maze with the default context factory.
	var mz = maze.NewMaze(nil)

	var sse = maze.NewSseBroker()
	mz.Push("/stats", sse.Serve)

	sse.OnConnect = func() (maze.Sse, error) {
		var result, err = encode()
		if err != nil {
			logger.Errorf("Unable to encode on connect.\n %+v", err)
			return maze.Sse{}, err
		}
		// send data
		return maze.NewSse(string(result)), nil
	}

	// every 10s send data using sse
	toolkit.NewTicker(time.Second*common.StatsPeriod, func(t time.Time) {
		if sse.HasSubscribers() {
			var result, err = encode()
			if err != nil {
				logger.Errorf("Unable to encode on tick.\n %+v", err)
				return
			}
			// send data
			var e = maze.NewSse(string(result))
			sse.Send(e)
		}
	})

	mz.Static("/*", "./static")

	if err := mz.ListenAndServe(*httpAddr); err != nil {
		panic(err)
	}
}

func peerAddress(w *gomsg.Wire) string {
	return w.RemoteMetadata()[grapevine.PeerAddressKey].(string)
}

func addEndpoint(name, addr string) {
	// LB
	var key = name + common.StatsKeySep + addr
	lbmu.Lock()
	servStatsLBNext[key] = &common.MyLBMetrics{
		Stats: common.Stats{
			Name: name,
		},
		Location: addr,
	}
	lbmu.Unlock()

	// CB
	cbmu.Lock()
	servStatsCBNext[name] = &common.BreakerStats{
		Stats: common.Stats{
			Name: name,
		},
	}
	cbmu.Unlock()
}

func dropEndpoint(name, addr string) {
	// LB
	lbmu.Lock()
	delete(servStatsLBNext, name+common.StatsKeySep+addr)
	lbmu.Unlock()

	// CB
	cbmu.Lock()
	delete(servStatsCBNext, name)
	cbmu.Unlock()

}

func encode() ([]byte, error) {
	lbmu.RLock()
	// collect stats
	// LB Stats
	var arrLb = make([]*common.MyLBMetrics, len(servStatsLBCurr))
	var i = 0
	for _, v := range servStatsLBCurr {
		arrLb[i] = v
		i++
	}
	lbmu.RUnlock()

	cbmu.RLock()
	// CB Stats
	var arrCb = make([]*common.BreakerStats, len(servStatsCBCurr))
	i = 0
	for _, v := range servStatsCBCurr {
		arrCb[i] = v
		i++
	}
	cbmu.RUnlock()

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
		return nil, faults.Wrapf(err, "Failed to encode %+v", value)
	}

	return result, nil
}
