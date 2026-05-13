# k8s-go

[![Docker Image](https://img.shields.io/docker/v/yinebeb/k8s-go?label=docker&logo=docker)](https://hub.docker.com/r/yinebeb/k8s-go)
[![Docker Pulls](https://img.shields.io/docker/pulls/yinebeb/k8s-go)](https://hub.docker.com/r/yinebeb/k8s-go)
[![Image Size](https://img.shields.io/docker/image-size/yinebeb/k8s-go/0.2)](https://hub.docker.com/r/yinebeb/k8s-go)

Go HTTP server packaged for Kubernetes. Handles `SIGTERM` with a readiness drain, exposes split `/livez` and `/readyz` probes, logs JSON via `slog`, and checks a Bearer token from a `Secret` on `/hello`. Runtime image is Alpine + the binary (~15 MB).

## Endpoints

| Path | Auth | Purpose |
|------|------|---------|
| `/hello` | `Authorization: Bearer $API_TOKEN` | Demo handler |
| `/livez` | none | Liveness probe |
| `/readyz` | none | Readiness probe (flips during startup + shutdown drain) |

## Environment

| Var | Default | Notes |
|-----|---------|-------|
| `PORT` | `8080` | App listen port |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR` (case-insensitive) |
| `API_TOKEN` | _(empty)_ | Bearer token for `/hello`. Empty = all requests rejected. |

## Run locally (without Kubernetes)

```bash
go build -ldflags "-X main.version=$(git rev-parse --short HEAD)" -o main .
API_TOKEN=devtoken LOG_LEVEL=DEBUG ./main
```

```bash
curl -H "Authorization: Bearer devtoken" http://localhost:8080/hello
curl http://localhost:8080/livez
curl http://localhost:8080/readyz
```

## Build Docker image

```bash
docker build -t yinebeb/k8s-go:0.2 .
docker push yinebeb/k8s-go:0.2   # only if your cluster pulls from a registry
```

If your cluster runs in a VM/daemon separate from the host (Minikube, kind, k3d), you may need to load the image into the cluster's runtime instead of pushing to a registry — check your tool's docs.

## Deploy

Prerequisite: a Kubernetes cluster and `kubectl` configured for it (`kubectl cluster-info` should succeed).

```bash
# 1. Create the Secret, replace API_TOKEN value
cp k8s-secret.example.yaml k8s-secret.yaml
kubectl apply -f k8s-secret.yaml

# Or imperative — no file at all:
# kubectl create secret generic k8s-go-secrets --from-literal=API_TOKEN=$(openssl rand -hex 32)

# 2. Apply ConfigMap + Deployment + Service
kubectl apply -f k8s.yaml

# 3. Block until all replicas are Ready
kubectl rollout status deployment/k8s-go-deployment
```

## Test the running service

```bash
kubectl port-forward svc/k8s-go-service 8080:80
```

In another terminal:

```bash
TOKEN=$(kubectl get secret k8s-go-secrets -o jsonpath='{.data.API_TOKEN}' | base64 -d)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/hello
```

## Walkthrough

Step-by-step write-up of how this repo is put together: [yinebebt.com/post/k8s-go-app/](https://yinebebt.com/post/k8s-go-app/)

