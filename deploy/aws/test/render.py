"""Deploys data-plane.yaml into moto's CloudFormation and writes out the
agent's IAM policy with every intrinsic resolved, for policy.test.mjs.

Usage: python render.py data-plane.yaml out.json
"""

import ast
import json
import sys
import time

import boto3
import yaml
from moto.server import ThreadedMotoServer

DATA_PLANE_ID = "0199d1a8-6f2b-7c1e-9a3b-2f6c1d2e3f40"
RUNNER_ROLE_ARN = "arn:aws:iam::123456789012:role/runner"


def main(template_path: str, out_path: str) -> None:
    server = ThreadedMotoServer(port=0, verbose=False)
    server.start()
    try:
        host, port = server.get_host_and_port()
        render(open(template_path).read(), f"http://{host}:{port}", out_path)
    finally:
        server.stop()


class _CfnLoader(yaml.SafeLoader):
    pass


# Keep CloudFormation's short-form intrinsics as {"Fn": value} nodes.
def _intrinsic(loader: yaml.SafeLoader, suffix: str, node: yaml.Node) -> dict:
    name = "Ref" if suffix == "Ref" else f"Fn::{suffix}"
    if isinstance(node, yaml.ScalarNode):
        return {name: loader.construct_scalar(node)}
    if isinstance(node, yaml.SequenceNode):
        return {name: loader.construct_sequence(node, deep=True)}
    return {name: loader.construct_mapping(node, deep=True)}


_CfnLoader.add_multi_constructor("!", _intrinsic)


def _resolve_refs(value, params: dict, has: set = frozenset(), attrs: dict | None = None):
    """Resolves Ref, Fn::If (on the conditions in has) and Fn::GetAtt (from
    attrs, "Resource.Attribute" -> value)."""
    if isinstance(value, dict):
        if set(value) == {"Ref"}:
            return params[value["Ref"]]
        if set(value) == {"Fn::If"}:
            cond, yes, no = value["Fn::If"]
            return _resolve_refs(yes if cond in has else no, params, has, attrs)
        if set(value) == {"Fn::GetAtt"}:
            ref = value["Fn::GetAtt"]
            return (attrs or {})[ref if isinstance(ref, str) else ".".join(ref)]
        return {k: _resolve_refs(v, params, has, attrs) for k, v in value.items()}
    if isinstance(value, list):
        return [_resolve_refs(v, params, has, attrs) for v in value]
    return value


def _role_policies_from_template(template: str, params: dict, has: set, attrs: dict) -> dict:
    """moto does not create AWS::IAM::RolePolicy resources, so read the ones
    whose condition holds straight from the template."""
    doc = yaml.load(template, Loader=_CfnLoader)  # noqa: S506 - safe loader subclass
    out = {}
    for resource in doc["Resources"].values():
        if resource["Type"] != "AWS::IAM::RolePolicy" or resource.get("Condition") not in has:
            continue
        props = resource["Properties"]
        out[props["PolicyName"]] = _resolve_refs(props["PolicyDocument"], params, has, attrs)
    return out


def _bucket_policy_from_template(template: str, params: dict, has: set, attrs: dict) -> dict:
    doc = yaml.load(template, Loader=_CfnLoader)  # noqa: S506 - safe loader subclass
    props = doc["Resources"]["CacheBucketPolicy"]["Properties"]
    return _sub(_resolve_refs(props["PolicyDocument"], params, has, attrs), attrs)


def _sub(value, attrs: dict):
    """Resolves the Fn::Sub strings the bucket policy uses (${X.Arn})."""
    if isinstance(value, dict):
        if set(value) == {"Fn::Sub"}:
            out = value["Fn::Sub"]
            for key, resolved in attrs.items():
                out = out.replace("${" + key + "}", resolved)
            return out
        return {k: _sub(v, attrs) for k, v in value.items()}
    if isinstance(value, list):
        return [_sub(v, attrs) for v in value]
    return value


def render(template: str, endpoint: str, out_path: str) -> None:
    kw = dict(endpoint_url=endpoint, region_name="us-east-1", aws_access_key_id="test", aws_secret_access_key="test")  # noqa: S106 - moto dummy
    ec2, cfn, iam = (boto3.client(s, **kw) for s in ("ec2", "cloudformation", "iam"))
    vpc = ec2.create_vpc(CidrBlock="10.20.0.0/16")["Vpc"]["VpcId"]
    subnets = [ec2.create_subnet(VpcId=vpc, CidrBlock=f"10.20.{i}.0/24")["Subnet"]["SubnetId"] for i in (1, 2)]

    def deploy(name: str, extra: dict) -> dict:
        params = {
            "ControlPlaneUrl": "https://api.airship.example",
            "DataPlaneId": DATA_PLANE_ID,
            "AgentToken": "asd_abc123",
            "VpcId": vpc,
            "SubnetIds": ",".join(subnets),
            **extra,
        }
        cfn.create_stack(
            StackName=name,
            TemplateBody=template,
            Capabilities=["CAPABILITY_IAM"],
            Parameters=[{"ParameterKey": k, "ParameterValue": v} for k, v in params.items()],
        )
        for _ in range(120):
            stack = cfn.describe_stacks(StackName=name)["Stacks"][0]
            if not stack["StackStatus"].endswith("IN_PROGRESS"):
                break
            time.sleep(0.25)
        if stack["StackStatus"] != "CREATE_COMPLETE":
            raise SystemExit(f"{name}: {stack['StackStatus']} {stack.get('StackStatusReason', '')}")
        resources = {r["LogicalResourceId"]: r for r in cfn.list_stack_resources(StackName=name)["StackResourceSummaries"]}
        role = resources["AgentRole"]["PhysicalResourceId"]
        policies = {}
        for policy_name in iam.list_role_policies(RoleName=role)["PolicyNames"]:
            doc = iam.get_role_policy(RoleName=role, PolicyName=policy_name)["PolicyDocument"]
            # moto returns inline policies as Python reprs, not JSON.
            policies[policy_name] = ast.literal_eval(doc) if isinstance(doc, str) else doc
        # Intrinsics resolved here see the template's defaults too.
        defaults = yaml.load(template, Loader=_CfnLoader)["Parameters"]  # noqa: S506
        params = {k: str(v["Default"]) for k, v in defaults.items() if "Default" in v} | params
        conditions = set()
        if params.get("RunnerRoleArn"):
            conditions.add("HasRunnerRole")
        if params.get("CacheBucket") == "Enabled":
            conditions.add("CreateCache")
            if "HasRunnerRole" not in conditions:
                conditions.add("CreateCacheRole")
        if conditions & {"HasRunnerRole", "CreateCacheRole"}:
            conditions.add("RunnersHaveRole")
        attrs = {}
        if "CacheRunnerRole" in resources:
            attrs["CacheRunnerRole.Arn"] = iam.get_role(RoleName=resources["CacheRunnerRole"]["PhysicalResourceId"])["Role"]["Arn"]
        if "CacheBucketResource" in resources:
            attrs["CacheBucketResource.Arn"] = "arn:aws:s3:::" + resources["CacheBucketResource"]["PhysicalResourceId"]
        for policy_name, doc in _role_policies_from_template(template, params, conditions, attrs).items():
            policies.setdefault(policy_name, doc)
        out = {"policies": policies, "securityGroup": resources["RunnerSecurityGroup"]["PhysicalResourceId"]}
        if "CacheBucketResource" in resources:
            bucket = resources["CacheBucketResource"]["PhysicalResourceId"]
            outputs = {o["OutputKey"]: o["OutputValue"] for o in cfn.describe_stacks(StackName=name)["Stacks"][0].get("Outputs", [])}
            task = resources["TaskDefinition"]["PhysicalResourceId"]
            env = boto3.client("ecs", **kw).describe_task_definition(taskDefinition=task)["taskDefinition"]["containerDefinitions"][0]["environment"]
            out["cache"] = {
                "bucket": bucket,
                "bucketArn": f"arn:aws:s3:::{bucket}",
                "output": outputs.get("CacheBucketName"),
                # moto does not create AWS::S3::BucketPolicy either.
                "policy": _bucket_policy_from_template(
                    template, {**params, "CacheBucketResource": bucket}, conditions, attrs
                ),
                # moto's CloudFormation ignores these properties, so they
                # come from the template (cfn-lint checks their schema).
                "properties": _resolve_refs(
                    yaml.load(template, Loader=_CfnLoader)["Resources"]["CacheBucketResource"]["Properties"],  # noqa: S506
                    params,
                    conditions,
                    attrs,
                ),
                "runnerRoleArn": attrs.get("CacheRunnerRole.Arn"),
                "instanceProfile": next((e["value"] for e in env if e["name"] == "AIRSHIP_AWS_INSTANCE_PROFILE"), None),
                "instanceProfileName": resources.get("CacheRunnerInstanceProfile", {}).get("PhysicalResourceId"),
            }
        return out

    rendered = {
        "dataPlaneId": DATA_PLANE_ID,
        "runnerRoleArn": RUNNER_ROLE_ARN,
        "vpc": vpc,
        "subnets": subnets,
        "default": deploy("airship-default", {}),
        "custom": deploy(
            "airship-custom",
            {
                "ImageOwners": "111111111111,222222222222",
                "RunnerInstanceProfileName": "runner-profile",
                "RunnerRoleArn": RUNNER_ROLE_ARN,
            },
        ),
        "cache": deploy("airship-cache", {"CacheBucket": "Enabled", "CacheRetentionDays": "3"}),
        "cacheCustomRole": deploy(
            "airship-cache-custom",
            {"CacheBucket": "Enabled", "RunnerInstanceProfileName": "runner-profile", "RunnerRoleArn": RUNNER_ROLE_ARN},
        ),
    }
    with open(out_path, "w") as f:
        json.dump(rendered, f, indent=1)


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
