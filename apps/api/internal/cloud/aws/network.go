package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// network is where a machine launches: subnets to try in turn, and the
// security groups to attach.
type network struct {
	subnets        []string
	securityGroups []string
}

// network resolves a machine's network. With no overrides it is the
// region's default VPC, every default subnet (one per zone, so a capacity
// shortage in one zone is survived), and an egress-only security group
// the control plane creates once per VPC.
func (p *Provider) network(ctx context.Context, client EC2, scope string, m cloud.Machine) (network, error) {
	key := scope + ":" + m.SubnetID + ":" + strings.Join(m.SecurityGroupIDs, ",")
	if net, ok := p.cacheGetNetwork(key); ok {
		return net, nil
	}

	var subnets []ec2types.Subnet
	in := &ec2.DescribeSubnetsInput{}
	if m.SubnetID != "" {
		in.SubnetIds = []string{m.SubnetID}
	} else {
		in.Filters = []ec2types.Filter{{Name: awssdk.String("default-for-az"), Values: []string{"true"}}}
	}
	out, err := client.DescribeSubnets(ctx, in)
	if err != nil {
		return network{}, fmt.Errorf("describe subnets: %w", err)
	}
	subnets = out.Subnets
	if len(subnets) == 0 {
		if m.SubnetID != "" {
			return network{}, fmt.Errorf("subnet %s not found", m.SubnetID)
		}

		return network{}, errors.New("this region has no default VPC; create one (aws ec2 create-default-vpc) or set SubnetId in extra_config")
	}
	slices.SortFunc(subnets, func(a, b ec2types.Subnet) int {
		return strings.Compare(awssdk.ToString(a.AvailabilityZone), awssdk.ToString(b.AvailabilityZone))
	})

	net := network{securityGroups: m.SecurityGroupIDs}
	for _, subnet := range subnets {
		net.subnets = append(net.subnets, awssdk.ToString(subnet.SubnetId))
	}
	if len(net.securityGroups) == 0 {
		group, err := p.ensureSecurityGroup(ctx, client, awssdk.ToString(subnets[0].VpcId))
		if err != nil {
			return network{}, err
		}
		net.securityGroups = []string{group}
	}
	p.cachePutNetwork(key, net)

	return net, nil
}

// ensureSecurityGroup finds or creates the runners' security group in a
// VPC. It allows no inbound traffic; the default egress rule stays.
func (p *Provider) ensureSecurityGroup(ctx context.Context, client EC2, vpcID string) (string, error) {
	group, err := p.findOrCreateSecurityGroup(ctx, client, vpcID)
	if errorCode(err) == "InvalidGroup.Duplicate" {
		// Another launch created it a moment ago; one more look finds it.
		group, err = p.findOrCreateSecurityGroup(ctx, client, vpcID)
	}
	if err != nil {
		return "", err
	}

	return group, nil
}

func (p *Provider) findOrCreateSecurityGroup(ctx context.Context, client EC2, vpcID string) (string, error) {
	existing, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("group-name"), Values: []string{SecurityGroupName}},
			{Name: awssdk.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe security groups: %w", err)
	}
	if len(existing.SecurityGroups) > 0 {
		return awssdk.ToString(existing.SecurityGroups[0].GroupId), nil
	}
	created, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   awssdk.String(SecurityGroupName),
		Description: awssdk.String("Airship GitHub Actions runners: no inbound traffic"),
		VpcId:       awssdk.String(vpcID),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeSecurityGroup,
			Tags:         []ec2types.Tag{{Key: awssdk.String("Name"), Value: awssdk.String(SecurityGroupName)}},
		}},
	})
	if err != nil {
		return "", fmt.Errorf("create security group: %w", err)
	}

	return awssdk.ToString(created.GroupId), nil
}
