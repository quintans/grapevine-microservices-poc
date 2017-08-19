package main

import (
	"flag"
	"math/rand"
	"time"

	"github.com/quintans/grapevine"
	"github.com/quintans/grapevine-microservices/common"
	"github.com/quintans/toolkit/log"
)

func init() {
	log.Register("/", log.DEBUG).ShowCaller(true)
}

func main() {
	//========
	// Config
	//========
	var logger = log.LoggerFor("helloservice")
	var bad = flag.Int("bad", 0, "error percentage rate")
	var gvAddr = flag.String("gv", ":5000", "grapevine address [ip]:port")
	flag.Parse()

	var peer = grapevine.NewPeer(grapevine.Config{
		BeaconName: common.ClusterName,
	})
	peer.SetLogger(logger)

	//==========
	// Services
	//==========
	var realBad = *bad / 3
	logger.Infof("Badness of %d", *bad)
	logger.Infof("Real Badness of %d", realBad)
	rand.Seed(time.Now().UnixNano())
	peer.Handle(common.ServiceHello, func(name string) string {
		var p = rand.Intn(100)
		if p < realBad {
			logger.Warnf("Badness of %d. Hello will timeout", p)
			time.Sleep(time.Second * 20)
		} else if p < *bad {
			logger.Warnf("Badness of %d. 'Hello' will take longer", p)
			time.Sleep(time.Second * 2)
			return "Hellozzz " + name
		}

		return "Hello " + name
	})

	logger.Infof("Grapevine at %s", *gvAddr)
	if err := <-peer.Bind(*gvAddr); err != nil {
		panic(err)
	}
}
