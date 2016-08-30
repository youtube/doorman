package main

import (
	"expvar"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"doorman/go/client/doorman"
	"doorman/go/ratelimiter"
	log "github.com/golang/glog"
	"github.com/pborman/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"

	pb "google.golang.org/grpc/examples/helloworld/helloworld"
)

var (
	port     = flag.Int("port", 0, "port to bind to")
	addr     = flag.String("addr", "", "address of the Doorman server")
	resource = flag.String("resource", "", "name of requested resource")

	count           = flag.Int("count", 1, "how many clients to simulate")
	initialCapacity = flag.Float64("initial_capacity", 0, "initial capacity to request")
	maxCapacity     = flag.Float64("max_capacity", 0, "maximum capacity to request")
	minCapacity     = flag.Float64("min_capacity", 0, "minimum capacity to request")
	increaseChance  = flag.Float64("increase_chance", 0, "chance the requested capacity will increase")
	decreaseChance  = flag.Float64("decrease_chance", 0, "chance the requested capacity will decrease")
	step            = flag.Float64("step", 0, "amount of capacity change")
	interval        = flag.Duration("interval", 1*time.Minute, "how often attempt changing requested capacity")

	target  = flag.String("target", "", "target greeter service")
	workers = flag.Int("workers", 4, "number of worker goroutines")
)

var (
	received  = expvar.NewMap("received")
	requested = expvar.NewMap("requested")
	askErrors = expvar.NewInt("ask-errors")
)

func manipulateCapacity(res doorman.Resource, current float64, id string) {
	clientRequested := new(expvar.Float)
	for range time.Tick(*interval) {
		r := rand.Float64()
		log.V(2).Infof("r=%v decreaseChance=%v increaseChance=%v", r, *decreaseChance, *increaseChance)
		switch {
		case r < *decreaseChance:
			current -= *step
			log.Infof("client %v will request less: %v.", id, current)
		case r < *decreaseChance+*increaseChance:
			log.Infof("client %v will request more: %v.", id, current)
			current += *step
		default:
			log.V(2).Infof("client %v not changing requested capacity", id)
			continue
		}
		if current > *maxCapacity {
			current = *maxCapacity
		}
		if current < *minCapacity {
			current = *minCapacity
		}
		log.Infof("client %v will request %v", id, current)
		if err := res.Ask(current); err != nil {
			log.Errorf("res.Ask(%v): %v", current, err)
			askErrors.Add(1)
			continue
		}
		clientRequested.Set(current)
		requested.Set(id, clientRequested)
	}
}

func main() {
	flag.Parse()
	log.Infof("Simulating %v clients.", *count)
	for i := 0; i < *count; i++ {
		id := uuid.New()
		log.Infof("client %v with id %v", i, id)

		client, err := doorman.NewWithID(*addr, id, doorman.DialOpts(grpc.WithInsecure()))
		if err != nil {
			log.Exit(err)
		}
		defer client.Close()

		res, err := client.Resource(*resource, *initialCapacity)
		if err != nil {
			log.Exit(err)
		}

		go manipulateCapacity(res, *initialCapacity, id)

		conn, err := grpc.Dial(*target, grpc.WithInsecure())
		if err != nil {
			log.Exitf("did not connect: %v", err)
		}
		defer conn.Close()

		c := pb.NewGreeterClient(conn)
		rl := ratelimiter.NewQPS(res)

		for i := 0; i < *workers; i++ {
			go func() {
				ctx := context.Background()
				for {
					if err := rl.Wait(ctx); err != nil {
						log.Exitf("rl.Wait: %v", err)
					}

					ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
					if _, err := c.SayHello(ctx, &pb.HelloRequest{Name: *resource}); err != nil {
						log.Error(err)
					}
					cancel()
				}
			}()
		}
	}
	http.Handle("/metrics", prometheus.Handler())
	http.ListenAndServe(fmt.Sprintf(":%v", *port), nil)

}
