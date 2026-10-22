# k8s-go on Fargate

One ECS Fargate service for `ghcr.io/yinebebt/k8s-go:v0.2`, defined with Pulumi in Go.

```text
your IP ──► ALB :80 (public subnets) ──► task :8080 (private subnets, no public IP)
                 health check /readyz          container health check /livez
                                               API_TOKEN from SSM Parameter Store
                                               logs to CloudWatch, 1-day retention
                                               image pull through one NAT gateway
```

| choice | why |
| --- | --- |
| ALB admits only `allowedCidr` | `make init` sets it to your current public IP. |
| Task admits only the ALB, and only opens HTTPS outward | Image pull, SSM, and CloudWatch Logs are all HTTPS. |
| `API_TOKEN` is a Pulumi secret, stored as an SSM SecureString | It never appears in the image, the task definition, or logs. |
| Non-root user, read-only root filesystem, all capabilities dropped | Same hardening as `k8s/deployment.yaml`. |
| No task role | The app calls no AWS APIs. |
| ALB checks `/readyz`, ECS checks `/livez` | Readiness takes a task out of rotation while it drains; liveness restarts it. |

## Cost

Roughly $0.10 per hour in us-east-1 while it is up: NAT gateway, ALB, three public IPv4 addresses, and one 0.25 vCPU / 0.5 GB task. Run `make destroy` when finished.

## Use

Requires the Pulumi CLI and working AWS credentials. State and the secrets passphrase are kept in this folder and are gitignored, so no Pulumi Cloud account is needed.

```bash
make local     # run the image locally under the task's restrictions; no AWS needed
make init      # create the stack, allow your IP, generate API_TOKEN; re-run when your IP changes
make preview   # show what would be created; changes nothing
make up        # create it (asks for confirmation)
make verify    # /livez, /readyz, /hello with and without the token
make destroy   # delete everything (asks for confirmation)
make leftovers # list anything still tagged project=k8s-go-fargate
```

`make local` covers what Docker can: the non-root user, read-only root filesystem, dropped capabilities, the token check, and the `curl` health check inside the image. The VPC, ALB, IAM, and SSM wiring only shows up in `preview` and `up`. LocalStack's free tier does not emulate ECS or ELBv2.

### Image tag

Deploy a fixed tag, not `latest`. The task definition stores the image string, so a new tag means a new task definition revision: ECS does a rolling deploy, and the circuit breaker can roll back to the previous revision. With `latest`, the string never changes. `pulumi up` sees no diff, tasks that restart pull whatever `latest` is at that moment, and a rollback goes to the same moving tag. To ship a release, set the tag in config rather than editing code:

```bash
pulumi config set image ghcr.io/yinebebt/k8s-go:v0.3 && make preview
```

Tags can be re-pushed. For a stricter pin, use the digest: `ghcr.io/yinebebt/k8s-go@sha256:...`.

Settings, all optional except the two `make init` sets:

| key | default |
| --- | --- |
| `region` | `us-east-1` |
| `image` | `ghcr.io/yinebebt/k8s-go:v0.2` |
| `desiredCount` | `1` |
| `allowedCidr` | set by `make init` |
| `apiToken` | set by `make init`, secret |

`pulumi config` needs the environment the Makefile exports: `PULUMI_BACKEND_URL=file://$PWD/.state` and `PULUMI_CONFIG_PASSPHRASE_FILE=$PWD/.passphrase`.
