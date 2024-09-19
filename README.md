# source-watcher

[![test](https://github.com/fluxcd/source-watcher/workflows/test/badge.svg)](https://github.com/fluxcd/source-watcher/actions)

Example consumer of the GitOps Toolkit Source APIs.

![Source Controller Overview](https://raw.githubusercontent.com/fluxcd/website/main/static/img/source-controller.png)

## Guides

* [Watching for source changes](https://fluxcd.io/flux/gitops-toolkit/source-watcher/)

## Radius-source-controller

How to run
```
kind create cluster --name dev
flux check --pre
flux install \
--namespace=flux-system \
--network-policy=false \
--components=source-controller

in one terminal:
kubectl -n flux-system port-forward svc/source-controller 8181:80

in another:
go to radius-flux-controller directory
make
export SOURCE_CONTROLLER_LOCALHOST=localhost:8181
make run

in another another terminal:
flux create source git test --url=https://github.com/willdavsmith/radius-flux-app --branch main

in your second terminal you should see the source controller log that it has detected the new source

have fun!
```