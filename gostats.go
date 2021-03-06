package gostats

import (
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/alexcesaro/statsd"
)

type collectorList []func() map[string]float64

type GoStats struct {
	ClientName   string
	Hostname     string
	PushInterval time.Duration
	StatsdHost   string
	PushTicker   *time.Ticker
	Conn         *statsd.Client
	Collectors   collectorList

	tags []string
}

func sanitizeMetricName(name string) string {
	for _, c := range []string{"/", ".", " "} {
		name = strings.Replace(name, c, "_", -1)
	}

	r := regexp.MustCompile("[^a-zA-Z0-9-_]")
	name = r.ReplaceAllString(name, "")

	return name
}

func New(tags ...string) *GoStats {
	s := GoStats{}
	s.ClientName = "gostats"
	host, _ := os.Hostname()
	s.Hostname = sanitizeMetricName(host)

	s.Collectors = collectorList{memStats, goRoutines, cgoCalls, gcs}
	s.tags = tags
	return &s
}

func Start(statsdHost string, pushInterval int, clientName string, tags ...string) (*GoStats, error) {
	s := New(tags...)

	s.StatsdHost = statsdHost
	s.PushInterval = time.Duration(pushInterval) * time.Second
	s.ClientName = clientName

	err := s.Start()

	return s, err
}

func (s *GoStats) MetricBase() string {
	return strings.Join([]string{"gostats", s.ClientName, ""}, ".")
}

func (s *GoStats) Start() error {
	var err error
	s.Conn, err = statsd.New(statsd.Prefix(s.MetricBase()), statsd.TagsFormat(statsd.InfluxDB), statsd.Tags(s.tags...), statsd.Address(s.StatsdHost))
	if err != nil {
		return err
	}
	s.PushTicker = time.NewTicker(s.PushInterval)
	go s.startSender()

	return nil
}

func (s *GoStats) Stop() {
	s.PushTicker.Stop()
}

func (s *GoStats) startSender() {
	for {
		select {
		case <-s.PushTicker.C:
			s.doSend()
		}
	}
}

func (s *GoStats) doSend() {
	for _, collector := range s.Collectors {
		metrics := collector()

		for metricName, metricValue := range metrics {
			s.Conn.Gauge(metricName, metricValue)
		}
	}
}

func memStats() map[string]float64 {
	m := runtime.MemStats{}
	runtime.ReadMemStats(&m)
	metrics := map[string]float64{
		"memory.objects.HeapObjects": float64(m.HeapObjects),
		"memory.summary.Alloc":       float64(m.Alloc),
		"memory.counters.Mallocs":    perSecondCounter("mallocs", int64(m.Mallocs)),
		"memory.counters.Frees":      perSecondCounter("frees", int64(m.Frees)),
		"memory.summary.System":      float64(m.HeapSys),
		"memory.heap.Idle":           float64(m.HeapIdle),
		"memory.heap.InUse":          float64(m.HeapInuse),
	}

	return metrics
}

func goRoutines() map[string]float64 {
	return map[string]float64{
		"goroutines.total": float64(runtime.NumGoroutine()),
	}
}

func cgoCalls() map[string]float64 {
	return map[string]float64{
		"cgo.calls": perSecondCounter("cgoCalls", runtime.NumCgoCall()),
	}
}

var lastGcPause float64
var lastGcTime uint64
var lastGcPeriod float64

func gcs() map[string]float64 {
	m := runtime.MemStats{}
	runtime.ReadMemStats(&m)
	gcPause := float64(m.PauseNs[(m.NumGC+255)%256])
	if gcPause > 0 {
		lastGcPause = gcPause
	}

	if m.LastGC > lastGcTime {
		lastGcPeriod = float64(m.LastGC - lastGcTime)
		if lastGcPeriod == float64(m.LastGC) {
			lastGcPeriod = 0
		}

		lastGcPeriod = lastGcPeriod / 1000000

		lastGcTime = m.LastGC
	}

	return map[string]float64{
		"gc.perSecond":   perSecondCounter("gcs-total", int64(m.NumGC)),
		"gc.pauseTimeNs": lastGcPause,
		"gc.pauseTimeMs": lastGcPause / float64(1000000),
		"gc.period":      lastGcPeriod,
	}
}
