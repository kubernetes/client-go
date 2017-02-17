# Installing client-go

## For the casual user

If you want to write a simple script, don't care about a reproducible client
library install, don't mind getting head (which may be less stable than a
particular release) and don't share any dependencies with client-go, then
simply:

```sh
$ go get k8s.io/client-go/...

# Test install
$ go build k8s.io/client-go/examples/...
```

This will install `k8s.io/client-go` in your `$GOPATH`. `k8s.io/client-go` includes its own
dependencies in its `k8s.io/client-go/vendor` path. `go get` will not flatten
those dependencies into your `$GOPATH`, which means they will be distinct from
any dependencies you may already have there. This will be problematic if you
happen to already have a copy of, say, glog, and in that case you'll need to
look down at the next section.

Note: the official go policy is that libraries should not vendor their
dependencies. This is unworkable for us, since our dependencies change and HEAD
on every dependency has not necessarily been tested with client-go. In fact,
HEAD from all dependencies may not even compile with client-go!

## Dependency management for the serious (or reluctant) user

Reasons why you might need to use a dependency management system:
* You use a dependency that client-go also uses, and don't want two copies of
  the dependency compiled into your application. For some dependencies with
  singletons or global inits (e.g. `glog`) this wouldn't even compile...
* You want to lock in a particular version (so you don't have to change your
  code every time we change a public interface).
* You want your install to be reproducible. For example, for your CI system or
  for new team members.

There are three tools you could in theory use for this. Instructions
for each follows.

### Dep

[dep](https://github.com/golang/dep) is an up-and-coming dependency management tool,
which has the goal of being accepted as part of the standard go toolchain. It
is currently pre-alpha. However, it comes the closest to working easily out of
the box.

```sh
$ go get github.com/golang/dep
$ go install github.com/golang/dep/cmd/dep

# Make sure you have a go file in your directory which imports k8s.io/client-go
# first--I suggest copying one of the examples.
$ dep ensure k8s.io/client-go@^2.0.0
```

This will set up a /vendor directory in your current directory, add `k8s.io/client-go`
to it, and flatten all of `k8s.io/client-go`'s dependencies into that vendor directory,
so that your code and `client-go` will both get the same copy of each
dependency.

After installing like this, you could either use dep for your other
dependencies, or copy everything in the `vendor` directory into your
`$GOPATH/src` directory and proceed as if you had done a fancy `go get` that
flattened dependencies sanely.

One thing to note about dep is that it will omit dependencies that aren't
actually used, and some dependencies of `client-go` are used only if you import
one of the plugins (for example, the auth plugins). So you may need to run `dep
ensure` again if you start importing a plugin that you weren't using before.

### Godep

[godep](https://github.com/tools/godep) is an older dependency management tool, which is
used by the main Kubernetes repo and `client-go` to manage dependencies.

Before proceeding with the below instructions, you should ensure that your
$GOPATH is empty except for containing your own package and its dependencies,
and you have a copy of godep somewhere in your $PATH.

To install `client-go` and place its dependencies in your `$GOPATH`:

```sh
go get k8s.io/client-go
cd $GOPATH/src/k8s.io/client-go
git checkout v2.0.0
# cd 1.5 # only necessary with 1.5 and 1.4 clients.
godep restore ./...
```

At this point, `client-go`'s dependencies have been placed in your $GOPATH, but
if you were to build, `client-go` would still see its own copy of its
dependencies in its `vendor` directory. You have two options at this point.

If you would like to keep dependencies in your own project's vendor directory,
then you can continue like this:

```sh
cd $GOPATH/src/<my-pkg>
godep save ./...
```

Alternatively, if you want to build using the dependencies in your `$GOPATH`,
then `rm -rf vendor/` to remove `client-go`'s copy of its dependencies.

### Glide

[glide](https://github.com/Masterminds/glide) is another popular dependency
management tool for go. Glide will manage your /vendor directory, but unlike
godep, will not modify your $GOPATH (there's no equivalent of `godep restore` or
`godep save`). To get the `client-go` dependency list into glide's format, you
can run this series of commands:

```sh
go get k8s.io/client-go
cd $GOPATH/src/k8s.io/client-go
git checkout v2.0.0
# cd 1.5 # only necessary with 1.5 and 1.4 clients.
glide init # Will import from godep format and produce glide.yaml
```

Next there are two possibilities: you could copy the `glide.yaml` file into your
own project (change the `project: ` line at the top!), or, if you already have a
`glide.yaml` file, you'll need to merge this `glide.yaml` with it.

At this point, your `glide.yaml` file has all the dependencies of the client
library, but doesn't list the client itself. Running `glide install
--strip-vendor k8s.io/client-go#^2.0.0` will add `client-go` to the dependency
list, and all the dependencies should resolve correctly.

Note that simply running `glide install k8s.io/client-go#^2.0.0` without first
getting the dependencies sorted out doesn't seem to trigger glide's
import-from-godep code, and as a consequence it doesn't resolve the dependencies
correctly.
