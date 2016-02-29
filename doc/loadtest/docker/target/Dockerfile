FROM golang:1.5
RUN go get github.com/golang/glog github.com/prometheus/client_golang/prometheus \
	golang.org/x/net/context google.golang.org/grpc \
	google.golang.org/grpc/examples/helloworld/helloworld
RUN mkdir -p ${GOPATH}/src/github.com/youtube/doorman/doc/loadtest/docker/target
ADD ./target.go ${GOPATH}/src/github.com/youtube/doorman/doc/loadtest/docker/target
RUN go install github.com/youtube/doorman/doc/loadtest/docker/target

