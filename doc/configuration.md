# Configuration

Doorman works off of a global configuration that defines resource templates with all the information needed to apportion and distribute capacity to clients.

Proto: doorman.proto (message 'ResourceRepository')

The configuration consists of a repeated group of resource templates. Each resource template configures a group of resources that are identical except for their actual resource identifier. Shell style globbing is used to determine which resource template applies to a request for capacity.

Every Doorman server contains an overview of the current configuration on its /debug/status page.

## Parameters

### identifier_glob (string)

A shell glob that is used to figure out which template applies to which resource identifier. A '*' catch-all entry in the configuration applies to every resource for which a more specific template cannot be found. When searching through the configuration the Doorman server first tries to find a resource template with the exact name as the resource (without doing globbing). If that search does not yield a template a second pass is done with a globbing match.

### capacity (double)

The maximum global capacity of the resource. This is the maximum that Doorman will hand out. Doorman is agnostic about units, but typically this number can be thought of as the maximum qps the resource can take (a rate) or the maximum in-flight transactions that it can handle (a gauge).

### safe_capacity (double)

The "safe capacity" for a resource is the capacity the Doorman client will use in case the Doorman server is unavailable. If you set this to -1 Doorman rate limiting will be disabled in case Doorman is down. If you set it to 0 clients will stop using the resource altogether. If you set it to a positive number each client will act as if it had received that capacity assignment from the Doorman server. If you do not specify the safe_capacity the Doorman server calculates a dynamic safe capacity that equals the maximum configured capacity divided by the number of clients that the Doorman server knows about. 

### description (string)

A free text field to give some more information about the resource.

### Algorithm.kind (enum)

Which of Doorman's available algorithms should be used to apportion capacity among clients. Please see the page on Algorithms for a description of the algorithms supported. 

### Algorithm.lease_length (int64)

The length (in seconds) of the capacity lease granted by this algorithm. Please see the page on "How it Works" for more information on leases. A typical value is 300 seconds.

### Algorithm.refresh_interval (int64)

The interval (in seconds) after which the client is requested to contact the Doorman server for a capacity refresh. Please see te page on "How it Works" for more information on refresh intervals. A typical value is 5 seconds. 

### Algorithm.learning_mode_duration (int64)

The duration of the Doorman server's learning mode for this resource. Please see the page on "How it Works" for information about the learning mode. Unless you totes know what you are doing you should probably leave this field unspecified.

### Algorithm.NamedParameter.name and Algorithm.NamedParameter.value (string)

The algorithm's named parameter is a way to specify additional parameters for the algorithm. Please see the page on Algorithms to find out which algorithms support which named parameters. 