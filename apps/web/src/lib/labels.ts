/** Why Airship did not run a job that asked for one of its labels. */
export const skipReasonLabel: Record<string, string> = {
  access_denied: "The organization's .access.yml does not allow this run",
  ambiguous_labels: "More than one profile matches its labels",
  config_invalid: "The repository's .airship.yml has a problem",
  installation_suspended: "The GitHub App installation is suspended",
  no_data_plane:
    "No connected data plane matches the runner's cloud and region",
  public_repository: "Public repository; public access is not allowed",
  repository_inactive: "The repository is not activated for runner config",
  unsatisfiable_labels: "No runner serves all of its labels",
};
