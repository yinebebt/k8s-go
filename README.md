# k8s-go

[![Docker Image](https://img.shields.io/docker/v/yinebeb/k8s-go?label=docker&logo=docker)](https://hub.docker.com/r/yinebeb/k8s-go)

Go HTTP server packaged for Kubernetes. Handles `SIGTERM` with a readiness drain, exposes split `/livez` and `/readyz` probes, logs JSON via `slog`, and checks a Bearer token from a `Secret` on `/hello`.

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

## What is a Kubernetes cluster?

A cluster is: a **control plane** (api-server, scheduler, controller-manager, etcd) plus one or more **nodes** running `kubelet` + a container runtime (containerd) + `kube-proxy`. A **CNI plugin** wires pod networking. That is it. Nothing in this list is a load balancer, an ingress, or a DNS for external traffic — those are add-ons.

### Why not just run the k8s binaries directly?

The Kubernetes release ships ~6 standalone Go binaries (`kube-apiserver`, `kube-controller-manager`, `kube-scheduler`, `kube-proxy`, `kubelet`, `kubectl`) plus `etcd`. Nothing prevents you from `wget`-ing them and running them — but to get a *working* cluster you also have to:

- generate a CA and ~10 TLS certs / kubeconfigs (api-server serving cert, kubelet client cert, controller-manager kubeconfig, scheduler kubeconfig, service-account signing key, etcd peer + client certs, front-proxy CA…),
- run `etcd` with proper peer/client TLS,
- start `kube-apiserver` wired to that `etcd`, with the right `--service-cluster-ip-range`, `--service-account-*` flags, admission plugins, audit policy,
- start `kube-controller-manager` and `kube-scheduler` pointed at the api-server,
- on every node: enable `br_netfilter`, `ip_forward`, swap off, install a container runtime + CRI socket, start `kubelet` with the right cgroup driver and `--kubeconfig`,
- install a CNI plugin (Calico, Cilium, kindnet…) and write `/etc/cni/net.d/*.conf`,
- start `kube-proxy` (iptables or IPVS),
- bootstrap-token join the workers.

`kubeadm` automates that. **`kind` and `minikube` go one step further** by bundling the binaries + a CNI + a container runtime + `kubeadm` itself into a node image, then provisioning the node hosts for you — Docker containers for kind, a VM (or Docker container) for minikube. (`k3d` is similar but uses the `k3s` distribution instead of kubeadm.) You give up visibility into the bootstrap; you gain a disposable cluster in ~30s without setting host sysctls, installing systemd units, or wiring TLS by hand. `kind` is the thinnest of these wrappers — `kubeadm` is still doing the real work, and you can shell into the node to watch it.

### Local cluster with `kind`

We use [`kind`](https://kind.sigs.k8s.io/) (Kubernetes-in-Docker). Each node is a plain Docker container running `kubelet` + `containerd`. No VM, no tunnel daemon, no host-network shim. You can `docker ps` and see the cluster.

`kind` is not zero-abstraction — it ships a prebuilt **`kindest/node`** image (tag tied to the kind release, e.g. `kindest/node:v1.35.0`) that bundles the Kubernetes binaries (`kube-apiserver`, `kube-controller-manager`, `kube-scheduler`, `etcd`, `kubelet`, `kube-proxy`, `kubeadm`, `containerd`) and the kindnet CNI into one image. `kind create cluster` then runs `kubeadm init` inside the control-plane container (and `kubeadm join` inside each worker if you ask for more nodes). Same components a real cluster runs — just colocated in one image so you do not install them separately.

Create:

```bash
kind create cluster --name k8s-go
kubectl cluster-info --context kind-k8s-go
docker ps                       # node is the container k8s-go-control-plane; only the api-server port is published
docker network inspect kind     # kind always uses a docker network called "kind", regardless of cluster name
```

Inspect what `kubeadm` set up inside the node:

```bash
docker exec -it k8s-go-control-plane crictl ps                  # api-server, etcd, controller-manager, scheduler, kube-proxy as containers
docker exec -it k8s-go-control-plane ls /etc/kubernetes/manifests # static pod manifests kubelet watches
```

Load the locally built image into the cluster (no registry push needed). **`kind load` defaults to a cluster named `kind`** — if your cluster has any other name you must pass `--name` or you get `ERROR: no nodes found for cluster "kind"`:

```bash
kind load docker-image yinebeb/k8s-go:0.2 --name k8s-go
```

When done:

```bash
kind delete cluster --name k8s-go
```

### Why not Minikube / Docker Desktop?

Both work, but both hide the part we want to see:

- **Minikube** ships `minikube tunnel`, a privileged process that adds a route on the host so `LoadBalancer` Services get a reachable IP. The "LB" is the tunnel, not a Kubernetes controller.
- **Docker Desktop**'s built-in Kubernetes registers a load-balancer controller that assigns `localhost` as the `EXTERNAL-IP` of every `LoadBalancer` Service. That is host-side glue, not part of the cluster.

Both are fine for app development. They are bad for learning *why* `LoadBalancer` works, because they make `<pending>` never happen. `kind` ships no such controller, so the failure mode is visible and the fix is explicit.

## Deploy

Prerequisite: a cluster reachable via `kubectl cluster-info`.

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

## Accessing the Service

`k8s.yaml` ships **two** Service objects against the same pods so you can compare:

```bash
kubectl get svc -l app=k8s-go
# NAME              TYPE           CLUSTER-IP     EXTERNAL-IP   PORT(S)
# k8s-go-service    LoadBalancer   10.96.x.x      <pending>     80:3xxxx/TCP
# k8s-go-nodeport   NodePort       10.96.y.y      <none>        80:30080/TCP
```

### The LoadBalancer reality

`EXTERNAL-IP` on `k8s-go-service` stays `<pending>` forever. This is **correct, not broken**. Vanilla Kubernetes has no load-balancer implementation. In a cloud cluster, the cloud-controller-manager watches `LoadBalancer` Services and provisions a real LB (AWS NLB, GCP forwarding rule, etc.). On bare metal or `kind`, nothing watches. No controller, no IP.

### NodePort works today — but only inside the kind network

`k8s-go-nodeport` opens port `30080` on every node. NodePort is a port on the node's host network: anything that can route to the node can reach it. The kind node is a Docker container on the `kind` docker network, so:

```bash
# from inside the node container
docker exec k8s-go-control-plane curl -sS http://localhost:30080/livez

# from a throwaway container joined to the same docker network
NODE_IP=$(docker inspect -f '{{(index .NetworkSettings.Networks "kind").IPAddress}}' k8s-go-control-plane)
docker run --rm --network kind curlimages/curl -sS http://$NODE_IP:30080/livez
```

Both return `HTTP 200`.

`curl localhost:30080` from a macOS/Windows shell **fails** — Docker Desktop runs Docker inside a VM and does not route the host into the `kind` docker network, so the node IP `172.x.x.x` is not reachable. On Linux with Docker's bridge driver, the node IP *is* routable and host curl works.

Three ways to reach the NodePort from a macOS/Windows host when you need to:

- `kind create cluster --config <file>` with `extraPortMappings` — publishes node port `30080` to host port `30080` (literally `docker run -p`).
- `kubectl port-forward svc/k8s-go-nodeport 30080:80` — tunnels via the api-server to a backing pod. Skips the NodePort path itself.
- Install MetalLB — assigns `k8s-go-service` a real IP from the kind subnet (same routability constraint as the node IP).

### NodePort vs LoadBalancer — when each makes sense

| | NodePort | LoadBalancer |
|---|---|---|
| **Allocates** | A port (30000-32767) on every node | An external IP + port from the LB provider |
| **Needs** | Nothing | Cloud controller, MetalLB, or equivalent |
| **Stable address** | None — clients must track node IPs | One stable IP per Service |
| **Health checks** | kube-proxy load-balances to healthy *pods*; nothing tells the client when a *node* dies | LB provider health-checks the nodes and stops sending traffic to dead ones |
| **Cloud cost** | Free | One LB per Service, billed by the cloud |
| **Typical use** | Dev/CI clusters, or behind an external LB / Ingress as a backend | Production public-facing services on cloud, or on bare metal after MetalLB |
| **Anti-pattern** | Exposing a NodePort directly to the public internet (high port, no TLS, no LB health-check) | One `LoadBalancer` Service per microservice in production — use one Ingress + many `ClusterIP` instead |

On a cloud production cluster the common pattern is **one `LoadBalancer` Service in front of an Ingress controller**, then many `ClusterIP` Services behind it. `NodePort` shows up either inside that chain (some Ingress installers use it) or in dev clusters like this one.

### Fallback: `kubectl port-forward`

Works anywhere, bypasses Service routing entirely (the api-server tunnels to a backing pod). Useful for debugging, not for understanding Services:

```bash
kubectl port-forward svc/k8s-go-service 8080:80
TOKEN=$(kubectl get secret k8s-go-secrets -o jsonpath='{.data.API_TOKEN}' | base64 -d)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/hello
```

### Next step

Install MetalLB in layer-2 mode → `<pending>` on `k8s-go-service` becomes a real IP from the kind docker subnet.

## Walkthrough

Step-by-step write-up of how this repo is put together: [yinebebt.com/post/k8s-go-app/](https://yinebebt.com/post/k8s-go-app/)
