STACK ?= dev
# Must match the default in main.go; `pulumi config set image ...` overrides both.
DEFAULT_IMAGE := ghcr.io/yinebebt/k8s-go:v0.2
LOCAL_PORT ?= 18080

# State and the secrets passphrase stay in this folder; no Pulumi Cloud account.
export PULUMI_BACKEND_URL := file://$(CURDIR)/.state
export PULUMI_CONFIG_PASSPHRASE_FILE := $(CURDIR)/.passphrase
export PULUMI_SKIP_UPDATE_CHECK := true

IMAGE = $$(pulumi config get image --stack $(STACK) 2>/dev/null || echo $(DEFAULT_IMAGE))
REGION = $$(pulumi config get region --stack $(STACK) 2>/dev/null || echo us-east-1)

# $(call probe,<base url>,<token>): health endpoints, then /hello without and with the token.
define probe
for path in /livez /readyz; do printf '%s ' $$path; curl -fsS $(1)$$path; echo; done; \
printf '/hello without token: '; curl -s -o /dev/null -w '%{http_code}\n' $(1)/hello; \
printf '/hello with token: '; curl -fsS -H "Authorization: Bearer $(2)" $(1)/hello; echo
endef

.PHONY: init local preview up verify destroy leftovers

# Creates the stack, limits the ALB to your current public IP, and generates
# API_TOKEN without printing it. Re-run when your public IP changes.
init:
	mkdir -p .state
	test -f .passphrase || (umask 077 && openssl rand -base64 32 > .passphrase)
	pulumi stack select --create $(STACK)
	pulumi config set allowedCidr "$$(curl -fsS https://checkip.amazonaws.com | tr -d '[:space:]')/32"
	pulumi config get apiToken >/dev/null 2>&1 || pulumi config set --secret apiToken "$$(openssl rand -hex 32)"

# Runs the image the stack would deploy under the task's restrictions (user,
# read-only root, no capabilities) and probes it. No AWS involved.
local:
	@docker run -d --rm --name k8s-go-local -p $(LOCAL_PORT):8080 --platform linux/amd64 \
		--user 65532:65532 --read-only --cap-drop ALL \
		-e PORT=8080 -e LOG_LEVEL=info -e API_TOKEN=local-token $(IMAGE) >/dev/null; \
	trap 'docker stop k8s-go-local >/dev/null' EXIT; \
	for i in $$(seq 30); do curl -fsS localhost:$(LOCAL_PORT)/livez >/dev/null 2>&1 && break; sleep 1; done; \
	$(call probe,http://localhost:$(LOCAL_PORT),local-token); \
	printf 'task health check: '; docker exec k8s-go-local curl -fsS http://localhost:8080/livez; echo

preview:
	pulumi preview --stack $(STACK)

up:
	pulumi up --stack $(STACK)

verify:
	@url=$$(pulumi stack output url --stack $(STACK)); token=$$(pulumi config get apiToken --stack $(STACK)); \
	$(call probe,$$url,$$token)

destroy:
	pulumi destroy --stack $(STACK)

# Anything still tagged for this project after destroy. Deleted resources can
# linger in this list for a few minutes.
leftovers:
	aws resourcegroupstaggingapi get-resources --region $(REGION) \
		--tag-filters Key=project,Values=k8s-go-fargate \
		--query 'ResourceTagMappingList[].ResourceARN' --output text
