# k8s-go

[![Docker Image](https://img.shields.io/docker/v/yinebeb/k8s-go?label=docker&logo=docker)](https://hub.docker.com/r/yinebeb/k8s-go)
[![Docker Pulls](https://img.shields.io/docker/pulls/yinebeb/k8s-go)](https://hub.docker.com/r/yinebeb/k8s-go)
[![Image Size](https://img.shields.io/docker/image-size/yinebeb/k8s-go/1.2.1)](https://hub.docker.com/r/yinebeb/k8s-go)

Lightweight Go server for Kubernetes with graceful shutdown and health probes. ~15MB Alpine image.

## Quick Start

## Deployment

`k8s.yaml` includes 4 replicas, rolling updates, health probes, and resource limits.

```bash
kubectl apply -f k8s.yaml
kubectl rollout status deployment/k8s-go-deployment
```

## Tutorial

**[Learn how to build this from scratch](https://yinebebtariku.com/post/k8s-go-app/)**