# scoped-cache-poc
## Why do we need a `ScopedCache`?

**Problem Statement**: As an operator author I want to develop an operator that can handle changing permissions so that cluster admins can use [Role Based Access Control (RBAC)](https://kubernetes.io/docs/reference/access-authn-authz/rbac/) to scope the permissions given to my operator.

In order to grant cluster admins the ability to scope the permissions given to operators, we need to provide operator authors an easy way to handle dynamically changing permissions. 

Operator authors that utilize Operator SDK for scaffolding their projects will by default leverage [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime).

`controller-runtime` utilizes a cache backed by Kubernetes Informers ([more info](https://aly.arriqaaq.com/kubernetes-informers/)) that makes subscribing to events and reading Kubernetes objects more efficient. The current limitation with existing `controller-runtime` cache implementations is that they are static and require a certain level of permissions granted to the controller at all times. If the controller does not have these permissions at any point it will crash.

## How does the `ScopedCache` PoC solve the problem?

The [scoped-cache-poc](https://github.com/everettraven/scoped-cache-poc) is a Go library that has a cache implementation (`ScopedCache`) that satisfies the `controller-runtime` `cache.Cache` interface. This `ScopedCache` provides operator authors a dynamic caching layer that can be used to handle dynamically changing permissions.

As of now, this library is ONLY the caching layer. It is up to the operator author to implement the logic to update the cache and handle the permission changes appropriately.

### How does it work?
The idea behind the `ScopedCache` is to create informers for resources as they are needed. This means:
- Informers are only created when a CR is reconciled
- Informers are only created for resources that are related to the CR
- Informers only live as long as the corresponding CR is around. If the CR is deleted the corresponding informers should be stopped.

One assumption is that Operators will always need to watch for CRs they reconcile at the cluster level.

In order to accomplish this, the `ScopedCache` is comprised of a couple different caches:
- A `cache.Cache` that is used for everything that should be cluster scoped
- A `NamespacedResourceCache` that is a mapping of `Namespace` ---> `ResourceCache`
    - A `ResourceCache` is a mapping of `types.UID` ---> `cache.Cache`
        - The `types.UID` is the unique identifier of a given Kubernetes Resource

To properly use the `ScopedCache`, when reconciling a CR you would need to create the corresponding watches. The workflow for creating these watches would look like:
- Create a new `cache.Cache` with options that scope the cache's view to only objects created or referenced when reconciling the given CR
- Add the `cache.Cache` above to the `ScopedCache`:
    - Internally, the `ScopedCache` will create the correct mapping of `Namespace` ---> `Resource` ---> `cache.Cache` for a given CR and `cache.Cache`
- Get/Create necessary informers for the CR from the `ScopedCache`
- Configure informers to handle changed permissions
- Start the `cache.Cache` that corresponds to the CR being reconciled
- Use `controller-runtime` utility functions to create watches with the informers from the `ScopedCache`

Due to the process of adding caches for a CR to the `ScopedCache` being a deliberate process - if there are any requests made to the `ScopedCache` without any `ResourceCaches` having been created, it is assumed that it is intended to use the cluster scoped `cache.Cache`.

## Demonstration

For a demonstration of using this library, see the GitHub Repository [everettraven/scoped-operator-poc](https://github.com/everettraven/scoped-operator-poc/tree/test/scoped-cache) - specifically the branch `test/scoped-cache`.

The demonstration can be found in the [Demonstration section of the README](https://github.com/everettraven/scoped-operator-poc/tree/test/scoped-cache#demonstration)

## Current Limitations
There are currently a few limitations with this approach:
- **Limitation**: `controller-runtime` is not designed in such a way that easily enables this functionality. There are workarounds that needed to be created to be able to properly implement this logic.
    **Potential Solution**: Work with `controller-runtime` to implement changes that limits workarounds and improves the user experience when it comes to creating scopeable operators.
- **Limitation**: Only a caching layer. It is up to the Operator Authors to implement more complex logic to properly update the cache and handle changing permissions.
    **Potential Solution**: Provide a higher level library that provides Operator Authors a better user experience when developing operators that should handle changing permissions.
- **Limitation**: Currently when using the scoped-cache-poc library watches are recreated multiple times for the same resources due to the way that informers are created. Informers returned by the `ScopedCache` is currently an aggregate of all informers available to an operator within the `NamespacedResourceCache`.
    **Potential Solution**: Provide a way to only get informers from a specific `ResourceCache`, enabling watches to be created only for the informers from a specific `ResourceCache`.

## Understanding Check
- What problem does the `scoped-cache-poc` and `ScopedCache` solve?
- Why can't we use existing `controller-runtime` cache implementations?
- What is `scoped-cache-poc`?
