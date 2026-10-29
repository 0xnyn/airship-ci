package runnerconfig

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Access is a parsed access.yaml: which repositories, and which people in
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

// AccessDocument is access.yaml as written: the shape the developer API
// reads and edits.
type AccessDocument struct {
	Version       int              `json:"version"        yaml:"version"`
	Policies      []PolicyDocument `json:"policies"       yaml:"policies"`
	AccessControl []AccessRule     `json:"access_control" yaml:"access_control"`
}

// PolicyDocument is one policies[] entry.
type PolicyDocument struct {
	ID          string   `json:"id"                     yaml:"id"`
	Repo        string   `json:"repo"                   yaml:"repo"`
	Teams       []string `json:"teams,omitempty"        yaml:"teams,omitempty"`
	Roles       []string `json:"roles,omitempty"        yaml:"roles,omitempty"`
	Users       []string `json:"users,omitempty"        yaml:"users,omitempty"`
	PullRequest bool     `json:"pull_request,omitempty" yaml:"pull_request,omitempty"`
}

// AccessRule is one access_control[] entry.
type AccessRule struct {
	Resource string   `json:"resource" yaml:"resource"`
	Policies []string `json:"policies" yaml:"policies"`
}

var (
	policyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	repoNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	slugPattern     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	loginPattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
)

const maxPolicies = 500

// ParseAccess reads and validates an access.yaml file.
func ParseAccess(data []byte) (Access, error) {
	doc, err := DecodeAccess(data)
	if err != nil {
		return Access{}, err
	}

	return doc.Compile()
}

// DecodeAccess reads access.yaml without validating its rules.
func DecodeAccess(data []byte) (AccessDocument, error) {
	if len(data) > MaxFileBytes {
		return AccessDocument{}, &Error{File: AccessFile, Detail: fmt.Sprintf("file is %d bytes; the limit is %d", len(data), MaxFileBytes)}
	}
	var doc AccessDocument
	if err := decodeStrict(data, &doc); err != nil {
		return AccessDocument{}, &Error{File: AccessFile, Detail: err.Error()}
	}

	return doc, nil
}

// Encode renders the document as YAML.
func (d AccessDocument) Encode() ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return nil, fmt.Errorf("encode %s: %w", AccessFile, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode %s: %w", AccessFile, err)
	}

	return out.Bytes(), nil
}

// Compile validates the document and builds the lookup structure.
func (d AccessDocument) Compile() (Access, error) {
	if d.Version != 1 {
		return Access{}, &Error{File: AccessFile, Path: "version", Detail: "version must be 1"}
	}
	if len(d.Policies) > maxPolicies || len(d.AccessControl) > maxPolicies {
		return Access{}, &Error{File: AccessFile, Detail: fmt.Sprintf("at most %d policies and %d rules", maxPolicies, maxPolicies)}
	}

	out := Access{policies: map[string]Policy{}, rules: map[string][]string{}}
	for i, p := range d.Policies {
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
	for i, rule := range d.AccessControl {
		path := fmt.Sprintf("access_control[%d]", i)
		resource := strings.ToLower(strings.TrimSpace(rule.Resource))
		if !namePattern.MatchString(resource) {
			return Access{}, &Error{File: AccessFile, Path: path + ".resource", Detail: "resource must name a runner from " + GlobalFile}
		}
		if len(rule.Policies) == 0 {
			return Access{}, &Error{File: AccessFile, Path: path + ".policies", Detail: "list at least one policy; remove the rule to open the runner to everyone"}
		}
		for j, id := range rule.Policies {
			id = strings.TrimSpace(id)
			if _, ok := out.policies[id]; !ok {
				return Access{}, &Error{File: AccessFile, Path: fmt.Sprintf("%s.policies[%d]", path, j), Detail: fmt.Sprintf("no policy has id %q", id)}
			}
			out.rules[resource] = append(out.rules[resource], id)
		}
	}

	return out, nil
}

func (p PolicyDocument) validate() (Policy, string) {
	out := Policy{ID: strings.TrimSpace(p.ID), Repo: strings.TrimSpace(p.Repo), PullRequest: p.PullRequest}
	if !policyIDPattern.MatchString(out.ID) {
		return Policy{}, "id is required"
	}
	// A policy names a repository in the organization, without the owner.
	if !repoNamePattern.MatchString(out.Repo) {
		return Policy{}, "repo must be a repository name in this organization, such as api"
	}
	for _, team := range p.Teams {
		team = strings.TrimSpace(team)
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
		user = strings.TrimSpace(user)
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

// AccessChange is one repository_resource_access entry of the
// access-control API: grant or revoke a repository's access to resources.
type AccessChange struct {
	Repository  string
	Resources   []string
	Remove      bool
	Teams       []string
	Roles       []string
	Users       []string
	PullRequest *bool
}

// Apply edits the document. Granting finds or creates
// the repository's policy, adds the people listed, and attaches the policy
// to each resource; revoking detaches it, dropping rules left empty.
func (d *AccessDocument) Apply(change AccessChange) error {
	repo := strings.TrimSpace(change.Repository)
	if owner, name, ok := strings.Cut(repo, "/"); ok && owner != "" {
		repo = name
	}
	if !repoNamePattern.MatchString(repo) {
		return &Error{File: AccessFile, Detail: fmt.Sprintf("%q is not a repository name", change.Repository)}
	}
	if len(change.Resources) == 0 {
		return &Error{File: AccessFile, Detail: "list at least one resource"}
	}
	// Validate everything the change would write before touching the
	// document, so the error names the input rather than the file.
	for i, resource := range change.Resources {
		if !namePattern.MatchString(strings.ToLower(strings.TrimSpace(resource))) {
			return &Error{File: AccessFile, Path: fmt.Sprintf("resources[%d]", i), Detail: fmt.Sprintf("%q is not a runner name", resource)}
		}
	}
	for i, team := range change.Teams {
		if !slugPattern.MatchString(strings.TrimSpace(team)) {
			return &Error{File: AccessFile, Path: fmt.Sprintf("teams[%d]", i), Detail: fmt.Sprintf("%q is not a team slug", team)}
		}
	}
	for i, role := range change.Roles {
		if !slugPattern.MatchString(strings.ToLower(strings.TrimSpace(role))) {
			return &Error{File: AccessFile, Path: fmt.Sprintf("roles[%d]", i), Detail: fmt.Sprintf("%q is not a repository role", role)}
		}
	}
	for i, user := range change.Users {
		if !loginPattern.MatchString(strings.TrimSpace(user)) {
			return &Error{File: AccessFile, Path: fmt.Sprintf("users[%d]", i), Detail: fmt.Sprintf("%q is not a GitHub username", user)}
		}
	}
	if d.Version == 0 {
		d.Version = 1
	}

	policyIndex := slices.IndexFunc(d.Policies, func(p PolicyDocument) bool { return strings.EqualFold(p.Repo, repo) })
	if change.Remove {
		if policyIndex < 0 {
			return nil
		}
		id := d.Policies[policyIndex].ID
		kept := d.AccessControl[:0]
		for _, rule := range d.AccessControl {
			if slices.ContainsFunc(change.Resources, func(r string) bool { return strings.EqualFold(r, rule.Resource) }) {
				rule.Policies = slices.DeleteFunc(slices.Clone(rule.Policies), func(p string) bool { return p == id })
			}
			if len(rule.Policies) > 0 {
				kept = append(kept, rule)
			}
		}
		d.AccessControl = kept

		return nil
	}

	if policyIndex < 0 {
		d.Policies = append(d.Policies, PolicyDocument{ID: d.freePolicyID(repo + "-policy"), Repo: repo})
		policyIndex = len(d.Policies) - 1
	}
	policy := &d.Policies[policyIndex]
	policy.Teams = union(policy.Teams, change.Teams)
	policy.Roles = union(policy.Roles, change.Roles)
	policy.Users = union(policy.Users, change.Users)
	if change.PullRequest != nil {
		policy.PullRequest = *change.PullRequest
	}
	for _, resource := range change.Resources {
		resource = strings.TrimSpace(resource)
		ruleIndex := slices.IndexFunc(d.AccessControl, func(r AccessRule) bool { return strings.EqualFold(r.Resource, resource) })
		if ruleIndex < 0 {
			d.AccessControl = append(d.AccessControl, AccessRule{Resource: resource, Policies: []string{policy.ID}})

			continue
		}
		if !slices.Contains(d.AccessControl[ruleIndex].Policies, policy.ID) {
			d.AccessControl[ruleIndex].Policies = append(d.AccessControl[ruleIndex].Policies, policy.ID)
		}
	}

	return nil
}

// freePolicyID returns want, or want with a number appended when another
// repository's policy already has that id.
func (d AccessDocument) freePolicyID(want string) string {
	taken := func(id string) bool {
		return slices.ContainsFunc(d.Policies, func(p PolicyDocument) bool { return strings.EqualFold(p.ID, id) })
	}
	id := want
	for n := 2; taken(id); n++ {
		id = fmt.Sprintf("%s-%d", want, n)
	}

	return id
}

func union(have, add []string) []string {
	out := slices.Clone(have)
	for _, v := range add {
		v = strings.TrimSpace(v)
		if v != "" && !slices.ContainsFunc(out, func(h string) bool { return strings.EqualFold(h, v) }) {
			out = append(out, v)
		}
	}

	return out
}
