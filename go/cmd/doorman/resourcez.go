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

package main

// Implements /resourcez, a page which gives detail information about
// outstanding leases.

import (
	"html/template"
	"net/http"
	"sync"

	log "github.com/golang/glog"

	"doorman/go/server/doorman"
)

var (
	// The compiled template for generating the HTML for the page.
	tmpl *template.Template

	// A slice of Doorman servers which have registered with us.
	servers []*doorman.Server

	// A mutex which protects the global data in this module.
	mu sync.RWMutex
)

// init gets called to initialise the elements in this file.
func init() {
	// Adds the handler for /resourcez.
	http.HandleFunc("/debug/resources", resourcezHandler)

	// Compiles the HTML template.
	tmpl = template.Must(template.New("resourcez").Parse(resourcezHTML))

	// Makes the slice that holds the servers for which we need to provide information.
	servers = make([]*doorman.Server, 0, 5)
}

// AddServer adds a Doorman server to the list of servers that we provide information for.
func AddServer(dm *doorman.Server) {
	mu.Lock()
	defer mu.Unlock()

	servers = append(servers, dm)
}

// The template we use to generate HTML for /resourcez.
const resourcezHTML = `
<!DOCTYPE html>
<html>
  <head>
    <title>Doorman resource information</title>
  </head>
  <body bgcolor="#ffffff">
    <div style="margin-left: 20px">
      {{if .Resource}}
	{{range .ResourceLeaseStatus}}
	  <table>
	    <tr><td>Resource:</td><td>{{.ID}}</td></tr>
	    <tr><td>Sum of has:</td><td>{{.SumHas}}</td></tr>
	    <tr><td>Sum of wants:</td><td>{{.SumWants}}</td></tr>
          </table>
	  <p/>
	  <table border="1">
	    <thead>
	      <tr>
	        <td>Client ID</td>
		<td>Lease Expiration</td>
		<td>Refresh Interval</td>
		<td>Has</td>
		<td>Wants</td>
	      </tr>
	    </thead>
	    {{range .Leases}}
	      <tr>
		<td>{{.ClientID}}</td>
		<td>{{.Lease.Expiry}}</td>
		<td>{{.Lease.RefreshInterval}}</td>
		<td>{{.Lease.Has}}</td>
		<td>{{.Lease.Wants}}</td>
	      </tr>
	    {{end}}
	  </table>
	{{end}}
      {{end}}
      <hr/>
      {{range .ServerStatus}}
        {{with .Resources}}
          <p/>
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
                <td><a href="?resource={{.ID}}">{{.ID}}</a></td>
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
          No resources in this server's store.
        {{end}}
      {{end}}
    </div>
  </body>
</html>
`

// This type contains the information that is passed to the templating
// engine and which gets inserted into the template.
type resourcezData struct {
	Resource            string
	ServerStatus        []doorman.ServerStatus
	ResourceLeaseStatus []doorman.ResourceLeaseStatus
}

// resourcezHandler is the handler function that gets called for a request
// to /resourcez.
func resourcezHandler(w http.ResponseWriter, r *http.Request) {
	// Locks the resourcez global data for reads.
	mu.RLock()
	defer mu.RUnlock()

	// Creates the data structure for the template.
	data := resourcezData{
		Resource:            r.FormValue("resource"),
		ServerStatus:        make([]doorman.ServerStatus, 0, len(servers)),
		ResourceLeaseStatus: make([]doorman.ResourceLeaseStatus, 0, len(servers)),
	}

	// Goes through all the servers and fills in their information in the data object.
	for _, srv := range servers {
		data.ServerStatus = append(data.ServerStatus, srv.Status())

		if data.Resource != "" {
			data.ResourceLeaseStatus = append(data.ResourceLeaseStatus, srv.ResourceLeaseStatus(data.Resource))
		}
	}

	// Executes the template and sends the output directly to the browser.
	if err := tmpl.Execute(w, data); err != nil {
		log.Errorf("template execution error: %v", err)
	}
}
