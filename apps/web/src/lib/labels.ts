/** Why Airship did not run a job that asked for one of its labels. */
export const skipReasonLabel: Record<string, string> = {
  access_denied: "The organization's .access.yml does not allow this run",
  config_invalid: "The repository's .cirun.yml has a problem",
  installation_suspended: "The GitHub App installation is suspended",
  no_cloud: "No cloud is connected",
  public_repository: "Public repository; public access is not allowed",
  repository_inactive: "The repository is not activated",
  unsatisfiable_labels: "No runner serves all of its labels",
};

/** Which file a runner definition came from. */
export const sourceLabel: Record<string, string> = {
  organization: ".cirun.global.yml",
  repository: ".cirun.yml",
};

/** GCP zones offered as a default; any zone or region works in `.cirun.yml`. */
export const gcpLocations = [
  "us-central1-a",
  "us-central1-b",
  "us-east1-b",
  "us-east4-a",
  "us-west1-a",
  "us-west2-a",
  "northamerica-northeast1-a",
  "southamerica-east1-a",
  "europe-west1-b",
  "europe-west2-a",
  "europe-west3-a",
  "europe-west4-a",
  "europe-north1-a",
  "asia-south1-a",
  "asia-southeast1-a",
  "asia-east1-a",
  "asia-northeast1-a",
  "australia-southeast1-a",
] as const;

/** AWS regions offered as a default; any region works in `.cirun.yml`. */
export const awsRegions = [
  "us-east-1",
  "us-east-2",
  "us-west-1",
  "us-west-2",
  "ca-central-1",
  "eu-west-1",
  "eu-west-2",
  "eu-west-3",
  "eu-central-1",
  "eu-north-1",
  "ap-south-1",
  "ap-southeast-1",
  "ap-southeast-2",
  "ap-northeast-1",
  "ap-northeast-2",
  "sa-east-1",
] as const;

/** Daytona regions offered as a default; any region of the organization works in `.cirun.yml`. */
export const daytonaRegions = ["us", "eu"] as const;
