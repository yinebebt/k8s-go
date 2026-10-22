// Command fargate deploys the k8s-go image as one Fargate service: tasks in
// private subnets with no public IP, an internet-facing ALB that only admits
// allowedCidr, and API_TOKEN injected from SSM Parameter Store.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cloudwatch"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ecs"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lb"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ssm"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/vpc"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

const (
	container = "k8s-go"
	appPort   = 8080
)

type settings struct {
	region       string
	image        string
	allowedCIDR  string
	desiredCount int
	apiToken     pulumi.StringOutput
}

func loadSettings(ctx *pulumi.Context) settings {
	cfg := config.New(ctx, "")
	s := settings{
		region:       cfg.Get("region"),
		image:        cfg.Get("image"),
		allowedCIDR:  cfg.Require("allowedCidr"),
		desiredCount: cfg.GetInt("desiredCount"),
		apiToken:     cfg.RequireSecret("apiToken"),
	}
	if s.region == "" {
		s.region = "us-east-1"
	}
	if s.image == "" {
		s.image = "ghcr.io/yinebebt/k8s-go:v0.2"
	}
	if s.desiredCount == 0 {
		s.desiredCount = 1
	}
	return s
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		s := loadSettings(ctx)
		name := "k8s-go-" + ctx.Stack()

		provider, err := aws.NewProvider(ctx, "aws", &aws.ProviderArgs{
			Region: pulumi.String(s.region),
			DefaultTags: &aws.ProviderDefaultTagsArgs{Tags: pulumi.StringMap{
				"project": pulumi.String("k8s-go-fargate"),
				"stack":   pulumi.String(ctx.Stack()),
			}},
		})
		if err != nil {
			return err
		}
		opt := pulumi.Provider(provider)

		net, err := newNetwork(ctx, opt)
		if err != nil {
			return err
		}
		albSG, taskSG, err := securityGroups(ctx, net.vpcID, s.allowedCIDR, opt)
		if err != nil {
			return err
		}
		targets, listener, alb, err := loadBalancer(ctx, name, net, albSG, opt)
		if err != nil {
			return err
		}
		task, logGroup, err := taskDefinition(ctx, name, s, opt)
		if err != nil {
			return err
		}

		cluster, err := ecs.NewCluster(ctx, "cluster", &ecs.ClusterArgs{
			Name: pulumi.String(name),
			Settings: ecs.ClusterSettingArray{&ecs.ClusterSettingArgs{
				Name:  pulumi.String("containerInsights"),
				Value: pulumi.String("disabled"),
			}},
		}, opt)
		if err != nil {
			return err
		}
		service, err := ecs.NewService(ctx, "service", &ecs.ServiceArgs{
			Name:           pulumi.String(name),
			Cluster:        cluster.Arn,
			TaskDefinition: task.Arn,
			DesiredCount:   pulumi.Int(s.desiredCount),
			LaunchType:     pulumi.String("FARGATE"),
			NetworkConfiguration: &ecs.ServiceNetworkConfigurationArgs{
				Subnets:        net.privateSubnets,
				SecurityGroups: pulumi.StringArray{taskSG.ID().ToStringOutput()},
				AssignPublicIp: pulumi.Bool(false),
			},
			LoadBalancers: ecs.ServiceLoadBalancerArray{&ecs.ServiceLoadBalancerArgs{
				TargetGroupArn: targets.Arn,
				ContainerName:  pulumi.String(container),
				ContainerPort:  pulumi.Int(appPort),
			}},
			HealthCheckGracePeriodSeconds: pulumi.Int(10),
			DeploymentCircuitBreaker: &ecs.ServiceDeploymentCircuitBreakerArgs{
				Enable:   pulumi.Bool(true),
				Rollback: pulumi.Bool(true),
			},
			EnableEcsManagedTags: pulumi.Bool(true),
			PropagateTags:        pulumi.String("SERVICE"),
			WaitForSteadyState:   pulumi.Bool(true),
		}, opt, pulumi.DependsOn([]pulumi.Resource{listener}))
		if err != nil {
			return err
		}

		ctx.Export("url", pulumi.Sprintf("http://%s", alb.DnsName))
		ctx.Export("cluster", cluster.Name)
		ctx.Export("service", service.Name)
		ctx.Export("logGroup", logGroup.Name)
		return nil
	})
}

// securityGroups admits allowedCIDR to the ALB on :80, and only the ALB to the
// task on appPort. The task may only open HTTPS connections outward, which
// covers the image pull, SSM, and CloudWatch Logs.
func securityGroups(ctx *pulumi.Context, vpcID pulumi.StringOutput, allowedCIDR string, opt pulumi.ResourceOption) (*ec2.SecurityGroup, *ec2.SecurityGroup, error) {
	albSG, err := ec2.NewSecurityGroup(ctx, "alb", &ec2.SecurityGroupArgs{VpcId: vpcID, Description: pulumi.String("k8s-go ALB")}, opt)
	if err != nil {
		return nil, nil, err
	}
	taskSG, err := ec2.NewSecurityGroup(ctx, "task", &ec2.SecurityGroupArgs{VpcId: vpcID, Description: pulumi.String("k8s-go task")}, opt)
	if err != nil {
		return nil, nil, err
	}

	if _, err := vpc.NewSecurityGroupIngressRule(ctx, "alb-in-http", &vpc.SecurityGroupIngressRuleArgs{
		SecurityGroupId: albSG.ID(),
		IpProtocol:      pulumi.String("tcp"),
		FromPort:        pulumi.Int(80),
		ToPort:          pulumi.Int(80),
		CidrIpv4:        pulumi.String(allowedCIDR),
	}, opt); err != nil {
		return nil, nil, err
	}
	if _, err := vpc.NewSecurityGroupEgressRule(ctx, "alb-out-task", &vpc.SecurityGroupEgressRuleArgs{
		SecurityGroupId:           albSG.ID(),
		IpProtocol:                pulumi.String("tcp"),
		FromPort:                  pulumi.Int(appPort),
		ToPort:                    pulumi.Int(appPort),
		ReferencedSecurityGroupId: taskSG.ID(),
	}, opt); err != nil {
		return nil, nil, err
	}
	if _, err := vpc.NewSecurityGroupIngressRule(ctx, "task-in-alb", &vpc.SecurityGroupIngressRuleArgs{
		SecurityGroupId:           taskSG.ID(),
		IpProtocol:                pulumi.String("tcp"),
		FromPort:                  pulumi.Int(appPort),
		ToPort:                    pulumi.Int(appPort),
		ReferencedSecurityGroupId: albSG.ID(),
	}, opt); err != nil {
		return nil, nil, err
	}
	if _, err := vpc.NewSecurityGroupEgressRule(ctx, "task-out-https", &vpc.SecurityGroupEgressRuleArgs{
		SecurityGroupId: taskSG.ID(),
		IpProtocol:      pulumi.String("tcp"),
		FromPort:        pulumi.Int(443),
		ToPort:          pulumi.Int(443),
		CidrIpv4:        pulumi.String("0.0.0.0/0"),
	}, opt); err != nil {
		return nil, nil, err
	}
	return albSG, taskSG, nil
}

// loadBalancer routes :80 to the tasks. The target group checks /readyz, so a
// draining task leaves rotation before it stops; the short deregistration
// delay matches the app's 2s drain.
func loadBalancer(ctx *pulumi.Context, name string, net *network, albSG *ec2.SecurityGroup, opt pulumi.ResourceOption) (*lb.TargetGroup, *lb.Listener, *lb.LoadBalancer, error) {
	alb, err := lb.NewLoadBalancer(ctx, "alb", &lb.LoadBalancerArgs{
		Name:                    pulumi.String(name),
		LoadBalancerType:        pulumi.String("application"),
		Internal:                pulumi.Bool(false),
		SecurityGroups:          pulumi.StringArray{albSG.ID().ToStringOutput()},
		Subnets:                 net.publicSubnets,
		DropInvalidHeaderFields: pulumi.Bool(true),
	}, opt)
	if err != nil {
		return nil, nil, nil, err
	}
	targets, err := lb.NewTargetGroup(ctx, "tasks", &lb.TargetGroupArgs{
		Name:                pulumi.String(name),
		Port:                pulumi.Int(appPort),
		Protocol:            pulumi.String("HTTP"),
		TargetType:          pulumi.String("ip"),
		VpcId:               net.vpcID,
		DeregistrationDelay: pulumi.Int(10),
		HealthCheck: &lb.TargetGroupHealthCheckArgs{
			Path:               pulumi.String("/readyz"),
			Matcher:            pulumi.String("200"),
			Interval:           pulumi.Int(10),
			Timeout:            pulumi.Int(5),
			HealthyThreshold:   pulumi.Int(2),
			UnhealthyThreshold: pulumi.Int(2),
		},
	}, opt)
	if err != nil {
		return nil, nil, nil, err
	}
	listener, err := lb.NewListener(ctx, "http", &lb.ListenerArgs{
		LoadBalancerArn: alb.Arn,
		Port:            pulumi.Int(80),
		Protocol:        pulumi.String("HTTP"),
		DefaultActions: lb.ListenerDefaultActionArray{&lb.ListenerDefaultActionArgs{
			Type:           pulumi.String("forward"),
			TargetGroupArn: targets.Arn,
		}},
	}, opt)
	if err != nil {
		return nil, nil, nil, err
	}
	return targets, listener, alb, nil
}

// taskDefinition mirrors the Kubernetes hardening: non-root user, read-only
// root filesystem, all capabilities dropped. ECS restarts the container when
// /livez fails. The task has no task role because the app calls no AWS APIs.
func taskDefinition(ctx *pulumi.Context, name string, s settings, opt pulumi.ResourceOption) (*ecs.TaskDefinition, *cloudwatch.LogGroup, error) {
	token, err := ssm.NewParameter(ctx, "api-token", &ssm.ParameterArgs{
		Name:  pulumi.Sprintf("/%s/api-token", name),
		Type:  pulumi.String("SecureString"),
		Value: s.apiToken,
	}, opt)
	if err != nil {
		return nil, nil, err
	}
	logGroup, err := cloudwatch.NewLogGroup(ctx, "logs", &cloudwatch.LogGroupArgs{
		Name:            pulumi.Sprintf("/ecs/%s", name),
		RetentionInDays: pulumi.Int(1),
	}, opt)
	if err != nil {
		return nil, nil, err
	}

	execRole, err := iam.NewRole(ctx, "execution", &iam.RoleArgs{
		AssumeRolePolicy: pulumi.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ecs-tasks.amazonaws.com"},"Action":"sts:AssumeRole"}]}`),
	}, opt)
	if err != nil {
		return nil, nil, err
	}
	if _, err := iam.NewRolePolicyAttachment(ctx, "execution", &iam.RolePolicyAttachmentArgs{
		Role:      execRole.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"),
	}, opt); err != nil {
		return nil, nil, err
	}
	readToken, err := iam.NewRolePolicy(ctx, "read-api-token", &iam.RolePolicyArgs{
		Role: execRole.ID(),
		Policy: token.Arn.ApplyT(func(arn string) (string, error) {
			return marshal(map[string]any{
				"Version": "2012-10-17",
				"Statement": []map[string]any{{
					"Effect":   "Allow",
					"Action":   "ssm:GetParameters",
					"Resource": arn,
				}},
			})
		}).(pulumi.StringOutput),
	}, opt)
	if err != nil {
		return nil, nil, err
	}

	containers := pulumi.All(token.Arn, logGroup.Name).ApplyT(func(args []any) (string, error) {
		return marshal([]map[string]any{{
			"name":                   container,
			"image":                  s.image,
			"essential":              true,
			"user":                   "65532:65532",
			"readonlyRootFilesystem": true,
			"linuxParameters":        map[string]any{"capabilities": map[string]any{"drop": []string{"ALL"}}},
			"portMappings":           []map[string]any{{"containerPort": appPort, "protocol": "tcp"}},
			"environment": []map[string]string{
				{"name": "PORT", "value": fmt.Sprint(appPort)},
				{"name": "LOG_LEVEL", "value": "info"},
			},
			"secrets": []map[string]string{{"name": "API_TOKEN", "valueFrom": args[0].(string)}},
			"healthCheck": map[string]any{
				"command":     []string{"CMD-SHELL", fmt.Sprintf("curl -fsS http://localhost:%d/livez || exit 1", appPort)},
				"interval":    10,
				"timeout":     3,
				"retries":     3,
				"startPeriod": 5,
			},
			"stopTimeout": 10,
			"logConfiguration": map[string]any{
				"logDriver": "awslogs",
				"options": map[string]string{
					"awslogs-group":         args[1].(string),
					"awslogs-region":        s.region,
					"awslogs-stream-prefix": container,
				},
			},
		}})
	}).(pulumi.StringOutput)

	task, err := ecs.NewTaskDefinition(ctx, "task", &ecs.TaskDefinitionArgs{
		Family:                  pulumi.String(name),
		Cpu:                     pulumi.String("256"),
		Memory:                  pulumi.String("512"),
		NetworkMode:             pulumi.String("awsvpc"),
		RequiresCompatibilities: pulumi.StringArray{pulumi.String("FARGATE")},
		ExecutionRoleArn:        execRole.Arn,
		RuntimePlatform: &ecs.TaskDefinitionRuntimePlatformArgs{
			OperatingSystemFamily: pulumi.String("LINUX"),
			CpuArchitecture:       pulumi.String("X86_64"),
		},
		ContainerDefinitions: containers,
	}, opt, pulumi.DependsOn([]pulumi.Resource{readToken}))
	if err != nil {
		return nil, nil, err
	}
	return task, logGroup, nil
}

func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
