# grapevine-microservice-poc
Proof of concept using grapevine to build a microservice solution that follows a event driven architecture.

It includes a service, a gateway and a taskboard.

## Service
The implemented service replies "Hello" with the supplied name.

To launch the service open a terminal in the __helloservice__ folder and run
```
go run helloservice.go
```

This will start a grapevine peer listening in port 5000.

To have more info, create a another service, a bad one, by running:
```
go run helloservice.go -gv 5001 -bad 100
```
This will start a grapevine peer listening in port 5001 and will have 33% timeout
and 66% of a long reply (2s)

## Gateway
The gateway is responsible to interface between the http client an the grapevine services.
On top of the resilience provided by the grapevine load balancers, all the call go through a circuit breaker.

Additionally we also the collection and publishing of metrics.
This gateway will periodically (10s) publish the collected metrics from the load balancer and the circuit breaker.

To launch the gateway open a terminal in the __gateway__ folder and run
```
go run gateway.go
```

To test the service open a browser (or whatever you like) and run, for example, to **http://localhost:8080/api/Quintans**

## Dashboard
The dashboard listens the published metrics from the gateway and agregates them.
It also makes available the endpoint **/stats** for server sent events to be consumed by any compatible browser,
with a refresh rate of 10s.

It also provides a web frontend to presents the information in the following layout:

---
### Circuit Breaker

Global report for each service, from the perspective of a Circuit Breaker (CB).

Service Name  | CB State  |  ok / all | Success
--------------|:---------:|:---------:|:-------------------:
hello         |  Closed   |    2/3    | (785457/85) 100%
hi            | **Open**  |    0/2    |


<br>

### Load Balacing

Report for each service from the perspective of the Load Balancer (LB).
Every LB will send its statistics.

Service Name  | Quarantine  |   Success           |   Location
:-------------|:-----------:|:-------------------:|----------------
hello         |   OK        |   (15990/143) 99%   | 127.0.0.1:5001
hello         |   OK        |                     | 127.0.0.1:5002
hello         |   **NOK**   |         0%          | 127.0.0.1:5003
hi            |   **NOK**   |                     | 127.0.0.1:5011
hi            |   **NOK**   |                     | 127.0.0.1:5012

---

To launch the gateway open a terminal in the __dashboard__ folder and run
```
go run dashboard.go
```

and open browser in [http://localhost:8070/](http://localhost:8070/)
