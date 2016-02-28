package doorman

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

type collector struct {
	server *Server
	mu     sync.Mutex
	has    *prometheus.GaugeVec
	wants  *prometheus.GaugeVec
	count  *prometheus.GaugeVec
}

// NewCollector returns a custom Prometheus collector that creates
// metrics for how much capacity has been assigned
// (doorman_server_sum_has), requested (doorman_server_sum_wants), and
// the total number of clients (doorman_server_client_count), with the
// resource id as the label. It has to be registered using
// prometheus.Register.
func NewCollector(server *Server) prometheus.Collector {
	labels := []string{"resource"}
	return &collector{
		server: server,
		has: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "doorman",
			Subsystem: "server",
			Name:      "sum_has",
			Help:      "All capacity assigned to clients for a resource.",
		}, labels),
		wants: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "doorman",
			Subsystem: "server",
			Name:      "sum_wants",
			Help:      "All capacity requested by clients for a resource.",
		}, labels),
		count: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "doorman",
			Subsystem: "server",
			Name:      "client_count",
			Help:      "Number of clients requesting this resource.",
		}, labels),
	}
}

func (c *collector) Describe(ch chan<- *prometheus.Desc) {
	c.has.Describe(ch)
	c.wants.Describe(ch)
	c.count.Describe(ch)
}

func (c *collector) Collect(ch chan<- prometheus.Metric) {
	status := c.server.Status()
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, res := range status.Resources {
		c.has.WithLabelValues(id).Set(res.SumHas)
		c.wants.WithLabelValues(id).Set(res.SumWants)
		c.count.WithLabelValues(id).Set(float64(res.Count))
	}
	c.has.Collect(ch)
	c.wants.Collect(ch)
	c.count.Collect(ch)

	c.has.Reset()
	c.wants.Reset()
	c.count.Reset()
}
