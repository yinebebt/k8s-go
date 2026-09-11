# k8s-go

[![GHCR Image](https://img.shields.io/badge/ghcr.io-k8s--go-blue?logo=github)](https://github.com/yinebebt/k8s-go/pkgs/container/k8s-go)

Go HTTP server packaged for learning Kubernetes. Handler exposes `/livez` and `/readyz` probes, and checks a Bearer token from a `Secret` on `/hello`.

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
curl http://localhost:8080/livez
curl http://localhost:8080/readyz
```

Common local workflows are also available through the `Makefile`:

```bash
make check
make build
make docker-build TAG=0.2
make load TAG=0.2 CLUSTER=k8s-go
make deploy
make logs
```

## Build Docker image

```bash
docker build -t ghcr.io/yinebebt/k8s-go:0.2 .
docker push ghcr.io/yinebebt/k8s-go:0.2   # after authenticating to ghcr.io
```

GitHub Actions publishes tagged images to GitHub Container Registry (GHCR)
using the automatic `GITHUB_TOKEN`, so no additional registry credentials are
needed in the repository. For a private GHCR package, configure an image pull
secret in the target cluster. A local `kind` cluster can avoid registry access
with `make load`.

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
kind create cluster --name k8s-go --config kind-config.yaml
kubectl cluster-info --context kind-k8s-go
docker ps                       # node is the container k8s-go-control-plane; only the api-server port is published
docker network inspect kind     # kind always uses a docker network called "kind", regardless of cluster name
```

The repository's `kind-config.yaml` also maps host ports `8080` and `8443` to
the node's ports `80` and `443`, which are used by the kind ingress-nginx
manifest. This makes the Ingress reachable from the host at
`http://localhost:8080` and `https://localhost:8443`. Port mappings are fixed
when the kind node is created; changing the file requires recreating the
cluster.

Inspect what `kubeadm` set up inside the node:

```bash
docker exec -it k8s-go-control-plane crictl ps                  # api-server, etcd, controller-manager, scheduler, kube-proxy as containers
docker exec -it k8s-go-control-plane ls /etc/kubernetes/manifests # static pod manifests kubelet watches
```

Load the locally built image into the cluster (no registry push needed). **`kind load` defaults to a cluster named `kind`** — if your cluster has any other name you must pass `--name` or you get `ERROR: no nodes found for cluster "kind"`:

```bash
kind load docker-image ghcr.io/yinebebt/k8s-go:0.2 --name k8s-go
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

Manifests live one-per-resource under `k8s/`, orchestrated by a Kustomize root:

```
k8s/
├── kustomization.yaml     # Kustomize root — namespace, labels, image tag pin
├── namespace.yaml         # `k8s-go` namespace
├── configmap.yaml         # app config (LOG_LEVEL, …)
├── deployment.yaml        # 4 replicas, probes, resources, envFrom CM + env from Secret
├── service.yaml           # Internal ClusterIP Service for the application
├── ingress.yaml           # HTTP routing from ingress-nginx to the application Service
├── pdb.yaml               # keeps two pods available during voluntary disruptions
├── metallb-pool.yaml      # IPAddressPool + L2Advertisement (kind subnet, metallb-system NS — outside kustomization)
├── secret.example.yaml    # template; copy → secret.yaml and fill in
└── secret.yaml            # real values, gitignored, applied separately
```

`kustomization.yaml` injects the namespace and standard labels onto every resource it lists, so the individual manifests stay minimal. Two files sit **outside** the kustomization on purpose:

- `metallb-pool.yaml` — lives in the `metallb-system` namespace (MetalLB controller hardcoded to watch that NS). Including it in a kustomization that sets `namespace: k8s-go` would rewrite its NS to the wrong place.
- `secret.yaml` — gitignored. Including it in `resources:` would break `kubectl apply -k` on any fresh clone where the file doesn't exist yet.

The `deploy` Make target applies these resources in the required order.

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

Kustomize emits resources in GVK (Group/Version/Kind) order — Namespace → CRD → RBAC → ConfigMap → Secret → Service → Deployment. The Namespace always lands first, so namespaced resources never race against its creation. No filename prefix hack needed.

Other approaches you might see in other repos:
- **Plain `kubectl apply -f dir/`** — alphabetical filename sort. Common convention: numeric prefix `00-`, `10-`, `20-` to force order. That's what this repo used pre-Kustomize.
- **Helm** — same GVK sort as Kustomize + lifecycle hooks (`pre-install`, `post-install`) for explicit phases.
- **Argo CD / Flux** — sync waves (`argocd.argoproj.io/sync-wave: "-1"` on the namespace, `"0"` on workloads).
- **Server-side apply + retry** — apply everything, retry on transient errors until convergence.

### Applying changes

Use `make deploy` for the complete ordered deployment. Use
`kubectl kustomize k8s/` or `kubectl diff -k k8s/` when reviewing manifests.

Bumping the image tag requires updating `images.newTag` and the
`app.kubernetes.io/version` label in `kustomization.yaml`.

MetalLB itself is installed once per cluster from upstream. `metallb-pool.yaml` is *configuration* for that install — the CRDs it uses (`IPAddressPool`, `L2Advertisement`) only resolve after the install manifest has been applied.

## Deploy

Create the local Secret from its template, replace the token, then run
`make deploy`. The target installs MetalLB, applies the manifests in order, and
waits for the rollout.

```bash
cp k8s/secret.example.yaml k8s/secret.yaml
```

Edit `k8s/secret.yaml`, then run:

```bash
make deploy
```

For a cluster created with this repository's `kind-config.yaml`, test the
Ingress from the host:

```bash
curl --fail http://localhost:8080/livez

TOKEN=$(kubectl get secret -n k8s-go k8s-go-secrets \
  -o jsonpath='{.data.API_TOKEN}' | base64 -d)
curl --fail -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/hello
```

If the cluster already existed before `kind-config.yaml` was added, recreate
it once so Docker publishes the ports:

```bash
kind delete cluster --name k8s-go
make recreate
make deploy
```

The Ingress controller's `LoadBalancer` Service remains `<pending>` until
MetalLB is installed and configured.

### Secrets: template-in-git vs real value out-of-git

This repo uses the **`.example.yaml` template + gitignored real file** pattern (mirrors the popular `.env.example` convention):

- `k8s/secret.example.yaml` — committed, holds the schema and a placeholder (`API_TOKEN: "replace-me"`).
- `k8s/secret.yaml` — gitignored (`.gitignore` line: `k8s/secret.yaml`), holds the real value, applied locally only.

Pros: zero extra tooling, obvious to a reader. Cons: real secret lives in plaintext on every dev's disk, and not reconcilable from git (every cluster needs out-of-band provisioning). The earlier apply-collision foot-gun (both Secret files getting applied by `kubectl apply -f k8s/`) is now gone — only `secret.yaml` is applied explicitly, and `secret.example.yaml` lives outside the Kustomize root as a pure schema reference.

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

When this repo grows past the demo stage, any of the above is a step up. A natural next move with Kustomize already in place is `secretGenerator` from a gitignored `secret.env` file — that kills the disk-plaintext step entirely while staying within stock Kustomize. The `secret.example.yaml` template stays useful as a schema reference even after migration.

## Accessing the Service

`k8s/` ships one application Service:

```bash
kubectl get svc -n k8s-go -l app=k8s-go
# NAME             TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)
# k8s-go-service   ClusterIP   10.96.x.x    <none>        80/TCP
```

### Service fundamentals

Pods have IP addresses, but Pods are ephemeral: a Deployment can replace them
and their addresses can change. A Service gives a changing set of selected
Pods a stable virtual IP and DNS name. The selector chooses the backing Pods;
the Service does not select a particular container.

`port` is the port exposed by the Service, while `targetPort` is the port on
the selected Pod. This repository uses the named container port `http` as its
`targetPort`, which resolves to port 8080 in the Deployment. A Pod may contain
multiple containers, but those containers share the Pod's network namespace and
IP address.

`ClusterIP` is the default Service type and is reachable inside the cluster.
This project deliberately uses it for the application. The external entry
point is the Ingress controller, not a separate public Service for every app.
`NodePort` opens a port on every node and is useful for learning or as a
backend for another load balancer. `LoadBalancer` asks an external
implementation, such as a cloud controller or MetalLB, for an external IP;
Kubernetes still routes the traffic from that Service to ready selected Pods.

A headless Service sets `clusterIP: None`. It has no virtual IP; DNS returns
the selected Pod addresses so a client or client-side library can choose a
Pod. DNS is still commonly used, so "headless" does not mean "without DNS."

### The LoadBalancer and Ingress path

The application Service is intentionally `ClusterIP`; it is not directly
public. The Ingress controller is the public entry point:

```text
client -> MetalLB VIP -> ingress-nginx LoadBalancer Service
       -> Ingress rule -> k8s-go-service ClusterIP -> ready Pod
```

Vanilla Kubernetes has no load-balancer implementation. In a cloud cluster,
the cloud-controller-manager provisions the external load balancer. On
`kind`, MetalLB watches the `ingress-nginx-controller` `LoadBalancer` Service,
assigns a VIP from the configured pool, and announces it on the Docker bridge.

Install the controller and deploy the application with:

```bash
make deploy
```

The controller manifest is pinned in the `Makefile`; the application Ingress
is in `k8s/ingress.yaml`.

This is why the repository still uses MetalLB after changing the application
Service to `ClusterIP`: MetalLB provides the external IP for the
`ingress-nginx-controller` `LoadBalancer` Service. It does not expose
`k8s-go-service` directly.

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

On a cloud production cluster the common pattern is **one `LoadBalancer`
Service in front of an Ingress controller**, then many `ClusterIP` Services
behind it. NodePort may be used internally by some ingress installations, but
it is not part of this application's public path.

### Direct host access

The normal walkthrough does not use port-forwarding. The supported path is
host port `8080` through kind and ingress-nginx.

```bash
TOKEN=$(kubectl get secret -n k8s-go k8s-go-secrets -o jsonpath='{.data.API_TOKEN}' | base64 -d)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/hello
```

### Verify the Ingress path

Check each hop from the public controller to the application Pods:

```bash
kubectl get ingress -n k8s-go
kubectl describe ingress -n k8s-go k8s-go
kubectl get svc,endpoints,pods -n k8s-go
kubectl get svc -n ingress-nginx ingress-nginx-controller
```

The application Service should remain `ClusterIP`. The
`ingress-nginx-controller` Service is the `LoadBalancer` that receives the
MetalLB VIP.

## Install MetalLB (layer 2)

`EXTERNAL-IP <pending>` on the `ingress-nginx-controller` Service is the
missing-LB-controller story. [MetalLB](https://metallb.io) watches that
`Service type=LoadBalancer`, pulls an IP from a pool we own, and gets one node
to answer ARP (Address Resolution Protocol) for it. The application remains
behind its internal `ClusterIP` Service.

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

The kind node is a Docker container on the `kind` docker bridge network. That bridge is a normal Linux L2 segment — any container joined to it sees ARP from its neighbors. So if MetalLB hands `ingress-nginx-controller` an IP from inside the bridge subnet and a speaker ARPs for it, any container on the same `kind` network can reach it. Outside that bridge (your mac/windows shell, another docker network) the IP is unroutable — same constraint as the NodePort path above.

### Step 1 — Pick a pool inside the kind subnet

**The range is not arbitrary.** L2 mode announces VIPs via ARP, which only travels inside one broadcast domain. The VIP therefore has to live on the same L2 segment as the cluster's nodes — for kind that's the `kind` Docker bridge. Picking an IP outside that bridge's subnet means the speaker ARPs into a network nobody listens on, and the VIP is unreachable forever.

Docker assigns the `kind` bridge a subnet when the network is first created — usually somewhere in the `172.x.0.0/16` private range, but the exact `/16` varies by host. Read yours:

```bash
docker network inspect kind -f '{{range .IPAM.Config}}{{.Subnet}}{{"\n"}}{{end}}'
# 172.22.0.0/16          ← IPv4 (yours may differ)
# fc00:f853:ccd:e793::/64 ← IPv6
```

Pick a range high in the subnet — Docker hands out the low end via its own DHCP and the kind node sits at `.0.2`. The repo's `k8s/metallb-pool.yaml` uses `172.22.255.200-172.22.255.250`. If your subnet differs, edit that line before applying.

**Allocation order is deterministic, not random.** MetalLB walks the pool
low-to-high and gives each new `LoadBalancer` Service the first free IP. In
this project, `ingress-nginx-controller` gets `.200` (the start of the range).
Delete and re-apply → same IP back. Pin a specific one with the annotation
`metallb.universe.tf/loadBalancerIPs: 172.22.255.230` if you need stability
across pool changes.

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
kubectl get svc -n ingress-nginx ingress-nginx-controller -w
# NAME             TYPE           CLUSTER-IP    EXTERNAL-IP       PORT(S)
# ingress-nginx-controller   LoadBalancer   10.96.x.x   172.22.255.200   80:3xxxx/TCP
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

`curl 172.22.255.200` from a macOS/Windows shell hangs — the `kind` bridge
lives inside Docker Desktop's small Linux VM, and the host has no route to it.
The ARP reply the speaker sends never reaches your terminal. **Not a bug, not
a misconfiguration** — it is a platform constraint. On native Linux with
Docker's bridge driver the bridge sits on the host kernel and the VIP is
reachable directly. Real bare-metal MetalLB has no such issue.

If you need the VIP reachable from the host shell on macOS/Windows, pick one:

| Option | What it does | Trade-off |
|---|---|---|
| `docker run --network kind curlimages/curl …` | Test from a sidecar container on the same bridge | Mirrors how real clients reach an LB (same L2). The lesson. |
| `kubectl port-forward -n k8s-go svc/k8s-go-service 18080:80` | api-server tunnels to a backing pod | Works anywhere, but skips Service routing — debug only. |
| [`cloud-provider-kind`](https://kind.sigs.k8s.io/docs/user/loadbalancer/) | Host-side daemon proxies `LoadBalancer` Services to host ports | Replaces MetalLB for the host-reachability role; closer to what Docker Desktop's built-in LB and `minikube tunnel` do. Mutually exclusive with MetalLB on the same Services. |
| Linux host (native, Lima, Colima with bridge net) | Host kernel owns the bridge | No extra plumbing. Same as production bare-metal. |

The repo sticks with MetalLB + the sidecar-container test because the goal is to see a real LB controller in action, not to make `localhost` work.

### Troubleshooting

| Symptom | Cause |
|---|---|
| `EXTERNAL-IP` stays `<pending>` after pool apply | No `L2Advertisement` referencing the pool, or pool exhausted. `kubectl describe svc -n ingress-nginx ingress-nginx-controller` shows the allocator's reason. |
| VIP assigned, `curl` from kind-net container times out | speaker pod not Running, or pool range outside the actual kind subnet. Check `docker network inspect kind` again. |
| `webhook "ipaddresspoolvalidationwebhook.metallb.io" ... connection refused` on first apply | Webhook pod not Ready yet. Retry in ~10 s. |
| `MountVolume.SetUp failed for volume "memberlist"` on speaker | Controller hasn't created the `memberlist` secret yet. Self-heals once controller is up. |
| `curl <VIP>` from macOS/Windows shell hangs | Docker Desktop VM hides the `kind` bridge from the host. See §Why curl from the host hangs on macOS/Windows. |
| `kubectl apply -f k8s/metallb-pool.yaml` errors `no matches for kind "IPAddressPool"` | MetalLB install manifest hasn't been applied yet. Run §Step 3 first. |
| `kind load docker-image …` errors `no nodes found for cluster "kind"` | Default cluster name is `kind`, not `k8s-go`. Add `--name k8s-go`. |

### From zero on a clean kind cluster

Use the Makefile for image loading and deployment:

```bash
# 0. (optional) wipe any prior cluster
kind delete cluster --name k8s-go

# 1. fresh cluster
make load
kubectl cluster-info --context kind-k8s-go

# 2. load the app image so kind doesn't try to pull from a registry
make load TAG=0.2 CLUSTER=k8s-go
make deploy

# 4. confirm the kind docker subnet matches the pool in k8s/metallb-pool.yaml
docker network inspect kind -f '{{range .IPAM.Config}}{{.Subnet}}{{"\n"}}{{end}}'
# if your IPv4 subnet is not 172.22.0.0/16, edit k8s/metallb-pool.yaml first

# 5. secret with the real token (file is gitignored)
cp k8s/secret.example.yaml k8s/secret.yaml
$EDITOR k8s/secret.yaml      # replace API_TOKEN value

# 6. apply app stack via Kustomize, then the two out-of-kustomization files
make deploy

# Watch the assigned Ingress LoadBalancer address.
kubectl get svc -n ingress-nginx ingress-nginx-controller -w

VIP=$(kubectl get svc -n ingress-nginx ingress-nginx-controller -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
TOKEN=$(kubectl get secret -n k8s-go k8s-go-secrets -o jsonpath='{.data.API_TOKEN}' | base64 -d)
docker run --rm --network kind curlimages/curl -sS -H "Authorization: Bearer $TOKEN" http://$VIP/hello
```
## Walkthrough

Step-by-step write-up of how this repo is put together:
[yinebebt.com/projects/k8s-go/](https://yinebebt.com/projects/k8s-go/)
