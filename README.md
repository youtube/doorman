# Doorman

*Got a party? Want your guests to behave? You need a Doorman!*

Doorman is a solution for Global Distributed Client Side Rate Limiting. Clients that talk to a shared resource (such as a database, a gRPC service, a RESTful API, or whatever) can use Doorman to voluntarily limit their use (usually in requests per second) of the resource.

Doorman is written in Go and uses gRPC as its communication protocol. For some high-availability features it needs [distributed lock manager](https://en.wikipedia.org/wiki/Distributed_lock_manager).
We currently support [etcd](https://github.com/coreos/etcd), but it should be relatively simple to make it use [Zookeeper]() instead.

## Getting Started

### Getting Doorman

### Running a single Doorman server

### Running a single level Doorman cluster

### Running a multi-level Doorman installation

## Contributing

See [CONTRIBUTING](CONTRIBUTING.md) for details on submitting patches and the contribution workflow.

## License
Doorman is under the Apache 2.0 license. See the [LICENSE](LICENSE) file for details.

## Note
This is not an official Google product.

