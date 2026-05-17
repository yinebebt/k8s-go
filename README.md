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

A cluster is: a **control plane** (api-server, scheduler, controller-manager, etcd) plus one or more **nodes** running `kubelet` + a container runtime (containerd) + `kube-proxy`. A **CNI (Container Network Interface) plugin** wires pod networking. That is it. Nothing in this list is a load balancer, an ingress, or a DNS for external traffic — those are add-ons.

### Why not just run the k8s binaries directly?

The Kubernetes release ships ~6 standalone Go binaries (`kube-apiserver`, `kube-controller-manager`, `kube-scheduler`, `kube-proxy`, `kubelet`, `kubectl`) plus `etcd`. Nothing prevents you from `wget`-ing them and running them — but to get a *working* cluster you also have to:

- generate a CA and ~10 TLS certs / kubeconfigs (api-server serving cert, kubelet client cert, controller-manager kubeconfig, scheduler kubeconfig, service-account signing key, etcd peer + client certs, front-proxy CA…),
- run `etcd` with proper peer/client TLS,
- start `kube-apiserver` wired to that `etcd`, with the right `--service-cluster-ip-range`, `--service-account-*` flags, admission plugins, audit policy,
- start `kube-controller-manager` and `kube-scheduler` pointed at the api-server,
- on every node: enable `br_netfilter`, `ip_forward`, swap off, install a container runtime + CRI socket, start `kubelet` with the right cgroup driver and `--kubeconfig`,
- install a CNI plugin (Calico, Cilium, kindnet…) and write `/etc/cni/net.d/*.conf`,
- start `kube-proxy` (iptables or IPVS (IP Virtual Server, a Linux kernel L4 load balancer)),
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

## Layout of `k8s/`

Manifests live one-per-resource under `k8s/`:

```
k8s/
├── 00-namespace.yaml      # `k8s-go` namespace — must exist before its members
├── configmap.yaml         # app config (LOG_LEVEL, …)
├── deployment.yaml        # 4 replicas, probes, resources, envFrom CM + env from Secret
├── service.yaml           # Two Services (LoadBalancer + NodePort) against the same pods
├── metallb-pool.yaml      # IPAddressPool + L2Advertisement (kind subnet, metallb-system NS)
├── secret.example.yaml    # template; copy → secret.yaml and fill in
└── secret.yaml            # real values, gitignored
```

App resources (configmap, deployment, service, secret) all set `metadata.namespace: k8s-go`. MetalLB pool stays in `metallb-system` (controller watches that namespace only — non-negotiable).

### Why a dedicated namespace?

A namespace is a logical partition of API objects. Same kind+name can coexist in different namespaces. Buys you:

- **Scope names** — `k8s-go-service` doesn't collide with another team's `k8s-go-service`
- **RBAC (Role-Based Access Control) unit** — grant rights on `k8s-go` namespace only
- **Quota unit** — `ResourceQuota`/`LimitRange` apply per-namespace
- **NetworkPolicy target** — policies select by namespace label
- **Cleanup unit** — `kubectl delete ns k8s-go` nukes everything inside

Cluster-scoped resources (Node, PersistentVolume, ClusterRole, Custom Resource Definitions, Namespace itself) ignore this; they live at the cluster level.

**`default` is itself a namespace.** Every cluster ships with four built-in namespaces:

| Namespace | Role |
|---|---|
| `default` | Catch-all for any resource that omits `metadata.namespace`. Not special, just unstyled. |
| `kube-system` | Control plane (`coredns`, `kube-proxy`, etc). |
| `kube-public` | Readable by every authenticated user; rarely used. |
| `kube-node-lease` | Per-node `Lease` objects for heartbeats. |

`kubectl get svc` without `-n …` queries your kubeconfig context's namespace, which is unset → falls back to `default`. The `kubernetes` ClusterIP Service you see there is the in-cluster reference to the API server itself, auto-created by the control plane.

Switch context once and the `-n` flag stops being needed:

```bash
kubectl config set-context --current --namespace=k8s-go
kubectl config view --minify -o jsonpath='{..namespace}'   # confirm
```

### Apply order

`kubectl apply -f k8s/` processes files **alphabetically**. The `00-` prefix on `00-namespace.yaml` is deliberate: it wins the sort so the namespace exists before any namespaced resource references it. Without it, the first apply on a fresh cluster errors with `namespaces "k8s-go" not found`.

Industry alternatives (for context):
- **Kustomize / Helm** — emit resources in a GVK (Group/Version/Kind) sorted order (Namespace → CRD → RBAC → ConfigMap → Secret → Service → Deployment). No filename hack needed.
- **Argo CD / Flux** — sync waves (`argocd.argoproj.io/sync-wave: "-1"` on the namespace, `"0"` on workloads).
- **Server-side apply + retry** — apply everything, retry on transient errors until convergence.

When this repo adopts Kustomize (see TODO), the `00-` prefix becomes redundant and can drop.

### Apply patterns

```bash
kubectl apply -f k8s/                 # everything (alphabetical; 00-namespace wins first)
kubectl apply -f k8s/deployment.yaml  # just the Deployment after an image bump
```

MetalLB itself is installed once per cluster from upstream (see §Install MetalLB). `metallb-pool.yaml` is *configuration* for that install — the CRDs it uses (`IPAddressPool`, `L2Advertisement`) only resolve after the install manifest has been applied.

## Deploy

Prerequisite: a cluster reachable via `kubectl cluster-info`.

```bash
# 1. Create the Secret, replace API_TOKEN value
cp k8s/secret.example.yaml k8s/secret.yaml

# 2. Apply everything (00-namespace first via alphabetical sort, then the rest)
kubectl apply -f k8s/

# 3. Block until all replicas are Ready
kubectl rollout status -n k8s-go deployment/k8s-go-deployment
```

Optional — pin your shell to the `k8s-go` namespace so you can drop `-n k8s-go` from every command:

```bash
kubectl config set-context --current --namespace=k8s-go
```

If MetalLB isn't installed yet, `metallb-pool.yaml` errors with `no matches for kind "IPAddressPool"`. Either install MetalLB first (see §Install MetalLB), or skip the pool for now: `kubectl apply -f k8s/00-namespace.yaml -f k8s/configmap.yaml -f k8s/deployment.yaml -f k8s/service.yaml -f k8s/secret.yaml`. The `LoadBalancer` Service will sit at `<pending>` until MetalLB is in place — that's the path the next section walks through.

### Secrets: template-in-git vs real value out-of-git

This repo uses the **`.example.yaml` template + gitignored real file** pattern (mirrors the popular `.env.example` convention):

- `k8s/secret.example.yaml` — committed, holds the schema and a placeholder (`API_TOKEN: "replace-me"`).
- `k8s/secret.yaml` — gitignored (`.gitignore` line: `k8s/secret.yaml`), holds the real value, applied locally only.

Pros: zero extra tooling, obvious to a reader. Cons: real secret lives in plaintext on every dev's disk, not reconcilable from git (every cluster needs out-of-band provisioning), and `kubectl apply -f k8s/` applies *both* files sequentially — the alphabetical order saves us today (real wins because `secret.yaml` sorts after `secret.example.yaml`), but rename either file and you flip back to placeholder. Fragile.

Industry-standard alternatives for shipping secrets *with* GitOps:

| Pattern | Tool | Idea |
|---|---|---|
| **Sealed Secrets** | [bitnami-labs/sealed-secrets](https://github.com/bitnami-labs/sealed-secrets) | Encrypt the Secret with the cluster's public key, commit the `SealedSecret` YAML. Controller decrypts in-cluster. Only that cluster can read it. |
| **SOPS** (Secrets OPerationS — encrypted-at-rest) | [getsops/sops](https://github.com/getsops/sops) (CNCF sandbox) + age / PGP / cloud KMS (Key Management Service) | Encrypt YAML field-by-field, commit `secret.enc.yaml`. Decrypt at apply time (Flux + Kustomize have native SOPS support). |
| **External Secrets Operator (ESO)** | [external-secrets.io](https://external-secrets.io/) | Commit an `ExternalSecret` reference. Operator fetches the real value from AWS Secrets Manager / Vault / GCP Secret Manager / 1Password / Azure Key Vault and materializes a regular `Secret`. |
| **Vault Agent Injector / CSI Secrets Store** | [vault-k8s](https://github.com/hashicorp/vault-k8s), [secrets-store-csi-driver](https://secrets-store-csi-driver.sigs.k8s.io/) | Inject secrets into the pod via init container or CSI volume at startup. No `Secret` object in the API at all. |
| **Imperative `kubectl create secret`** | built-in | Skip the manifest entirely: `kubectl create secret generic k8s-go-secrets --from-literal=API_TOKEN=$(openssl rand -hex 32) -n k8s-go`. No file = no commit risk. Loses declarative reconciliation. |

Rule of thumb:
- **Solo dev / learning repo** (this one) — template + gitignored real, or imperative create. Fine until you need a second cluster.
- **Single team, single cloud** — Sealed Secrets (simplest) or SOPS (works offline, no controller needed at decrypt-time on Flux).
- **Multi-team / regulated / rotating secrets** — ESO or Vault. Secret lives in a real KMS; pods get the latest on every restart.

When this repo grows past the demo stage, picking one of the above closes the foot-gun in §Apply patterns (the two-Secret-file apply collision). Until then, the `secret.example.yaml` template stays useful as a schema reference even after migration.

## Accessing the Service

`k8s/` ships **two** Service objects against the same pods so you can compare:

```bash
kubectl get svc -n k8s-go -l app=k8s-go
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
- `kubectl port-forward -n k8s-go svc/k8s-go-nodeport 30080:80` — tunnels via the api-server to a backing pod. Skips the NodePort path itself.
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
kubectl port-forward -n k8s-go svc/k8s-go-service 8080:80
TOKEN=$(kubectl get secret -n k8s-go k8s-go-secrets -o jsonpath='{.data.API_TOKEN}' | base64 -d)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/hello
```

## Install MetalLB (layer 2)

`EXTERNAL-IP <pending>` is the missing-LB-controller story. [MetalLB](https://metallb.io) is the controller. It watches `Service type=LoadBalancer`, pulls an IP from a pool we own, and gets one node to answer ARP (Address Resolution Protocol) for it. Same `Service` API, no app change.

### What MetalLB is

Two workloads in the `metallb-system` namespace:

| Component | Kind | Job |
|---|---|---|
| `controller` | Deployment (1 replica) | Watches `Service` objects, allocates an IP from an `IPAddressPool`, writes it into `.status.loadBalancer.ingress`. |
| `speaker` | DaemonSet (every node) | Announces assigned IPs on the local network. In L2 (Layer 2) mode that means raw ARP (IPv4) / NDP (Neighbor Discovery Protocol, IPv6). In BGP (Border Gateway Protocol) mode it peers with a router. |

Two CRDs you write:

- `IPAddressPool` — IP ranges MetalLB is allowed to hand out.
- `L2Advertisement` (or `BGPAdvertisement`) — *how* to announce. Without one, IPs get assigned but nothing answers for them.

### Why two modes — and why L2 here

MetalLB ships **L2** and **BGP**.

- **L2 mode** — one speaker wins an election per VIP (Virtual IP) and ARP-replies for it on its node's interface. Failover ≈ 10 s on node loss. Single node carries all traffic for that VIP (no sharding); kube-proxy still load-balances pod-to-pod after the packet lands. Fine for dev clusters and small bare-metal. No router config required.
- **BGP mode** — each speaker peers with an upstream router and announces the VIP. Router uses ECMP (Equal-Cost Multi-Path) to spread connections across nodes. True multi-node throughput, sub-second failover, but you need a router that speaks BGP and someone to own the peering session.

On kind there's no router to peer with, so L2 is the only sensible choice. The "router" is the host's Docker bridge, and ARP just works inside that bridge.

### Why this works on kind specifically

The kind node is a Docker container on the `kind` docker bridge network. That bridge is a normal Linux L2 segment — any container joined to it sees ARP from its neighbors. So if MetalLB hands `k8s-go-service` an IP from inside the bridge subnet and a speaker ARPs for it, any container on the same `kind` network can reach it. Outside that bridge (your mac/windows shell, another docker network) the IP is unroutable — same constraint as the NodePort path above.

### Step 1 — Pick a pool inside the kind subnet

**The range is not arbitrary.** L2 mode announces VIPs via ARP, which only travels inside one broadcast domain. The VIP therefore has to live on the same L2 segment as the cluster's nodes — for kind that's the `kind` Docker bridge. Picking an IP outside that bridge's subnet means the speaker ARPs into a network nobody listens on, and the VIP is unreachable forever.

Docker assigns the `kind` bridge a subnet when the network is first created — usually somewhere in the `172.x.0.0/16` private range, but the exact `/16` varies by host. Read yours:

```bash
docker network inspect kind -f '{{range .IPAM.Config}}{{.Subnet}}{{"\n"}}{{end}}'
# 172.22.0.0/16          ← IPv4 (yours may differ)
# fc00:f853:ccd:e793::/64 ← IPv6
```

Pick a range high in the subnet — Docker hands out the low end via its own DHCP and the kind node sits at `.0.2`. The repo's `k8s/metallb-pool.yaml` uses `172.22.255.200-172.22.255.250`. If your subnet differs, edit that line before applying.

**Allocation order is deterministic, not random.** MetalLB walks the pool low-to-high and gives each new `LoadBalancer` Service the first free IP. That's why `k8s-go-service` ends up at `.200` (start of range) and a hypothetical second LB Service would get `.201`. Delete and re-apply → same IP back. Pin a specific one with the annotation `metallb.universe.tf/loadBalancerIPs: 172.22.255.230` if you need stability across pool changes.

#### What changes on other cluster managers

Pool boundaries are dictated by **whichever network the nodes sit on**. MetalLB itself never inspects Docker, kind, or the cluster type — it just announces whatever range you give it. Same `IPAddressPool` YAML, different range:

| Cluster manager | Find the node network | Typical subnet |
|---|---|---|
| **kind** | `docker network inspect kind` | `172.18-31.0.0/16` (Docker picks first free /16) |
| **k3d** | `docker network inspect k3d-<cluster-name>` | Same Docker /16 pool |
| **Minikube (docker driver)** | `docker network inspect minikube` | `192.168.49.0/24` common |
| **Minikube (VM driver: hyperkit/kvm/virtualbox)** | `minikube ip` then `ip route` inside the host | `192.168.49.0/24` or similar host-only net |
| **Docker Desktop k8s** | Rarely uses MetalLB — built-in LB shim maps to `localhost`. | n/a |
| **Bare metal** | Ask your network admin for an unallocated range on the node VLAN (Virtual LAN). | e.g. `10.10.50.200-.250` |
| **Cloud (EKS / GKE / AKS)** | Don't install MetalLB — cloud-controller-manager owns `LoadBalancer`. | n/a |

The constraint is L2-reachability from clients to nodes, nothing else. Swap kind for k3d on this host and the pool YAML still works after editing the range to the new docker bridge's subnet.

### Step 2 — kube-proxy mode check

If kube-proxy runs in IPVS mode, MetalLB needs `strictARP: true` so the speaker can answer ARP for VIPs the node doesn't own. kind defaults to iptables mode, so no edit is needed here. To confirm:

```bash
kubectl -n kube-system get configmap kube-proxy -o jsonpath='{.data.config\.conf}' | grep -E 'mode:|strictARP'
# mode: iptables
# strictARP: false  ← fine in iptables mode; only matters for ipvs
```

If you switch to ipvs:

```bash
kubectl get configmap kube-proxy -n kube-system -o yaml \
  | sed -e 's/strictARP: false/strictARP: true/' \
  | kubectl apply -f - -n kube-system
```

### Step 3 — Install MetalLB

Pin a version, don't track `main` — CRDs change.

```bash
kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.15.3/config/manifests/metallb-native.yaml
kubectl -n metallb-system wait --for=condition=Ready pod --all --timeout=180s
kubectl -n metallb-system get pods
# controller-...   1/1   Running
# speaker-...      1/1   Running
```

`metallb-native.yaml` is the lightweight variant (no FRR (Free Range Routing) sidecar). Enough for L2. The FRR variant (`metallb-frr-k8s.yaml`) only matters if you need BGP with BFD (Bidirectional Forwarding Detection — sub-second neighbor liveness for fast failover) or IPv6 BGP.

First-run quirk: the speaker pod stays in `ContainerCreating` for ~30 s with `MountVolume.SetUp failed for volume "memberlist": secret "memberlist" not found`. The controller creates that secret on startup, so the speaker only succeeds *after* the controller is up. Normal. Wait it out, don't redeploy.

### Step 4 — Apply pool + advertisement

```bash
kubectl apply -f k8s/metallb-pool.yaml
kubectl -n metallb-system get ipaddresspool,l2advertisement
```

`metallb-pool.yaml` ships two objects:

- `IPAddressPool/kind-pool` — the range from step 1.
- `L2Advertisement/kind-l2` — scoped to `kind-pool`. Leave `ipAddressPools` empty if you want every pool advertised.

### Step 5 — Watch `<pending>` flip

```bash
kubectl get svc -n k8s-go k8s-go-service -w
# NAME             TYPE           CLUSTER-IP    EXTERNAL-IP       PORT(S)
# k8s-go-service   LoadBalancer   10.96.x.x     172.22.255.200    80:31056/TCP
```

The IP comes from `kind-pool`. Reach it from a container on the same docker network:

```bash
docker run --rm --network kind curlimages/curl -sS http://172.22.255.200/livez
# 200

TOKEN=$(kubectl get secret -n k8s-go k8s-go-secrets -o jsonpath='{.data.API_TOKEN}' | base64 -d)
docker run --rm --network kind curlimages/curl -sS \
  -H "Authorization: Bearer $TOKEN" http://172.22.255.200/hello
# Hello, Welcome to Kubernetes world!
```

### Why curl from the host hangs on macOS/Windows

`curl 172.22.255.200` from a macOS/Windows shell hangs — same caveat as NodePort. Docker Desktop runs Docker inside a small Linux VM; the `kind` bridge lives *inside* that VM and the host has no route to it. The ARP reply the speaker sends never reaches your terminal. **Not a bug, not a misconfig** — it's a platform constraint, identical to why `curl <node-ip>:30080` hangs. On native Linux with Docker's bridge driver the bridge sits on the host kernel and the VIP is reachable directly. Real bare-metal MetalLB has no such issue.

If you need the VIP reachable from the host shell on macOS/Windows, pick one:

| Option | What it does | Trade-off |
|---|---|---|
| `docker run --network kind curlimages/curl …` | Test from a sidecar container on the same bridge | Mirrors how real clients reach an LB (same L2). The lesson. |
| `kubectl port-forward -n k8s-go svc/k8s-go-service 8080:80` | api-server tunnels to a backing pod | Works anywhere, but skips Service routing — debug only. |
| [`cloud-provider-kind`](https://kind.sigs.k8s.io/docs/user/loadbalancer/) | Host-side daemon proxies `LoadBalancer` Services to host ports | Replaces MetalLB for the host-reachability role; closer to what Docker Desktop's built-in LB and `minikube tunnel` do. Mutually exclusive with MetalLB on the same Services. |
| Linux host (native, Lima, Colima with bridge net) | Host kernel owns the bridge | No extra plumbing. Same as production bare-metal. |

The repo sticks with MetalLB + the sidecar-container test because the goal is to see a real LB controller in action, not to make `localhost` work.

### Troubleshooting

| Symptom | Cause |
|---|---|
| `EXTERNAL-IP` stays `<pending>` after pool apply | No `L2Advertisement` referencing the pool, or pool exhausted. `kubectl describe svc -n k8s-go k8s-go-service` shows the allocator's reason. |
| VIP assigned, `curl` from kind-net container times out | speaker pod not Running, or pool range outside the actual kind subnet. Check `docker network inspect kind` again. |
| `webhook "ipaddresspoolvalidationwebhook.metallb.io" ... connection refused` on first apply | Webhook pod not Ready yet. Retry in ~10 s. |
| `MountVolume.SetUp failed for volume "memberlist"` on speaker | Controller hasn't created the `memberlist` secret yet. Self-heals once controller is up. |
| `curl <VIP>` from macOS/Windows shell hangs | Docker Desktop VM hides the `kind` bridge from the host. See §Why curl from the host hangs on macOS/Windows. |
| `kubectl apply -f k8s/` errors `no matches for kind "IPAddressPool"` | MetalLB install manifest hasn't been applied yet. Run §Step 3 first. |
| `kind load docker-image …` errors `no nodes found for cluster "kind"` | Default cluster name is `kind`, not `k8s-go`. Add `--name k8s-go`. |

### From zero on a clean kind cluster

Full path, copy-paste:

```bash
# 0. (optional) wipe any prior cluster
kind delete cluster --name k8s-go

# 1. fresh cluster
kind create cluster --name k8s-go
kubectl cluster-info --context kind-k8s-go

# 2. load the app image so kind doesn't try to pull from a registry
kind load docker-image yinebeb/k8s-go:0.2 --name k8s-go

# 3. install MetalLB (CRDs + controller + speaker)
kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.15.3/config/manifests/metallb-native.yaml
kubectl -n metallb-system wait --for=condition=Ready pod --all --timeout=180s

# 4. confirm the kind docker subnet matches the pool in k8s/metallb-pool.yaml
docker network inspect kind -f '{{range .IPAM.Config}}{{.Subnet}}{{"\n"}}{{end}}'
# if your IPv4 subnet is not 172.22.0.0/16, edit k8s/metallb-pool.yaml first

# 5. secret with the real token (file is gitignored)
cp k8s/secret.example.yaml k8s/secret.yaml
$EDITOR k8s/secret.yaml      # replace API_TOKEN value

# 6. apply everything app-side (Namespace, ConfigMap, Deployment, both Services, MetalLB pool)
kubectl apply -f k8s/
kubectl rollout status -n k8s-go deployment/k8s-go-deployment

# 7. watch <pending> flip to a real IP
kubectl get svc -n k8s-go k8s-go-service -w

# 8. hit the VIP from a container on the kind docker network
VIP=$(kubectl get svc -n k8s-go k8s-go-service -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
TOKEN=$(kubectl get secret -n k8s-go k8s-go-secrets -o jsonpath='{.data.API_TOKEN}' | base64 -d)
docker run --rm --network kind curlimages/curl -sS -H "Authorization: Bearer $TOKEN" http://$VIP/hello
```

Order matters: step 3 (MetalLB install) must come before step 6, otherwise `kubectl apply -f k8s/` errors with `no matches for kind "IPAddressPool"`.

### Next step

Add an Ingress controller (`ingress-nginx`) in front of the MetalLB VIP so multiple Services share one external IP via host/path routing.

## Walkthrough

Step-by-step write-up of how this repo is put together: [yinebebt.com/post/k8s-go-app/](https://yinebebt.com/post/k8s-go-app/)
