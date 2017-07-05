
TODO: This document was neglected and is currently not complete. Working on
fixing this.

# HEAD (changes that will go into the next release)

# v3.0.0

Bug fixes:
* Use OS-specific libs when computing client User-Agent in kubectl, etc. (https://github.com/kubernetes/kubernetes/pull/44423)
* kubectl commands run inside a pod using a kubeconfig file now use the namespace specified in the kubeconfig file, instead of using the pod namespace. If no kubeconfig file is used, or the kubeconfig does not specify a namespace, the pod namespace is still used as a fallback. (https://github.com/kubernetes/kubernetes/pull/44570)
* Restored the ability of kubectl running inside a pod to consume resource files specifying a different namespace than the one the pod is running in. (https://github.com/kubernetes/kubernetes/pull/44862)
* Fix a bug that caused invalid Tolerations to be applied to Pods created by DaemonSets. (https://github.com/kubernetes/kubernetes/pull/45349)
* Fix `Invalid value: "foregroundDeletion"` error when attempting to delete a resource. (https://github.com/kubernetes/kubernetes/pull/46500)

# v3.0.0-beta.0

* Added dependency on k8s.io/apimachinery. The impacts include changing import path of API objects like `ListOptions` from `k8s.io/client-go/pkg/api/v1` to `k8s.io/apimachinery/pkg/apis/meta/v1`.
* Added generated listers (listers/) and informers (informers/)
* Kubernetes API changes:
  * Added client support for:
    * authentication/v1
    * authorization/v1
    * autoscaling/v2alpha1
    * rbac/v1beta1
    * settings/v1alpha1
    * storage/v1
  * Changed client support for:
    * certificates from v1alpha1 to v1beta1
    * policy from v1alpha1 to v1beta1
* CHANGED: pass typed options to dynamic client (https://github.com/kubernetes/kubernetes/pull/41887)

# v2.0.0

* Included bug fixes in k8s.io/kuberentes release-1.5 branch, up to commit 
  bde8578d9675129b7a2aa08f1b825ec6cc0f3420

# v2.0.0-alpha.1

* Removed top-level version folder (e.g., 1.4 and 1.5), switching to maintaining separate versions
  in separate branches.
* Clientset supported multiple versions per API group
* Added ThirdPartyResources example
* Kubernetes API changes
  * Apps API group graduated to v1beta1 
  * Policy API group graduated to v1beta1
  * Added support for batch/v2alpha1/cronjob
  * Renamed PetSet to StatefulSet
  

# v1.5.0

* Included the auth plugin (https://github.com/kubernetes/kubernetes/pull/33334)
* Added timeout field to RESTClient config (https://github.com/kubernetes/kubernetes/pull/33958)
