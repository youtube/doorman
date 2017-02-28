// package configuration defines different sources of text-based
// configuration.
package configuration

import (
	"io/ioutil"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/coreos/etcd/client"
	log "github.com/golang/glog"
	"doorman/go/timeutil"
	"golang.org/x/net/context"
)

// Source is a source for configuration. Calling it will block until a
// new version of the config is available.
type Source func(context.Context) (data []byte, err error)

type pair struct {
	data []byte
	err  error
}

// LocalFiles is a configuration stored in a file in the local
// filesystem. The file will be reloaded if the process receives a
// SIGHUP.
func LocalFile(path string) Source {
	updates := make(chan pair, 1)

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)
	c <- syscall.SIGHUP
	go func() {
		for range c {
			log.Infof("config: loading configuration from %v.", path)
			data, err := ioutil.ReadFile(path)
			updates <- pair{data: data, err: err}
		}
	}()
	return func(ctx context.Context) (data []byte, err error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case p := <-updates:
			return p.data, p.err
		}
	}
}

// Etcd is a configuration stored in etcd. It will be reloaded as soon
// as it changes.
func Etcd(path string, endpoints []string) Source {

	updates := make(chan pair, 1)
	req := make(chan context.Context)

	go func() {
		var c client.Client
		for i := 0; true; i++ {
			var err error
			c, err = client.New(client.Config{Endpoints: endpoints})
			if err != nil {
				log.Errorf("configuration: cannot connect to etcd: %v", err)
				updates <- pair{err: err}
				time.Sleep(timeutil.Backoff(1*time.Second, 60*time.Second, i))
				continue
			}
			break
		}
		log.V(2).Infof("configuration: connected to etcd")
		kapi := client.NewKeysAPI(c)

		r, err := kapi.Get(<-req, path, nil)
		if err != nil {
			updates <- pair{err: err}
		} else {
			updates <- pair{data: []byte(r.Node.Value)}
		}

		w := kapi.Watcher(path, nil)

		for i := 0; true; i++ {
			ctx := <-req
			r, err := w.Next(ctx)
			if err != nil {
				updates <- pair{err: err}
				time.Sleep(timeutil.Backoff(1*time.Second, 60*time.Second, i))
				continue
			}
			updates <- pair{data: []byte(r.Node.Value)}
		}

	}()

	return func(ctx context.Context) (data []byte, err error) {
		req <- ctx
		p := <-updates
		return p.data, p.err
	}

}

// ParseSource parses text and returns the kind of configuration
// source and the desired path.
func ParseSource(text string) (kind string, path string) {
	parts := strings.SplitN(text, ":", 2)
	if len(parts) == 1 {
		return "file", text
	}
	switch parts[0] {
	case "etcd":
		return "etcd", parts[1]
	case "file":
		return "file", parts[1]
	}

	return "file", text
}
