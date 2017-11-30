// Copyright 2016 Google, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package option

import (
	"flag"
	"time"

	_ "expvar"
	"fmt"
	log "github.com/golang/glog"
	"github.com/spf13/pflag"
	"github.com/youtube/doorman/go/flagutil"
	_ "net/http/pprof"
	"os"
)

var (
	Statusz = `
<h2>Mastership</h2>
<p>
{{if .IsMaster}}
  This <strong>is</strong> the master.
{{else}}
This is <strong>not</strong> the master.
  {{with .CurrentMaster}}
    The current master is <a href="http://{{.}}">{{.}}</a>
  {{else}}
    The current master is unknown.
  {{end}}
{{end}}
</p>
{{with .Election}}{{.}}{{end}}

<h2>Resources</h2>
{{ with .Resources }}
<table border="1">
  <thead>
    <tr>
      <td>ID</td>
      <td>Capacity</td>
      <td>SumHas</td>
      <td>SumWants</td>
      <td>Clients</td>
      <td>Learning</td>
      <td>Algorithm</td>
    </tr>
  </thead>
  {{range .}}
  <tr>
    <td><a href="/debug/resources?resource={{.ID}}">{{.ID}}</a></td>
    <td>{{.Capacity}}</td>
    <td>{{.SumHas}}</td>
    <td>{{.SumWants}}</td>
    <td>{{.Count}}</td>
    <td>{{.InLearningMode}}
    <td><code>{{.Algorithm}}</code></td>
  </tr>
  {{end}}
</table>
{{else}}
No resources in the store.
{{end}}

<h2>Configuration</h2>
<pre>{{.Config}}</pre>
`
)

type ServerConfiguration struct {
	Port       int
	DebugPort  int
	ServerRole string

	HostName string
	Config   string

	RpcDialTimeout     time.Duration
	MinRefreshInterval time.Duration

	*SecureConfig

	MasterLease        time.Duration
	MasterElectionLock string

	EtcdEndpoints []string
	ParentServers []string
}

type SecureConfig struct {
	EnableTls bool
	CertFile  string
	KeyFile   string
}

func NewServerConfiguration() *ServerConfiguration {
	return &ServerConfiguration{
		Port:      5101,
		DebugPort: 5151,

		ServerRole: "root",

		HostName: os.Hostname(),
		Config:   "/etc/doorman/config.yml",

		RpcDialTimeout:     5 * time.Second,
		MinRefreshInterval: 5 * time.Second,

		SecureConfig: &SecureConfig{
			EnableTls: false,
		},

		MasterLease:        10 * time.Second,
		MasterElectionLock: "",
	}
}

func (sc *ServerConfiguration) InitServerFlags(fs *pflag.FlagSet) {

	fs.IntVar(&sc.Port,"port",sc.Port,"port to bind to")

	// FIXME(ryszard): As of Jan 21, 2016 it's impossible to serve
	// both RPC and HTTP traffic on the same port. This should be
	// fixed by grpc/grpc-go#75. When that happens, remove
	// debugPort.
	fs.IntVar(&sc.DebugPort,"debug_port",sc.DebugPort,"port to bind for HTTP debug info")
	fs.StringVar(&sc.ServerRole,"server_role",sc.ServerRole,"Role of this server in the server tree")

	fs.StringVar(&sc.HostName,"hostname",sc.HostName,"Use this as the hostname (if empty, use whatever the kernel reports")
	fs.StringVar(&sc.Config,"config",sc.Config,"source to load the config from (text protobufs)")

	fs.DurationVar(&sc.RpcDialTimeout,"doorman_rpc_dial_timeout",sc.RpcDialTimeout,"timeout to use for connecting to the doorman server")
	fs.DurationVar(&sc.MinRefreshInterval,"doorman_minimum_refresh_interval",sc.MinRefreshInterval,"minimum refresh interval")

	fs.BoolVar(&sc.EnableTls,"tls",sc.EnableTls,"Connection uses TLS if true, else plain TCP")
	fs.StringVar(&sc.CertFile,"cert_file","","The TLS cert file")
	fs.StringVar(&sc.KeyFile,"key_file","","The TLS key file")

	fs.DurationVar(&sc.MasterLease,"master_lease",sc.MasterLease,"lease in master elections")
	fs.StringVar(&sc.MasterElectionLock,"master_election_lock","","etcd path for the master election or empty for no master election")

	fs.StringSliceVar(&sc.EtcdEndpoints,"etcd_endpoints",sc.EtcdEndpoints,"comma separated list of etcd endpoints")

	fs.StringSliceVar(&sc.ParentServers,"parent_servers",sc.ParentServers,"Addresses of the parent servers which this server connects to")

	flag.Parse()
	if err := flagutil.Populate(fs, "DOORMAN"); err != nil {
		log.Exit(err)
	}


}

// getServerID returns a unique server id, consisting of a host:port id.
func GetServerID(port int, hostname string) string {
	if hostname != "" {
		return fmt.Sprintf("%s:%d", hostname, port)
	}
	hn, err := os.Hostname()

	if err != nil {
		hn = "unknown.localhost"
	}

	return fmt.Sprintf("%s:%d", hn, port)
}
