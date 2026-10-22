package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const zoneCount = 2 // an ALB needs subnets in at least two zones

type network struct {
	vpcID          pulumi.StringOutput
	publicSubnets  pulumi.StringArray
	privateSubnets pulumi.StringArray
}

// newNetwork builds a VPC with one public and one private subnet per zone.
// Private subnets reach the internet through a single NAT gateway, which is
// enough for a lab and is the main hourly cost.
func newNetwork(ctx *pulumi.Context, opt pulumi.ResourceOrInvokeOption) (*network, error) {
	zones, err := aws.GetAvailabilityZones(ctx, &aws.GetAvailabilityZonesArgs{State: pulumi.StringRef("available")}, opt)
	if err != nil {
		return nil, err
	}
	if len(zones.Names) < zoneCount {
		return nil, fmt.Errorf("need %d availability zones, found %d", zoneCount, len(zones.Names))
	}

	vpc, err := ec2.NewVpc(ctx, "vpc", &ec2.VpcArgs{
		CidrBlock:          pulumi.String("10.0.0.0/16"),
		EnableDnsSupport:   pulumi.Bool(true),
		EnableDnsHostnames: pulumi.Bool(true),
	}, opt)
	if err != nil {
		return nil, err
	}
	igw, err := ec2.NewInternetGateway(ctx, "igw", &ec2.InternetGatewayArgs{VpcId: vpc.ID()}, opt)
	if err != nil {
		return nil, err
	}

	publicRoutes, err := ec2.NewRouteTable(ctx, "public", &ec2.RouteTableArgs{
		VpcId:  vpc.ID(),
		Routes: ec2.RouteTableRouteArray{&ec2.RouteTableRouteArgs{CidrBlock: pulumi.String("0.0.0.0/0"), GatewayId: igw.ID()}},
	}, opt)
	if err != nil {
		return nil, err
	}

	net := &network{vpcID: vpc.ID().ToStringOutput()}
	var publicSubnets []*ec2.Subnet
	for i := range zoneCount {
		pub, err := subnet(ctx, fmt.Sprintf("public-%d", i), vpc, fmt.Sprintf("10.0.%d.0/24", i), zones.Names[i], publicRoutes, opt)
		if err != nil {
			return nil, err
		}
		publicSubnets = append(publicSubnets, pub)
		net.publicSubnets = append(net.publicSubnets, pub.ID().ToStringOutput())
	}

	eip, err := ec2.NewEip(ctx, "nat", &ec2.EipArgs{Domain: pulumi.String("vpc")}, opt)
	if err != nil {
		return nil, err
	}
	nat, err := ec2.NewNatGateway(ctx, "nat", &ec2.NatGatewayArgs{
		AllocationId: eip.ID(),
		SubnetId:     publicSubnets[0].ID(),
	}, opt, pulumi.DependsOn([]pulumi.Resource{igw}))
	if err != nil {
		return nil, err
	}
	privateRoutes, err := ec2.NewRouteTable(ctx, "private", &ec2.RouteTableArgs{
		VpcId:  vpc.ID(),
		Routes: ec2.RouteTableRouteArray{&ec2.RouteTableRouteArgs{CidrBlock: pulumi.String("0.0.0.0/0"), NatGatewayId: nat.ID()}},
	}, opt)
	if err != nil {
		return nil, err
	}
	for i := range zoneCount {
		priv, err := subnet(ctx, fmt.Sprintf("private-%d", i), vpc, fmt.Sprintf("10.0.%d.0/24", 10+i), zones.Names[i], privateRoutes, opt)
		if err != nil {
			return nil, err
		}
		net.privateSubnets = append(net.privateSubnets, priv.ID().ToStringOutput())
	}
	return net, nil
}

func subnet(ctx *pulumi.Context, name string, vpc *ec2.Vpc, cidr, zone string, routes *ec2.RouteTable, opt pulumi.ResourceOrInvokeOption) (*ec2.Subnet, error) {
	s, err := ec2.NewSubnet(ctx, name, &ec2.SubnetArgs{
		VpcId:            vpc.ID(),
		CidrBlock:        pulumi.String(cidr),
		AvailabilityZone: pulumi.String(zone),
	}, opt)
	if err != nil {
		return nil, err
	}
	_, err = ec2.NewRouteTableAssociation(ctx, name, &ec2.RouteTableAssociationArgs{
		SubnetId:     s.ID(),
		RouteTableId: routes.ID(),
	}, opt)
	return s, err
}
