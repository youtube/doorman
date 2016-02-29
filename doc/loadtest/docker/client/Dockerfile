FROM golang:1.5
RUN go get  "github.com/golang/glog" "github.com/pborman/uuid" "github.com/youtube/doorman/go/client/doorman" \
	golang.org/x/net/context google.golang.org/grpc google.golang.org/grpc/examples/helloworld/helloworld \
	github.com/youtube/doorman/go/ratelimiter

RUN mkdir -p $GOPATH/src/github.com/youtube/doorman/doc/loadtest/docker/client/doorman_client
ADD doorman_client.go $GOPATH/src/github.com/youtube/doorman/doc/loadtest/docker/client
RUN cd $GOPATH/src/github.com/youtube/doorman/doc/loadtest/docker/client && go install

