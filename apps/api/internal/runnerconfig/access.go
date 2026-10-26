package runnerconfig

import (
	"fmt"
	"regexp"
	"strings"
)

// Access is a parsed .access.yml: which repositories, and which people in
// them, may launch each organization runner. A runner with no
// access_control entry is open to every activated repository.
type Access struct {
	policies map[string]Policy
	// rules maps a runner name (lowercase) to the IDs of its policies.
	rules map[string][]string
}

// Policy allows launches on one repository by the listed users, members of
// the listed teams, or people holding one of the listed repository roles.
// A policy that lists none of them allows anyone who can start a workflow
// on the repository.
type Policy struct {
	ID    string
	Repo  string
	Teams []string
	Roles []string
	Users []string
	// PullRequest allows launches for pull_request workflows.
	PullRequest bool
}

// Open reports whether the policy names nobody, so anyone qualifies.
func (p Policy) Open() bool {
	return len(p.Teams) == 0 && len(p.Roles) == 0 && len(p.Users) == 0
}

type rawAccess struct {
	Version       int             `yaml:"version"`
	Policies      []rawPolicy     `yaml:"policies"`
	AccessControl []rawAccessRule `yaml:"access_control"`
}

type rawPolicy struct {
	ID          string   `yaml:"id"`
	Repo        string   `yaml:"repo"`
	Teams       []string `yaml:"teams"`
	Roles       []string `yaml:"roles"`
	Users       []string `yaml:"users"`
	PullRequest bool     `yaml:"pull_request"`
}

type rawAccessRule struct {
	Resource string   `yaml:"resource"`
	Policies []string `yaml:"policies"`
}

var (
	policyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	repoNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	slugPattern     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	loginPattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
)

const maxPolicies = 500

// ParseAccess reads and validates an .access.yml file.
func ParseAccess(data []byte) (Access, error) {
	if len(data) > MaxFileBytes {
		return Access{}, &Error{File: AccessFile, Detail: fmt.Sprintf("file is %d bytes; the limit is %d", len(data), MaxFileBytes)}
	}
	var raw rawAccess
	if err := decodeStrict(data, &raw); err != nil {
		return Access{}, &Error{File: AccessFile, Detail: err.Error()}
	}
	if raw.Version != 1 {
		return Access{}, &Error{File: AccessFile, Path: "version", Detail: "version must be 1"}
	}
	if len(raw.Policies) > maxPolicies || len(raw.AccessControl) > maxPolicies {
		return Access{}, &Error{File: AccessFile, Detail: fmt.Sprintf("at most %d policies and %d rules", maxPolicies, maxPolicies)}
	}

	out := Access{policies: map[string]Policy{}, rules: map[string][]string{}}
	for i, p := range raw.Policies {
		path := fmt.Sprintf("policies[%d]", i)
		policy, err := p.validate()
		if err != "" {
			return Access{}, &Error{File: AccessFile, Path: path, Detail: err}
		}
		if _, dup := out.policies[policy.ID]; dup {
			return Access{}, &Error{File: AccessFile, Path: path + ".id", Detail: fmt.Sprintf("policy %q is defined twice", policy.ID)}
		}
		out.policies[policy.ID] = policy
	}
	for i, rule := range raw.AccessControl {
		path := fmt.Sprintf("access_control[%d]", i)
		resource := strings.ToLower(strings.TrimSpace(rule.Resource))
		if !namePattern.MatchString(resource) {
			return Access{}, &Error{File: AccessFile, Path: path + ".resource", Detail: "resource must name a runner from " + GlobalFile}
		}
		if len(rule.Policies) == 0 {
			return Access{}, &Error{File: AccessFile, Path: path + ".policies", Detail: "list at least one policy; remove the rule to open the runner to everyone"}
		}
		for j, id := range rule.Policies {
			if _, ok := out.policies[id]; !ok {
				return Access{}, &Error{File: AccessFile, Path: fmt.Sprintf("%s.policies[%d]", path, j), Detail: fmt.Sprintf("no policy has id %q", id)}
			}
		}
		out.rules[resource] = append(out.rules[resource], rule.Policies...)
	}

	return out, nil
}

func (p rawPolicy) validate() (Policy, string) {
	out := Policy{ID: strings.TrimSpace(p.ID), Repo: strings.TrimSpace(p.Repo), PullRequest: p.PullRequest}
	if !policyIDPattern.MatchString(out.ID) {
		return Policy{}, "id is required"
	}
	// A policy names a repository in the organization, without the owner.
	if !repoNamePattern.MatchString(out.Repo) {
		return Policy{}, "repo must be a repository name in this organization, such as api"
	}
	for _, team := range p.Teams {
		if !slugPattern.MatchString(team) {
			return Policy{}, fmt.Sprintf("team %q is not a team slug", team)
		}
		out.Teams = append(out.Teams, strings.ToLower(team))
	}
	for _, role := range p.Roles {
		lower := strings.ToLower(strings.TrimSpace(role))
		if !slugPattern.MatchString(lower) {
			return Policy{}, fmt.Sprintf("role %q is not a repository role", role)
		}
		out.Roles = append(out.Roles, lower)
	}
	for _, user := range p.Users {
		if !loginPattern.MatchString(user) {
			return Policy{}, fmt.Sprintf("user %q is not a GitHub username", user)
		}
		out.Users = append(out.Users, strings.ToLower(user))
	}

	return out, ""
}

// Restricted returns the policies governing runner on repo. restricted is
// false when no rule mentions the runner; policies is then empty and every
// activated repository may launch it. A restricted runner with no policy
// for repo cannot be launched there.
func (a Access) Restricted(runner, repo string) (policies []Policy, restricted bool) {
	ids, ok := a.rules[strings.ToLower(runner)]
	if !ok {
		return nil, false
	}
	for _, id := range ids {
		if p := a.policies[id]; strings.EqualFold(p.Repo, repo) {
			policies = append(policies, p)
		}
	}

	return policies, true
}
