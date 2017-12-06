package main

import (
	"flag"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/quintans/gomsg"
	"github.com/quintans/grapevine"
	"github.com/quintans/grapevine-microservices-poc/common"
	"github.com/quintans/maze"
	"github.com/quintans/toolkit"
	"github.com/quintans/toolkit/breaker"
	"github.com/quintans/toolkit/log"
)

func init() {
	log.Register("/", log.INFO).ShowCaller(true)
}

func port(a string) string {
	var i = strings.LastIndex(a, ":")
	return a[i:]
}

const httpEndpoint = "/api/:Name"

func main() {
	var logger = log.LoggerFor("gateway")

	var httpAddr = flag.String("http", ":8080", "http address [ip]:port")
	var gvAddr = flag.String("gv", ":7080", "grapevine address [ip]:port")
	flag.Parse()

	var peer = grapevine.NewPeer(grapevine.Config{
		Addr: *gvAddr,
		Beacon: grapevine.Beacon{
			Name: common.ClusterName,
		},
	})
	peer.SetLogger(logger)
	peer.Metadata()[common.HttpProvider] = httpEndpoint
	var lb = common.NewMyLB()
	peer.SetLoadBalancer(lb)

	go func() {
		logger.Infof("Grapevine at %s", *gvAddr)
		if err := <-peer.Bind(); err != nil {
			panic(err)
		}
	}()

	maze.SetLogger(logger)
	// creates maze with the default context factory.
	var mz = maze.NewMaze(nil)

	// create circuit breaker
	var cb = breaker.New(breaker.Config{
		Maxfailures:  3,
		ResetTimeout: time.Second * 15,
	})
	var bm = &common.BreakerMetrics{}
	cb.SetMetrics(bm)

	var mugw sync.RWMutex
	var gwStats common.MyLBMetrics
	var ip, err = gomsg.IP()
	if err != nil {
		panic(err)
	}
	gwStats.Location = "http://" + ip + ":" + port(*httpAddr)
	gwStats.Name = httpEndpoint
	gwStats.Period = common.StatsPeriod
	gwStats.Type = common.HttpProviderType

	mz.GET(httpEndpoint, func(c maze.IContext) error {
		var values = c.PathValues()
		var name = values.AsString("Name")

		logger.Debugf("name=%s", name)

		var result string
		// call the remote service with circuit breaker
		var err = <-cb.Try(
			func() error {
				return <-peer.RequestTimeout(common.ServiceHello, name, func(r string) {
					result = r
				}, time.Second)
			},
			func(e error) error {
				result = "Ups, fallback..."
				return nil
			},
		)
		mugw.Lock()
		if err == nil {
			gwStats.Successes++
		} else {
			gwStats.Fails++
		}
		mugw.Unlock()

		return c.JSON(http.StatusOK, result)
	})

	// collects all services statistics
	toolkit.NewTicker(time.Second*common.StatsPeriod, func(t time.Time) {
		var lbStats = lb.ClearStats()
		for i := 0; i < len(lbStats); i++ {
			lbStats[i].Period = common.StatsPeriod
			lbStats[i].Type = common.GrapevineType
		}
		lbStats = append(lbStats, gwStats)
		// LB stats
		peer.Publish(common.ServiceStatsLB, lbStats)

		// CB stats
		var cbStats = make([]common.BreakerStats, 2)
		// for "Hello" CB
		cbStats[0] = common.BreakerStats{
			Stats: bm.Clear(),
			State: cb.State(),
		}
		cbStats[0].Name = common.ServiceHello
		cbStats[0].Period = common.StatsPeriod
		cbStats[0].Type = common.GrapevineType

		cbStats[1] = common.BreakerStats{
			Stats: gwStats.Stats,
			State: breaker.CLOSE,
		}
		peer.Publish(common.ServiceStatsCB, cbStats)

		// gateway stats
		gwStats.Fails = 0
		gwStats.Successes = 0
	})

	mz.GET("/", func(c maze.IContext) error {
		return c.JSON(http.StatusOK, "hello")
	})

	logger.Infof("Listening http at %s", *httpAddr)
	if err := mz.ListenAndServe(*httpAddr); err != nil {
		panic(err)
	}
}
