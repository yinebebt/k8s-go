IMAGE ?= ghcr.io/yinebebt/k8s-go
TAG ?= 0.2
NAMESPACE ?= k8s-go
CLUSTER ?= k8s-go
KIND_CONFIG ?= kind-config.yaml
METALLB_VERSION ?= v0.15.3
METALLB_MANIFEST := https://raw.githubusercontent.com/metallb/metallb/$(METALLB_VERSION)/config/manifests/metallb-native.yaml

.PHONY: build test lint check docker-build load recreate ingress-install metallb-install deploy undeploy logs port-forward

build:
	go build -ldflags "-X main.version=$$(git rev-parse --short HEAD)" -o main .

test:
	go test ./...

lint:
	golangci-lint run -v --fix ./...

check: test lint

docker-build:
	docker build -t $(IMAGE):$(TAG) .

load: docker-build
	@if ! kind get clusters | grep -Fxq "$(CLUSTER)"; then \
		kind create cluster --name $(CLUSTER) --config $(KIND_CONFIG); \
	fi
	kind load docker-image $(IMAGE):$(TAG) --name $(CLUSTER)

recreate:
	kind delete cluster --name $(CLUSTER)
	$(MAKE) load

metallb-install:
	kubectl apply -f $(METALLB_MANIFEST)
	kubectl -n metallb-system wait --for=condition=Available deployment/controller --timeout=180s
	kubectl -n metallb-system wait --for=condition=Ready pod --all --timeout=180s

ingress-install:
	kubectl label nodes --all ingress-ready=true --overwrite
	kubectl wait --for=condition=Ready nodes --all --timeout=180s
	kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.12.1/deploy/static/provider/kind/deploy.yaml
	kubectl -n ingress-nginx rollout status deployment/ingress-nginx-controller --timeout=180s
	kubectl -n ingress-nginx patch service ingress-nginx-controller --type=merge -p '{"spec":{"type":"LoadBalancer"}}'

deploy: ingress-install metallb-install
	kubectl delete service k8s-go-nodeport -n $(NAMESPACE) --ignore-not-found
	kubectl apply -k k8s/
	kubectl apply -f k8s/secret.yaml
	kubectl apply -f k8s/metallb-pool.yaml
	kubectl rollout status -n $(NAMESPACE) deployment/k8s-go-deployment

undeploy:
	kubectl delete namespace $(NAMESPACE) --ignore-not-found

logs:
	kubectl logs -n $(NAMESPACE) deployment/k8s-go-deployment --all-containers=true --tail=100 -f

port-forward:
	kubectl port-forward -n $(NAMESPACE) svc/$(NAMESPACE)-service 18080:80
