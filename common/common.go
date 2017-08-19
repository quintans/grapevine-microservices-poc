package common

import (
	"strings"
	"sync"

	"github.com/quintans/gomsg"
	"github.com/quintans/grapevine"
	"github.com/quintans/toolkit/breaker"
)

const StatsPeriod = 10
const ServiceHello = "api/Hello"
const ServiceStatsCB = "StatsCB"
const ServiceStatsLB = "StatsLB"

const StatsKeySep = "@"

const ClusterName = "XPTO"

type BreakerStats struct {
	breaker.Stats
	Name   string
	State  breaker.EState
	Weight uint32
}

var _ breaker.Metrics = &BreakerMetrics{}

type BreakerMetrics struct {
	sync.RWMutex
	breaker.Stats
}

func (m *BreakerMetrics) IncSuccess() {
	m.Lock()
	m.Successes++
	m.Unlock()
}

func (m *BreakerMetrics) IncFailure() {
	m.Lock()
	m.Fails++
	m.Unlock()
}

func (m *BreakerMetrics) Clear() breaker.Stats {
	m.RLock()
	defer m.RUnlock()
	var s = m.Stats
	m.Stats.Successes = 0
	m.Stats.Fails = 0
	return s
}

type MyLB struct {
	sync.RWMutex
	gomsg.SimpleLB
	metrics map[string]*MyLBMetrics
}

type MyLBMetrics struct {
	Successes    uint32
	Fails        uint32
	Name         string
	Location     string
	Quarantine   bool
	Weight       uint32
	inQuarantine func(string) bool
}

func NewMyLB() MyLB {
	return MyLB{
		SimpleLB: gomsg.NewSimpleLB(),
		metrics:  make(map[string]*MyLBMetrics),
	}
}

func (lb MyLB) Remove(w *gomsg.Wire) {
	lb.SimpleLB.Remove(w)

	lb.Lock()
	// remove all metrics for this wire
	var loc = w.RemoteMetadata()[grapevine.PeerAddressKey].(string)
	var suffix = StatsKeySep + loc
	for k := range lb.metrics {
		if strings.HasSuffix(k, suffix) {
			delete(lb.metrics, k)
		}
	}
	lb.Unlock()
}

func (lb MyLB) getMetrics(w *gomsg.Wire, m gomsg.Envelope) *MyLBMetrics {
	var loc = w.RemoteMetadata()[grapevine.PeerAddressKey].(string)
	var name = m.Name + StatsKeySep + loc
	var metrics = lb.metrics[name]
	if metrics == nil {
		metrics = &MyLBMetrics{}
		metrics.Name = m.Name
		metrics.Location = loc
		metrics.inQuarantine = w.Policy.InQuarantine
		lb.metrics[name] = metrics
	}
	return metrics
}

func (lb MyLB) Done(w *gomsg.Wire, m gomsg.Envelope, e error) {
	lb.SimpleLB.Done(w, m, e)

	if strings.HasPrefix(m.Name, "api/") {
		lb.Lock()
		var metrics = lb.getMetrics(w, m)
		if metrics != nil {
			metrics.Quarantine = w.Policy.InQuarantine(m.Name)
			if e == nil {
				metrics.Successes++
			} else {
				metrics.Fails++
			}
		}
		lb.Unlock()
	}
}

func (lb MyLB) ClearStats() []MyLBMetrics {
	lb.RLock()
	var arr = make([]MyLBMetrics, len(lb.metrics))
	var i = 0
	for _, v := range lb.metrics {
		arr[i] = *v
		i++
		v.Successes = 0
		v.Fails = 0
		v.Quarantine = v.inQuarantine(v.Name)
	}
	lb.RUnlock()
	return arr
}
