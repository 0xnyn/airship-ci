package gcp

import (
	"fmt"
	"regexp"
	"strings"
)

// Compute Engine places machines in zones (us-central1-b) grouped into
// regions (us-central1). Cirun's config field is called region but takes
// a zone; Airship accepts either. The control plane tracks runners per
// region and the provider picks the zone, unless the config pinned one.
var (
	zonePattern   = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+-[a-z]$`)
	regionPattern = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`)
)

// DefaultRegion is where runners launch when neither the config nor the
// credentials say.
const DefaultRegion = "us-central1"

// projectSeparator joins a region to a project in a runner's region
// field, when extra_config.project_id sends the runner to a project other
// than the credential's own. The inventory scan lists one (credential,
// region) at a time, so the project has to be part of what it lists.
const projectSeparator = "@"

// IsZone reports whether s is spelled like a zone.
func IsZone(s string) bool { return zonePattern.MatchString(s) }

// IsRegion reports whether s is spelled like a region.
func IsRegion(s string) bool { return regionPattern.MatchString(s) }

// RegionOf returns the region a zone belongs to.
func RegionOf(zone string) string {
	if i := strings.LastIndex(zone, "-"); i > 0 && IsZone(zone) {
		return zone[:i]
	}

	return zone
}

// SplitLocation reads a config or credential location: a zone gives its
// region and itself, a region gives itself and no zone. ok is false for
// anything else.
func SplitLocation(s string) (region, zone string, ok bool) {
	switch {
	case IsZone(s):
		return RegionOf(s), s, true
	case IsRegion(s):
		return s, "", true
	default:
		return "", "", false
	}
}

// Scope is where a credential's runners are listed: a region of a project.
type Scope struct {
	Project string
	Region  string
}

// EncodeScope renders the region field stored on a runner: the region
// alone when the runner launches in the credential's own project.
func EncodeScope(region, project, credentialProject string) string {
	if project == "" || project == credentialProject {
		return region
	}

	return region + projectSeparator + project
}

// ParseScope reads a runner's region field. credentialProject fills in
// the project when the field names none.
func ParseScope(field, credentialProject string) (Scope, error) {
	region, project, _ := strings.Cut(field, projectSeparator)
	if project == "" {
		project = credentialProject
	}
	if !IsRegion(region) {
		return Scope{}, fmt.Errorf("%q is not a Compute Engine region", region)
	}
	if !projectPattern.MatchString(project) {
		return Scope{}, fmt.Errorf("%q is not a project ID", project)
	}

	return Scope{Project: project, Region: region}, nil
}

// InstanceID is how a Compute Engine machine is recorded on a runner:
// zone/name, which is what every API call about it needs.
func InstanceID(zone, name string) string { return zone + "/" + name }

// ParseInstanceID splits an InstanceID.
func ParseInstanceID(id string) (zone, name string, err error) {
	zone, name, ok := strings.Cut(id, "/")
	if !ok || !IsZone(zone) || !namePattern.MatchString(name) {
		return "", "", fmt.Errorf("%q is not a Compute Engine instance ID (zone/name)", id)
	}

	return zone, name, nil
}

// namePattern is Compute Engine's resource name rule (RFC 1035 labels).
var namePattern = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)
