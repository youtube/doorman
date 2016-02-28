package doorman

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	pb "github.com/youtube/doorman/proto/doorman"
)

func TestCollector(t *testing.T) {
	s, err := MakeTestServer(makeResourceTemplate("*", pb.Algorithm_FAIR_SHARE))
	if err != nil {
		t.Fatal(err)
	}
	c := NewCollector(s)
	prometheus.EnableCollectChecks(true)
	if err := prometheus.Register(c); err != nil {
		t.Fatal(err)
	}
}
