package main

import (
	"flag"
	"time"

	"github.com/quintans/grapevine"
	"github.com/quintans/grapevine-microservices-poc/common"
	"github.com/quintans/maze"
	"github.com/quintans/toolkit"
	"github.com/quintans/toolkit/breaker"
	"github.com/quintans/toolkit/log"
)

func init() {
	log.Register("/", log.DEBUG).ShowCaller(true)
}

func main() {
	var logger = log.LoggerFor("gateway")

	var httpAddr = flag.String("http", ":8080", "http address [ip]:port")
	var gvAddr = flag.String("gv", ":7080", "grapevine address [ip]:port")
	flag.Parse()

	var peer = grapevine.NewPeer(grapevine.Config{
		BeaconName: common.ClusterName,
	})
	peer.SetLogger(logger)
	var lb = common.NewMyLB()
	peer.SetLoadBalancer(lb)

	go func() {
		logger.Infof("Grapevine at %s", *gvAddr)
		if err := <-peer.Bind(*gvAddr); err != nil {
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

	mz.GET("/api/:Name", func(c maze.IContext) error {
		var values = c.PathValues()
		var name = values.AsString("Name")

		logger.Debugf("name=%s", name)

		var result string
		// call the remote service with circuit breaker
		<-cb.Try(
			func() error {
				return <-peer.RequestTimeout(common.ServiceHello, name, func(r string) {
					result = r
				}, time.Second*3)
			},
			func(e error) error {
				result = "Ups, fallback..."
				return nil
			},
		)

		return c.JSON(result)
	})

	// collects all services statistics
	const period = 10
	toolkit.NewTicker(time.Second*common.StatsPeriod, func(t time.Time) {
		s := lb.ClearStats()
		// LB stats
		peer.Publish(common.ServiceStatsLB, s)
		// CB stats
		var stats = make([]common.BreakerStats, 1)
		// for "Hello" CB
		stats[0] = common.BreakerStats{
			Stats: bm.Clear(),
			Name:  common.ServiceHello,
			State: cb.State(),
		}
		peer.Publish(common.ServiceStatsCB, stats)
	})

	mz.GET("/", func(c maze.IContext) error {
		return c.JSON("hello")
	})

	logger.Infof("Listening http at %s", *httpAddr)
	if err := mz.ListenAndServe(*httpAddr); err != nil {
		panic(err)
	}

}
