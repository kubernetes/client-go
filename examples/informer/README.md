# Informer Example

Informers provide a high-level API for creating custom controllers for Kubernetes resources.

This particular example demonstrates:

* How to write an Informer against a core resource type and handle to create/update/delete events

## Running

``` go
# assumes you have a working kubeconfig, not required if operating in-cluster 
go run *.go -kubeconfig=$HOME/.kube/config
```

## Use Cases

* Capturing resource events for logging to external systems (eg. monitor non-Normal Events and publish metrics to a time series database)
* Creating lifecycle controllers for ThirdPartyResources (eg. coordinate create/update/delete of an external datastore represented via a ThirdPartyResource type)

## Recommendations

The design of `cache.NewInformer` lends itself towards writing simple, idempotent operations.
It includes its own reconciliation loop which refreshes resources every resync period in addition to on-the-fly event handlers.
This allows you to focus on writing simple handlers, instead of introducing an external queue or handling complex retry logic yourself as long as you configure a satisfactory resync period for your own needs.

Each resource has a UID field inside its Metadata.
This UID should be used in concert with any fields you feel relevant to provide deterministic resource lookup.

Example:

> If you write a controller that for a ThirdPartyResource that manages the lifecycle of Database objects, you might use `{{.Metadata.Name}}-{{.Metadata.UID}}` as the name of the remote database instance.

Some resources may require more than UID to guarantee uniqueness (for example `v1.Event`). 

Additionally, always:

* check if the external objects exist before attempting to create them
  * or handle "already exists" errors instead of outright `panic()`
* check if the external objects properly match the `.Spec` of your Resource, and reconcile them appropriately
* choose a resync period carefully if interacting with other APIs in your handlers to avoid exhausting rate limits
