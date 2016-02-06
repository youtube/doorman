# Algorithms

The actual apportioning of capacity is done by executing an Algorithm on every request. The Doorman server architecture is modular so that new algorithms can be added easily without influencing existing clients. The role of the algorithm is to look at all the clients' resource requirements (*wants*) and outstanding leases (*has*), and determining what the capacity is that can be returned for the request (*gets*). Algorithms guarantee that they will not have more outstanding leases than there is available capacity. This means that sometimes a client can not get the capacity it has a "right" to because Doorman needs to reduce other clients' allocations to make that possible. In that case Doorman returns the capacity it can without overshooting the maximum configured capacity, however after one refresh cycle things should have converged to their equilibrium.

***Note***: The Doorman design-doc describes the notion of shortfall. This is a short-term small capacity overshoot that can happen in the fully distributed multi-level server tree.

The Doorman protocol supports the notion of a resource "priority": When asking for capacity a client can indicate a numeric priority with respect to the resource. The Doorman server is mostly agnostic when it comes to priorities, it is up to the algorithms to support it. 

***Note***:  The algorithms currently supported do not take priority into account.

Doorman currently supports the following algorithms:

***Note***: In the examples below *cx* is client *x*, *wx* indicates a wanted capacity of *x*, and *gx* means that he client receives *x* from the algorithm.

None of the current algorithms support priorities or additional named parameters.

## NO_ALGORITHM

This pseudo-algorithm always returns the wanted capacity, making it essentially a no-op. For this algorithm the configured capacity is meaningless.

Example:

``` 
c0 w100  g100
c1 w10   g10
c2 w1000 g1000
```

## STATIC

This algorithm returns the minimum of the desired (wanted) capacity and a static capacity (which is specified in the configuration). This means that if the client asks for less than the configured capacity it will get what it is asking for. If the client asks for more than the configured capacity it will get that capacity.

***Note***: For this algorithm the configured resource capacity is not the maximum capacity of the resource, but the maximum capacity to return on every single request.

Example (for a resource with a maximum capacity of 120):

``` 
c0 w50  g50
c1 w200 g120
c2 w300 g120
```

## PROPORTIONAL_SHARE

The PROPORTIONAL_SHARE algorithm guarantees every client an equal share of the available capacity. Any capacity left on the table by clients asking for less than their equal share is distributed to the clients who are asking for more than their equal share. The top-up that these clients get is proportional to their wanted capacity.

Example (for a resource with a maximum capacity of 120): There are three clients, so their equal share is 40. The third client only requests a capacity of 10, so there is 30 left on the table to distribute between the first and the second client. The surplus need (after giving each client the minimum of their wants or their equal share) is 970. Clients c0 and c1 each get a top up that is proportional to their extra need (960 and 10): 

``` 
c0 w1000 g69.69072165
c1 w50   g40.309278351
c2 w10   g10
```

PROPORTIONAL_SHARE is O(n) to the number of clients.

## FAIR_SHARE

Like PROPORTIONAL\_SHARE this algorithm guarantees every client an equal share of the available capacity, and underused capacity gets distributed to the clients asking for more than their equal share. However unlike PROPORTIONAL\_SHARE this algorithm assigns the available capacity in rounds to successive clients so that as many clients as possible get totally fulfilled. If there are a number of clients that cannot be completely fulfilled the remaining capacity is distributed equally among these clients.

Example (for a resource with a maximum capacity of 120):

``` 
c0 w1000 g60
c1 w50   g50
c2 w10   g10
```

As you can see, compared to PROPORTIONAL_SHARE client c1 gets what it wants and c0, who asks for an excessive 1,000 units only gets the top up of what is available after every one else got what they wanted. In more complicated scenarios the algorithm goes through successive rounds of fulfilling clients and distributing the surplus over the clients that have not yet gotten what they wanted. Because of this FAIR_SHARE is O(n^2) to the number of clients, which might suggest using PROPORTIONAL_SHARE for scenarios with a large number of clients.