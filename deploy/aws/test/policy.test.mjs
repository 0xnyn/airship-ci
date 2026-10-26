// Evaluates every IAM authorization AWS performs for the agent's calls
// against the policy that CloudFormation (moto, via render.py) produced from
// data-plane.yaml. The resources and condition keys per action follow the
// Service Authorization Reference, which iam-simulate validates requests
// against. Run with `pnpm --filter @airship/deploy-aws test:policy`.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import {
  runSimulation,
  runUnsafeSimulation,
} from "@cloud-copilot/iam-simulate";

const rendered = JSON.parse(
  readFileSync(new URL("../.rendered.json", import.meta.url), "utf8")
);

const ACCOUNT = "123456789012";
const REGION = "us-east-1";
const CANONICAL = "099720109477";
const CUSTOM_OWNER = "111111111111";
const DATA_PLANE = rendered.dataPlaneId;
const OTHER_DATA_PLANE = "0199d1a8-0000-7000-8000-000000000999";
const PRINCIPAL = `arn:aws:sts::${ACCOUNT}:assumed-role/airship-agent/task`;
const CANONICAL_PARAMETER =
  "/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id";

const ec2Arn = (type, id) => `arn:aws:ec2:${REGION}:${ACCOUNT}:${type}/${id}`;
const imageArn = `arn:aws:ec2:${REGION}::image/ami-0abc`;
const vpcArn = (id) => `arn:aws:ec2:${REGION}:${ACCOUNT}:vpc/${id}`;
const roleArn = (name) => `arn:aws:iam::${ACCOUNT}:role/${name}`;

function stack(name) {
  const s = rendered[name];
  return {
    policies: Object.entries(s.policies).map(([policyName, policy]) => ({
      name: policyName,
      policy,
    })),
    securityGroup: s.securityGroup,
    subnet: rendered.subnets[0],
    vpc: vpcArn(rendered.vpc),
  };
}

// The tags the agent puts on every instance and volume it launches.
function requestTags(dataPlane) {
  if (dataPlane === null) {
    return {};
  }
  return {
    "aws:RequestTag/airship:data-plane": dataPlane,
    "aws:RequestTag/airship:profile": "airship-4cpu",
    "aws:RequestTag/airship:runner": "0199d1a8-6f2b-7c1e-9a3b-2f6c1d2e3f40",
    "aws:RequestTag/Name": "airship-abc",
    "aws:TagKeys": [
      "airship:data-plane",
      "airship:runner",
      "Name",
      "airship:profile",
    ],
  };
}

// One RunInstances call is authorized once per resource it touches.
function runInstances(s, overrides = {}) {
  const {
    dataPlane = DATA_PLANE,
    owner = CANONICAL,
    securityGroup = s.securityGroup,
    spot = false,
    vpc = s.vpc,
  } = overrides;
  const tags = requestTags(dataPlane);
  const checks = [
    [
      "ec2:RunInstances",
      imageArn,
      {
        "ec2:ImageType": "machine",
        "ec2:Owner": owner,
        "ec2:Public": "true",
        "ec2:RootDeviceType": "ebs",
      },
    ],
    [
      "ec2:RunInstances",
      ec2Arn("instance", "*"),
      {
        ...tags,
        "ec2:InstanceMarketType": spot ? "spot" : "on-demand",
        "ec2:InstanceType": "c7i.xlarge",
        "ec2:MetadataHttpPutResponseHopLimit": "1",
        "ec2:MetadataHttpTokens": "required",
        "ec2:RootDeviceType": "ebs",
        "ec2:Tenancy": "default",
      },
    ],
    [
      "ec2:RunInstances",
      ec2Arn("volume", "*"),
      {
        ...tags,
        "ec2:Encrypted": "true",
        "ec2:VolumeSize": "100",
        "ec2:VolumeType": "gp3",
      },
    ],
    [
      "ec2:RunInstances",
      ec2Arn("network-interface", "*"),
      { "ec2:Subnet": ec2Arn("subnet", s.subnet), "ec2:Vpc": vpc },
    ],
    ["ec2:RunInstances", ec2Arn("subnet", s.subnet), { "ec2:Vpc": vpc }],
    [
      "ec2:RunInstances",
      ec2Arn("security-group", securityGroup),
      { "ec2:Vpc": vpc },
    ],
    [
      "ec2:CreateTags",
      ec2Arn("instance", "*"),
      { ...tags, "ec2:CreateAction": "RunInstances" },
    ],
    [
      "ec2:CreateTags",
      ec2Arn("volume", "*"),
      { ...tags, "ec2:CreateAction": "RunInstances" },
    ],
  ];
  if (spot) {
    checks.push([
      "ec2:RunInstances",
      ec2Arn("spot-instances-request", "*"),
      {},
    ]);
  }
  return checks;
}

async function isAllowed(s, [action, resource, context]) {
  const simulation = {
    identityPolicies: s.policies,
    request: {
      action,
      contextVariables: { "ec2:Region": REGION, ...context },
      principal: PRINCIPAL,
      resource: { accountId: ACCOUNT, resource },
    },
    resourceControlPolicies: [],
    serviceControlPolicies: [],
  };
  // Public SSM parameters have no account in their ARN, which the
  // simulator's ARN templates reject, so only they skip validation.
  if (resource.startsWith(`arn:aws:ssm:${REGION}::`)) {
    return (await runUnsafeSimulation(simulation, {})) === "Allowed";
  }
  const result = await runSimulation(simulation, {});
  assert.notEqual(
    result.resultType,
    "error",
    `${action} ${resource}: ${JSON.stringify(result.errors)}`
  );
  return result.overallResult === "Allowed";
}

async function denied(s, checks) {
  const results = await Promise.all(checks.map((check) => isAllowed(s, check)));
  return checks
    .filter((_, i) => !results[i])
    .map(([action, resource]) => `${action} ${resource}`);
}

async function assertAllowed(s, checks) {
  assert.deepEqual(await denied(s, checks), []);
}

// Denied, and only by the resources the guard is meant to stop, so a
// request refused for some unrelated reason does not pass as a guard.
async function assertDeniedBy(s, checks, guards) {
  const refused = await denied(s, checks);
  assert.ok(refused.length > 0, "request was allowed");
  for (const r of refused) {
    assert.ok(
      guards.some((g) => r.includes(g)),
      `denied by ${r}, expected one of ${guards.join(", ")}`
    );
  }
}

const tagged = (dataPlane) => ({
  "aws:ResourceTag/airship:data-plane": dataPlane,
});
const terminate = (dataPlane) => [
  [
    "ec2:TerminateInstances",
    ec2Arn("instance", "i-1"),
    dataPlane ? tagged(dataPlane) : {},
  ],
];
const passRole = (name, service) => [
  ["iam:PassRole", roleArn(name), { "iam:PassedToService": service }],
];

for (const [name, owner] of [
  ["default", CANONICAL],
  ["custom", CUSTOM_OWNER],
]) {
  const s = stack(name);

  test(`${name}: launches on-demand and spot runners`, async () => {
    await assertAllowed(s, runInstances(s, { owner }));
    await assertAllowed(s, runInstances(s, { owner, spot: true }));
  });

  test(`${name}: lists, describes and terminates its own runners`, async () => {
    await assertAllowed(s, [
      ["ec2:DescribeInstances", "*", {}],
      ["ec2:DescribeImages", "*", {}],
      ...terminate(DATA_PLANE),
    ]);
  });

  test(`${name}: creates the spot service-linked role once`, async () => {
    await assertAllowed(s, [
      [
        "iam:CreateServiceLinkedRole",
        roleArn("aws-service-role/spot.amazonaws.com/AWSServiceRoleForEC2Spot"),
        { "iam:AWSServiceName": "spot.amazonaws.com" },
      ],
    ]);
  });

  test(`${name}: resolves public AMI parameters but no others`, async () => {
    await assertAllowed(s, [
      [
        "ssm:GetParameter",
        `arn:aws:ssm:${REGION}::parameter${CANONICAL_PARAMETER}`,
        {},
      ],
    ]);
    await assertDeniedBy(
      s,
      [
        [
          "ssm:GetParameter",
          `arn:aws:ssm:${REGION}:${ACCOUNT}:parameter/prod/db-password`,
          {},
        ],
      ],
      ["parameter/prod"]
    );
  });

  test(`${name}: cannot launch untagged or for another data plane`, async () => {
    const guards = ["instance/*", "volume/*"];
    await assertDeniedBy(
      s,
      runInstances(s, { dataPlane: null, owner }),
      guards
    );
    await assertDeniedBy(
      s,
      runInstances(s, { dataPlane: OTHER_DATA_PLANE, owner }),
      guards
    );
  });

  test(`${name}: cannot launch outside its VPC or security group`, async () => {
    await assertDeniedBy(
      s,
      runInstances(s, { owner, vpc: vpcArn("vpc-other") }),
      ["network-interface", "subnet", "security-group"]
    );
    await assertDeniedBy(
      s,
      runInstances(s, { owner, securityGroup: "sg-0admin" }),
      ["security-group/sg-0admin"]
    );
  });

  test(`${name}: cannot boot an AMI from an untrusted owner`, async () => {
    await assertDeniedBy(s, runInstances(s, { owner: "666666666666" }), [
      "image/",
    ]);
  });

  test(`${name}: cannot touch instances it did not launch`, async () => {
    await assertDeniedBy(s, terminate(OTHER_DATA_PLANE), ["instance/"]);
    await assertDeniedBy(s, terminate(null), ["instance/"]);
    await assertDeniedBy(
      s,
      [
        [
          "ec2:CreateTags",
          ec2Arn("instance", "i-1"),
          { ...requestTags(DATA_PLANE), "ec2:CreateAction": "CreateTags" },
        ],
      ],
      ["instance/"]
    );
    await assertDeniedBy(
      s,
      [
        ["ec2:StopInstances", ec2Arn("instance", "i-1"), tagged(DATA_PLANE)],
        [
          "ec2:ModifyInstanceAttribute",
          ec2Arn("instance", "i-1"),
          tagged(DATA_PLANE),
        ],
      ],
      ["instance/"]
    );
  });

  test(`${name}: cannot pass arbitrary roles`, async () => {
    await assertDeniedBy(s, passRole("admin", "ec2.amazonaws.com"), [
      "role/admin",
    ]);
  });
}

test("default: no role can be passed without an instance profile", async () => {
  await assertDeniedBy(
    stack("default"),
    passRole("runner", "ec2.amazonaws.com"),
    ["role/runner"]
  );
});

test("custom: passes only the runner role, only to EC2", async () => {
  const s = stack("custom");
  assert.equal(rendered.runnerRoleArn, roleArn("runner"));
  await assertAllowed(s, passRole("runner", "ec2.amazonaws.com"));
  await assertDeniedBy(s, passRole("runner", "lambda.amazonaws.com"), [
    "role/runner",
  ]);
});

test("custom: ImageOwners replaces the default owners", async () => {
  const s = stack("custom");
  await assertDeniedBy(s, runInstances(s, { owner: CANONICAL }), ["image/"]);
});
