/** Why Airship did not run a job that asked for one of its labels. */
export const skipReasonLabel: Record<string, string> = {
  ambiguous_labels: "More than one profile matches its labels",
  installation_suspended: "The GitHub App installation is suspended",
  public_repository: "Public repository; the profile does not allow them",
  unsatisfiable_labels: "Asks for labels the profile's runners do not have",
};
